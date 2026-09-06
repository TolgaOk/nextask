---
name: nextask-setup-services
description: Deploy nextask backend services (PostgreSQL, Gitea) on a VPS, local machine, or managed provider. Use when the user wants to set up the database, configure a git remote for snapshots, or deploy nextask services. Also triggers for "set up the task queue", "deploy postgres for nextask", or "I need a git server for snapshots."
---

See the [README](https://github.com/TolgaOk/nextask) for install instructions and full documentation.

Related skills: `nextask` (enqueue, monitor, manage tasks), `nextask-setup-worker` (set up workers).

Deploy nextask backend services. These are lightweight (1 CPU, 1GB RAM is enough) but should run on an always-on machine. A VPS is ideal. Local machines that sleep or change networks will disrupt workers.

Requires Docker with Docker Compose (`docker compose`). Apple Container (`container` CLI) does not support compose files and is not suitable for multi-service setups.

Compose files are in `${CLAUDE_SKILL_DIR}/scripts/`.

## Agent guidance

Guide the user through setup interactively. Follow this decision tree. Each answer determines the next question. Do not ask all questions upfront.

If `AskUserQuestion` is available, use it to present choices as structured options. Otherwise, ask in plain text.

**Quick reference:**
```
0. Check nextask (silent install if missing)
1. Check existing config → use it / start fresh
2. Where to deploy? → remote (recommend) / local
3. Check Docker (ask user to install if missing)
4. Snapshots? → no: skip to 7
5. Git remote? → Gitea / bare repo / GitHub / git daemon
6. Gitea admin? → auto / custom
7. DB password? → auto / custom
8. Agent runs: ports, compose, export environment, init db, write non-secret config, verify
```

### 0. Check nextask

Check `nextask --version`. If not installed, install with:
```bash
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash
```

### 1. Check existing config

Check for existing configuration:
```bash
nextask config show --sources
if [ -n "$NEXTASK_DB_URL" ]; then echo "NEXTASK_DB_URL is set"; else echo "NEXTASK_DB_URL is missing"; fi
```

If a config exists, tell the user what was found and ask: "You already have a nextask config. Do you want to use it, or start fresh?"

- **Use existing** → test the connection: `nextask list` and (if integrations.git.remote is set) `git ls-remote <remote>`.
  - Both work → setup is done, skip all remaining steps.
  - DB works but no source remote → ask if they want snapshots (jump to step 4).
  - DB fails → ask if they want to fix it or start fresh (jump to step 2).
- **Start fresh** → continue to step 2.

If no config found, continue to step 2.

> **Note:** When jumping to a step from here, the agent still needs Docker (step 3) if deploying new services. Use judgement: if only adding a source remote to an existing DB setup, Docker may not be needed.

### 2. "Where will you deploy — locally or on a remote server?"

- **Remote server/VPS** (recommend) → services stay up reliably. Verify SSH: `ssh -o ConnectTimeout=10 user@host "echo ok"`. Continue to step 3.
- **Local** → fine for testing, but local machines that sleep or change networks will disrupt workers. Continue to step 3.

### 3. Check Docker

Check `docker compose version` (locally, or via SSH if remote). If not installed, ask: "Docker is not installed. I can install it for you, or you can install it yourself and tell me when it's ready."

- **Agent installs**: macOS `brew install --cask docker`, Linux `curl -fsSL https://get.docker.com | sh`. On macOS, Docker Desktop needs to be launched before `docker compose` works. Tell the user to open it.
- **User installs**: wait for user to confirm, then re-check.

Continue once `docker compose version` succeeds.

### 4. "Do you want source snapshots (`--with git`)?"

Explain: snapshots capture your exact working tree (including uncommitted changes) so every task is reproducible. Requires a git remote to store them.

- **Yes** → continue to step 5
- **No** → skip to step 7 (Postgres only)

### 5. "For the git remote — self-hosted git server, local bare repo, GitHub/GitLab, or git daemon?"

- **Gitea** (recommend) → self-hosted, included in full-stack compose. Continue to step 6.
- **Local bare repo** → single-machine only. Skip to step 7.
- **GitHub/GitLab** → user provides repo URL + token. Skip to step 7.
- **Git daemon** → no auth, trusted networks only. Skip to step 7.

### 6. "For the Gitea admin — should I set it up automatically or do you want custom credentials?"

- **Auto** (recommend this) → username `nextask`, password = same as DB password. The compose handles everything. Continue to step 7.
- **Custom** → user must create admin manually via Gitea web UI after startup, then create token and repo (see troubleshooting table). More work, warn them. Continue to step 7.

### 7. "For the database password — should I generate one or do you want to set your own?"

- **Auto** → generate with `openssl rand -hex 24` and store through the chosen secret mechanism
- **Custom** → user provides

### 8. Agent actions (no more questions)

1. Check ports 5432 and 3000: `lsof -i :5432`, `lsof -i :3000`. If occupied, set `NEXTASK_PG_PORT` / `NEXTASK_GITEA_PORT` in `.env`. Tell user which ports will be used.
2. Copy compose file, write `.env`, run `docker compose up -d`.
3. If full stack: wait for `gitea-init` to complete, extract token from `docker compose logs gitea-init`.
4. Set `NEXTASK_DB_URL` in the CLI process environment, then run `nextask init db`.
5. Write only non-secret options, such as a credential-free `integrations.git.remote`, to `~/.config/nextask/global.toml`. DB URLs are rejected in Nextask/shared TOML and through `--db-url`.
6. Configure the database environment independently for each worker. Use the shell or service's secret mechanism; keep any persistent environment file outside the repository with mode 600. Configure Git SSH authentication or a credential helper separately on each host.
7. Verify `nextask list` and, if Git is used, `git ls-remote <credential-free-remote>`.
8. Tell the user where credentials are stored without repeating their values. Compose environment files supply service passwords; Nextask TOML contains only shareable options. Docker named volumes (`pgdata`, `gitea`) hold persistent service data.

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

## Full stack (PostgreSQL + Gitea)

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
1. `db` — PostgreSQL 17 (exposed on port 5432 or `NEXTASK_PG_PORT`)
2. `db-init` — creates the `gitea` database (one-shot, exits when done)
3. `gitea` — git server, exposed on port 3000 (or `NEXTASK_GITEA_PORT`)
4. `gitea-init` — creates admin user `nextask`, access token, and `source` repo (one-shot)

### Step 3: Get the remote URL

Wait for `gitea-init` to complete (~10-20 seconds after `docker compose up`), then:

```bash
docker compose logs gitea-init
```

The output shows:
```
Remote: http://nextask:<TOKEN>@<HOST>:3000/nextask/source.git
```

Capture the token into a Git credential helper or secret store on the submitter
and workers. Remove userinfo from the URL before configuring Nextask:
`http://<HOST>:3000/nextask/source.git`. Nextask rejects credential-bearing URLs.
Replace `<HOST>` with the server's IP or hostname; use `localhost` for local services.

If using a non-default Gitea port, adjust the URL accordingly (e.g., `:3001` instead of `:3000`).

### Step 4: Initialize nextask tables

```bash
export NEXTASK_DB_URL='postgres://nextask:<password>@<host>:5432/nextask'
nextask init db
```

Use the port from `NEXTASK_PG_PORT` if overridden.

### Step 5: Verify

```bash
# Database works
nextask list
# Expected: "No tasks found"

# Git remote works
git ls-remote "http://<host>:3000/nextask/source.git"
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

Use this if you won't use `--with git` or already have a git remote.

Copy `scripts/postgres-only.docker-compose.yml` as `docker-compose.yml`. Create `.env`:
```
DB_PASSWORD=<strong random password>
```

Port override if 5432 is taken: add `NEXTASK_PG_PORT=5433` to `.env`.

```bash
docker compose up -d
```

Connection URL: `postgres://nextask:<password>@<host>:5432/nextask` (use `NEXTASK_PG_PORT` if overridden).

Alternatives (no compose needed):
- **Existing PostgreSQL 14+**: user provides URL
- **Managed** (Supabase, Neon, RDS): user provides URL

Initialize tables:
```bash
export NEXTASK_DB_URL='postgres://nextask:<password>@<host>:5432/nextask'
nextask init db
```

**Verify:** `nextask list` with `NEXTASK_DB_URL` set returns "No tasks found".

## Git remote (standalone, without full stack)

Skip if using the full-stack compose (Gitea is included) or if user won't use `--with git`.

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

Supply `NEXTASK_DB_URL` through the shell or service environment. For persistent
local setup, use a private environment file outside the repository, such as
`~/.local/share/nextask/secrets.env`, with mode 600. Its content is:

```sh
NEXTASK_DB_URL='postgres://nextask:<password>@<host>:5432/nextask'
```

Load it into the process environment before running Nextask:

```sh
set -a
. ~/.local/share/nextask/secrets.env
set +a
```

Nextask does not load environment files automatically. Non-secret settings go in
`.nextask.toml` or `~/.config/nextask/global.toml`:

```toml
[integrations.git]
remote = "http://<host>:3000/nextask/source.git"
```

Use SSH authentication or Git credential helpers for Git access. For S3 tasks,
set `S3_ACCESS_KEY` and `S3_SECRET_KEY` independently on the worker. Never write
credentials into Nextask/shared TOML or commit private environment files.

**Verify:** `nextask config show --sources` shows redacted effective settings and
`nextask list` connects using `NEXTASK_DB_URL`.
