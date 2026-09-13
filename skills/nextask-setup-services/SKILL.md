---
name: nextask-setup-services
description: Configure or deploy the PostgreSQL database and optional Git or S3 storage used by Nextask.
---

Check `nextask config show --sources` and reuse existing services where possible.
PostgreSQL is required; a Git server and S3 storage are needed only for their integrations.

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

Verify DB access with `nextask list` from both the submitting machine and a worker.
Verify the selected integrations with a small task, then fetch its artifact if S3 is enabled.
See [configuration](https://github.com/TolgaOk/nextask/blob/main/doc/configuration.md) and [Git/S3 usage](https://github.com/TolgaOk/nextask/blob/main/doc/integrations.md) for details.
