---
name: nextask-setup-services
description: Configure or deploy the PostgreSQL database and optional Git or S3 storage used by Nextask.
---

Check `nextask config show --sources` and reuse existing services where possible.
PostgreSQL is required; a Git server and S3 storage are needed only for their integrations.

## Guide the user

Prompt at each unresolved setup decision, one question at a time, with brief choices and a recommendation when useful.
Reuse answers already given; skip questions about services the user does not need.

- Which existing services should be reused, and which need to be created?
- Where should new services run: locally, on a remote host, or with a managed provider?
- Does the user want Git snapshots, S3 artifacts, or both?
- For selected integrations, which Git remote and storage provider/bucket should be used?
- Should new credentials be generated or existing ones used, and where should they be stored?

Have the user supply secrets through their environment or secret store rather than paste them into chat.
Wait for answers before taking dependent actions; continue read-only checks meanwhile.
Summarize the selected services, addresses, ports, and config destination before deployment, and confirm choices not already approved.

## Set up the required services

1. Identify the existing services, deployment host, and integrations the user wants.
   An existing PostgreSQL service, Git remote, or S3-compatible provider does not need a replacement deployment.
2. If deploying with Docker Compose, select a template below, copy it into the deployment directory, and fill a private `.env` from `env.example`.
   Set `DB_PASSWORD` and adjust the published ports if necessary.
3. Start the selected services with `docker compose up -d` and inspect `docker compose ps` and any failing service's logs.
   The combined template creates the `nextask` database and initializes a private Gitea `source` repository.
   The Gitea-only template requires account and repository setup through its web UI.
4. Configure the service addresses and credential references on the submitting machine and workers.
   Store any generated Gitea token through the chosen secret mechanism; do not repeat it in the response.
5. Run `nextask init db`, then `nextask list`, once the database and connection are ready.
6. Test only the selected integrations and report the service addresses, config location, and where credentials are stored.

For example, these connection settings belong in `.nextask.toml` or the user's global config:

```toml
[db]
url = "postgres://nextask:${DB_PASSWORD}@db.example:5432/nextask"

[integrations.git]
remote = "https://nextask:${GIT_TOKEN}@git.example/nextask/source.git"

[integrations.s3]
endpoint = "https://${S3_ACCESS_KEY}:${S3_SECRET_KEY}@storage.example"
remote = "s3://my-bucket/my-project"
```

Replace the example addresses, add the provider's S3 region if required, and omit unused integrations.
Choose artifact include patterns for the project or individual task.

## Critical points

- Create the database first, then run `nextask init db` to create or migrate its tables.
  It preserves existing task data; back up an existing database before upgrading.
- Put passwords and keys in environment variables referenced by connection templates, or supply complete connection URLs through the environment.
  Literal URL passwords in Nextask config are rejected.
  Nextask does not load `.env` files automatically, even though Docker Compose does.
- Use addresses reachable from both the submitting machine and workers.
  `localhost` inside a container refers to that container.
  Limit service access to the machines that need it.
- Git needs push access from the submitter and read access from workers.
  Use SSH keys, a credential helper, or an HTTPS token reference in the remote URL.
- S3 uses a service endpoint with access/secret key references and a separate `s3://bucket/prefix` destination.
  Create the bucket first; workers need object inspection/upload access and fetch clients need list/read access.
- Keep existing Docker volumes when restarting or updating services.
  The full-stack template's `gitea-init` logs contain a token, and rerunning it rotates that token; handle those logs as secrets.

For Docker Compose deployments, choose only the needed template: [PostgreSQL](scripts/postgres-only.docker-compose.yml), [Gitea](scripts/gitea-only.docker-compose.yml), or [both](scripts/full-stack.docker-compose.yml).
Use [env.example](scripts/env.example) for Compose variables and port overrides; keep the filled file private.
Paths are relative to this skill directory.

## Verify and diagnose

Verify DB access with `nextask list` from both the submitting machine and a worker.
Verify the selected integrations with a small task, then fetch its artifact if S3 is enabled.

- Connection refused: check that the service is healthy and its address and published port are reachable from the failing machine.
- Authentication failed: check the variables or Git authentication on that machine without printing their values.
- Gitea initialization failed: inspect `gitea-init` privately and check Gitea readiness before retrying; rerunning initialization invalidates the previous token.
- S3 uploads failed: check endpoint, bucket, region, permissions, and task include patterns.

See [configuration](https://github.com/TolgaOk/nextask/blob/main/doc/configuration.md) and [Git/S3 usage](https://github.com/TolgaOk/nextask/blob/main/doc/integrations.md) for details.
