---
name: nextask-setup-services
description: Deploy nextask backend services (PostgreSQL, PgBouncer, Gitea) on a VPS, local machine, or managed provider. Use when the user wants to set up the database, configure a git remote for snapshots, or deploy nextask services. Also triggers for "set up the task queue", "deploy postgres for nextask", or "I need a git server for snapshots."
---

See the [README](https://github.com/TolgaOk/nextask) for install instructions and full documentation.

Deploy nextask backend services. These are lightweight (1 CPU, 1GB RAM is enough) but should run on an always-on machine. A VPS is ideal. Local machines that sleep or change networks will disrupt workers.

Requires Docker with Docker Compose (`docker compose`). Apple Container (`container` CLI) does not support compose files and is not suitable for multi-service setups.

Compose files are in `${CLAUDE_SKILL_DIR}/scripts/`.

## Agent guidance

Guide the user through setup interactively. Follow this decision tree — each answer determines the next question. Do not ask all questions upfront.

### 1. "Where will you deploy — locally or on a remote server?"

- **Local** → continue to step 2
- **Remote** → verify SSH access (`ssh -o ConnectTimeout=10 user@host "docker compose version"`), then continue to step 2

### 2. "Do you want source snapshots (`--snapshot`)?"

Explain: snapshots capture your exact working tree (including uncommitted changes) so every task is reproducible. Requires a git remote to store them.

- **Yes** → continue to step 3
- **No** → skip to step 5 (Postgres only, no Gitea)

### 3. "For the git remote — Gitea (simplest, included), local bare repo, or GitHub/GitLab?"

- **Gitea** (recommend this) → continue to step 4
- **Local bare repo** → run `git init --bare ~/.nextask/source.git`, skip to step 5 (Postgres only compose). Set `source.remote` to that path.
- **GitHub/GitLab** → user provides a private repo URL and personal access token. Skip to step 5 (Postgres only compose). Set `source.remote` to the authenticated URL.

### 4. "For the Gitea admin — should I set it up automatically or do you want custom credentials?"

- **Auto** (recommend this) → username `nextask`, password = same as DB password. The compose handles everything.
- **Custom** → user must create admin manually via Gitea web UI after startup, then create token and repo (see troubleshooting table). More work — warn them.

### 5. "For the database password — should I generate one or do you want to set your own?"

- **Auto** → generate with `openssl rand -base64 24`, show to user
- **Custom** → user provides

### 6. Agent actions (no more questions)

1. Check ports 5432 and 3000: `lsof -i :5432`, `lsof -i :3000`. If occupied, set `NEXTASK_PG_PORT` / `NEXTASK_GITEA_PORT` in `.env`. Tell user which ports will be used.
2. Copy compose file, write `.env`, run `docker compose up -d`.
3. If full stack: wait for `gitea-init` to complete, extract token from `docker compose logs gitea-init`.
4. Run `nextask init db`.
5. Write `db.url` and `source.remote` to `~/.config/nextask/global.toml` (create directory if missing). This is the safe default — local to user's machine, not in version control. Do NOT write a project `.env` unless the user explicitly asks (risk of committing secrets).
6. Verify: `nextask list` (expect "No tasks found"), `git ls-remote <remote>` (expect refs listed).
7. Tell user: config written to `~/.config/nextask/global.toml`. If Gitea was set up, mention once that Gitea web login is username `nextask` with the database password.

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

## Full stack (PostgreSQL + PgBouncer + Gitea)

This is the recommended setup. The compose file includes a `gitea-init` service that automatically creates an admin user, access token, and private `source` repo.

### Step 1: Copy compose file and create `.env`

```bash
cp ${CLAUDE_SKILL_DIR}/scripts/full-stack.docker-compose.yml docker-compose.yml
```

Create `.env` in the same directory:
```
DB_PASSWORD=<strong random password>
```

**Port conflicts:** If port 5432 or 3000 is already in use, add overrides to `.env`:
```
NEXTASK_PG_PORT=5433
NEXTASK_GITEA_PORT=3001
```

### Step 2: Start services

```bash
docker compose up -d
```

This starts 5 services in order:
1. `db` — PostgreSQL 17
2. `db-init` — creates the `gitea` database (one-shot, exits when done)
3. `pgbouncer` — connection pooler, exposed on port 5432 (or `NEXTASK_PG_PORT`)
4. `gitea` — git server, exposed on port 3000 (or `NEXTASK_GITEA_PORT`)
5. `gitea-init` — creates admin user `nextask`, access token, and `source` repo (one-shot)

### Step 3: Get the remote URL

Wait for `gitea-init` to complete (~10-20 seconds after `docker compose up`), then:

```bash
docker compose logs gitea-init
```

The output shows:
```
Remote: http://nextask:<TOKEN>@<HOST>:3000/nextask/source.git
```

Replace `<HOST>` with the server's IP or hostname. If running locally, use `localhost`.

If using a non-default Gitea port, adjust the URL accordingly (e.g., `:3001` instead of `:3000`).

### Step 4: Initialize nextask tables

```bash
nextask init db --db-url "postgres://nextask:<password>@<host>:5432/nextask"
```

Use the port from `NEXTASK_PG_PORT` if overridden.

### Step 5: Verify

```bash
# Database works
nextask list --db-url "postgres://nextask:<password>@<host>:5432/nextask"
# Expected: "No tasks found"

# Git remote works
git ls-remote "http://nextask:<token>@<host>:3000/nextask/source.git"
# Expected: HEAD and refs/heads/main listed
```

### Troubleshooting (full stack)

| Symptom | Fix |
|---------|-----|
| `gitea-init` logs show "ERROR: Failed to create token" | Gitea may still be starting. Wait 30s, run `docker compose restart gitea-init` |
| `db-init` or `gitea` won't start | Check `docker compose logs db` for Postgres errors |
| Port conflict on startup | Add `NEXTASK_PG_PORT` / `NEXTASK_GITEA_PORT` to `.env` and recreate: `docker compose up -d` |
| Need a new token | `docker compose restart gitea-init` — it deletes the old token and creates a fresh one |
| Manual Gitea admin access | Web UI at `http://<host>:3000`, login as `nextask` with `DB_PASSWORD`. Profile > Settings > Applications to manage tokens |
| `docker exec` commands fail with "not supposed to be run as root" | Always use `docker exec --user git <container> gitea ...` |

## PostgreSQL only (no Gitea)

Use this if you won't use `--snapshot` or already have a git remote.

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

## Git remote (standalone, without full stack)

Skip if using the full-stack compose (Gitea is included) or if user won't use `--snapshot`.

Options:
- **Standalone Gitea** (SQLite, no shared Postgres): use `${CLAUDE_SKILL_DIR}/scripts/gitea-only.docker-compose.yml`. Admin user must be created manually via the web UI at `http://<host>:3000`.
- **Local bare repo** (single machine only): `git init --bare ~/.nextask/source.git`
- **GitHub/GitLab**: private repo with personal access token
- **Git daemon** (no auth, trusted networks only)

**Verify:** `git ls-remote "<remote>"` returns no error.

## VPS deployment

If deploying to a remote server, you need SSH access. Verify first:
```bash
ssh root@<host> "docker --version && docker compose version"
```

Then copy files and start:
```bash
scp docker-compose.yml .env root@<host>:/opt/nextask/
ssh root@<host> "cd /opt/nextask && docker compose up -d"
```

Open firewall ports: `NEXTASK_PG_PORT` (default 5432) for DB, `NEXTASK_GITEA_PORT` (default 3000) for Gitea if used.

Get the remote URL:
```bash
ssh root@<host> "cd /opt/nextask && docker compose logs gitea-init"
```

## Configuration

**Keep secrets out of version control.** Use a `.env` file (add to `.gitignore`).

After setup, configure the nextask client. Create `.env` in the project root:
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
