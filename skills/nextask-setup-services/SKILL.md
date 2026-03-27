---
name: nextask-setup-services
description: Deploy nextask backend services (PostgreSQL, PgBouncer, Gitea) on a VPS, local machine, or managed provider. Use when the user wants to set up the database, configure a git remote for snapshots, or deploy nextask services. Also triggers for "set up the task queue", "deploy postgres for nextask", or "I need a git server for snapshots."
---

Deploy nextask backend services. These are lightweight (1 CPU, 1GB RAM is enough) but should run on an always-on machine. A VPS is ideal. Local machines that sleep or change networks will disrupt workers.

On macOS, prefer `container` (Apple Container) over `docker` for running containers locally.

Compose files are in `${CLAUDE_SKILL_DIR}/scripts/`.

## SSH tips (for remote deployment)

When running commands on a remote server:
```bash
# Always use -o ConnectTimeout and run non-interactively
ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new user@host "command"

# For writing files remotely, pipe through ssh (don't use heredocs over ssh)
cat docker-compose.yml | ssh user@host "cat > /opt/nextask/docker-compose.yml"

# Or use scp
scp docker-compose.yml user@host:/opt/nextask/
```

Do NOT use `-t` or `-tt` for non-interactive commands. Do NOT run SSH in background and poll for results.

## PostgreSQL

Default: Docker Compose with PgBouncer (handles many workers, connection pooling).

Copy `scripts/postgres-only.docker-compose.yml` as `docker-compose.yml`. Create `.env`:
```
DB_PASSWORD=<strong random password>
```
```bash
docker compose up -d
```

Connection URL: `postgres://nextask:<password>@<host>:5432/nextask`

Alternatives:
- **Standalone container**: `docker run -d --name nextask-pg -p 5432:5432 -e POSTGRES_USER=nextask -e POSTGRES_PASSWORD=<pw> -e POSTGRES_DB=nextask -v nextask-pgdata:/var/lib/postgresql/data --restart unless-stopped postgres:17`
- **Existing PostgreSQL 14+**: user provides URL
- **Managed** (Supabase, Neon, RDS): user provides URL

Initialize tables:
```bash
nextask init db --db-url "postgres://nextask:<password>@<host>:5432/nextask"
```

**Verify:** `nextask list --db-url "..."` returns "No tasks found".

## Git remote (for `--snapshot`)

Skip if user won't use `--snapshot`. Tasks without it still work.

Default: Gitea container sharing the same Postgres.

Copy `${CLAUDE_SKILL_DIR}/scripts/full-stack.docker-compose.yml` and `${CLAUDE_SKILL_DIR}/scripts/init-db.sql`. Run `docker compose up -d`.

The compose file includes a `gitea-init` service that automatically:
- Creates an admin user (`nextask` with `DB_PASSWORD` as password)
- Generates an access token
- Creates a private `source` repo

Check the `gitea-init` logs for the token and remote URL:
```bash
docker compose logs gitea-init
```

The output will show: `Remote: http://nextask:<token>@<host>:3000/nextask/source.git`

Replace `<host>` with the server's IP.

If the auto-setup fails or the user needs a new token, they can do it manually via the Gitea web UI at `http://<host>:3000`:
1. Log in as `nextask`
2. Profile > **"Settings"** > **"Applications"** > generate new token with "Read and Write" repo permissions

Alternatives:
- **Standalone Gitea** (SQLite, no shared Postgres): use `${CLAUDE_SKILL_DIR}/scripts/gitea-only.docker-compose.yml`. Admin user must be created manually via the web UI.
- **Local bare repo** (single machine only): `git init --bare ~/.nextask/source.git`
- **GitHub/GitLab**: private repo with personal access token
- **Git daemon** (no auth, trusted networks only)

**Verify:** `git ls-remote "<remote>"` returns no error.

## VPS deployment

If deploying to a remote server, you need SSH access. Verify first:
```bash
ssh root@<host> "docker --version"
```

Then copy files and start:
```bash
scp docker-compose.yml init-db.sql .env root@<host>:/opt/nextask/
ssh root@<host> "cd /opt/nextask && docker compose up -d"
```

Open firewall ports: 5432 (DB), 3000 (Gitea if used).

## Configuration

**Keep secrets out of version control.** Use a `.env` file (add to `.gitignore`).

Create `.env` in the project root:
```
NEXTASK_DB_URL="postgres://nextask:<password>@<host>:5432/nextask"
NEXTASK_SOURCE_REMOTE="http://nextask:<token>@<host>:3000/nextask/source.git"
```

Source before running nextask: `source .env`

Non-secret config goes in `.nextask.toml`:
```toml
[source]
remote = "origin"
```

For global config: `~/.config/nextask/global.toml` (same format).

**Verify:** `nextask config` shows loaded files, `nextask list` works without flags.
