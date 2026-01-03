# nextask

A non-intrusive, distributed task queue for ML experiments with full reproducibility.

> **Status**: Rewriting from Python + Redis to Go + PostgreSQL

## Features

- **Reproducible**: Every task captures the exact source code state at enqueue time
- **Distributed**: Workers can run anywhere, pulling tasks from a shared queue
- **Non-intrusive**: Does not modify your git history or working tree
- **Observable**: Full console log capture and streaming

## Quick Start

```bash
# Initialize database and source remote
nextask init db --db-url "postgres://user:pass@host:5432/nextask"
nextask init source

# Enqueue a task (from a git repository)
nextask enqueue "python train.py --lr 0.001" \
  --db-url "..." \
  --source-remote "~/.nextask/source.git"

# Start a worker (on any machine)
nextask worker --db-url "..." --workdir "/tmp/nextask"

# View tasks and logs
nextask list --db-url "..."
nextask logs <uuid> --db-url "..."
```

## Installation

```bash
go install github.com/nextask/nextask/cmd/nextask@latest
```

Or build from source:

```bash
git clone https://github.com/nextask/nextask
cd nextask
go build -o nextask ./cmd/nextask
```

## Commands

### Initialize Resources

```bash
# Create database tables
nextask init db --db-url "postgres://localhost/nextask"

# Create local bare git remote for source snapshots
nextask init source
nextask init source --path "/custom/path/source.git"

# SSH remote (not yet implemented)
# nextask init source --type ssh --url "git@server:nextask/source.git"

# Gitea remote (not yet implemented)
# nextask init source --type gitea --url "https://gitea.example.com" --org nextask
```

### Enqueue Tasks

Enqueue snapshots your source code and creates a task.

```bash
# Basic (must be run from a git repository)
nextask enqueue "python train.py" \
  --db-url "..." \
  --source-remote "~/.nextask/source.git"

# With tags (for filtering and metadata)
nextask enqueue "python train.py" \
  --tag device=gpu --tag lr=0.001 --tag experiment=baseline \
  --db-url "..." \
  --source-remote "..."
```

**What happens on enqueue:**
1. Verifies you're in a git repository
2. Creates a snapshot commit (includes uncommitted changes, respects .gitignore)
3. Pushes to `refs/nextask/<task-id>` on the source remote
4. Inserts task record into PostgreSQL
5. Prints task ID

**Your git repo is NOT modified** - no commits, no branch changes, no modified index.

### Workers

Workers poll for tasks, clone source, and execute commands.

```bash
# Start a worker
nextask worker --db-url "..." --workdir "/tmp/nextask"

# Named worker (for identification)
nextask worker --db-url "..." --name "gpu-node-01"

# Run single task and exit
nextask worker --db-url "..." --once

# Filter by tags (not yet implemented)
# nextask worker --db-url "..." --accepts gpu=a100
```

**What happens in worker loop:**
1. Poll database for pending tasks
2. Atomically claim task (status → running)
3. Clone source to `<workdir>/<task-id>/`
4. Run `setup.sh` if it exists
5. Execute command
6. Stream stdout/stderr to database
7. Update status (completed/failed) and exit code
8. Cleanup workdir

### View Tasks

```bash
# List all tasks
nextask list --db-url "..."

# Filter by status
nextask list --status pending --db-url "..."
nextask list --status running,failed --db-url "..."

# Filter by tag
nextask list --tag device=gpu --db-url "..."

# Filter by command (substring, case-insensitive)
nextask list --command "train.py" --db-url "..."
nextask list --command "train.py" --command "lr=0.001" --db-url "..."

# Filter by time
nextask list --since 1h --db-url "..."
nextask list --since 24h --db-url "..."
nextask list --since 7d --db-url "..."

# Limit results
nextask list --limit 10 --db-url "..."

# Combine filters
nextask list --status pending --tag device=gpu --since 24h --db-url "..."
```

### Task Details and Logs

```bash
# Show task details
nextask show <uuid> --db-url "..."

# View logs
nextask logs <uuid> --db-url "..."

# Follow logs in real-time (not yet implemented)
# nextask logs -f <uuid> --db-url "..."
```

### Task Management

```bash
# Cancel a task
nextask cancel <uuid> --db-url "..."
```

## Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                            ENQUEUE                                   │
│                                                                      │
│   1. Verify in git repo                                              │
│   2. Create snapshot commit (uncommitted + untracked)                │
│   3. Push to refs/nextask/<uuid>                                     │
│   4. Insert task into PostgreSQL                                     │
└──────────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────────────┐
│                          POSTGRESQL                                  │
│                                                                      │
│   tasks: id, command, status, source_remote, source_ref, tags, ...  │
│   task_logs: id, task_id, stream, data, created_at                  │
└──────────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────────────┐
│                            WORKER                                    │
│                                                                      │
│   1. Poll for pending task                                           │
│   2. Atomically claim (status=running)                               │
│   3. Clone source to workdir                                         │
│   4. Run setup.sh (if exists)                                        │
│   5. Execute command, stream logs to DB                              │
│   6. Update status (completed/failed)                                │
└──────────────────────────────────────────────────────────────────────┘
```

## Snapshots

Snapshots are stored in a bare git repository (the source remote):

```
source.git/refs/nextask/
├── vbshx9d0  → commit (snapshot 1)
├── abc12345  → commit (snapshot 2)
└── xyz98765  → commit (snapshot 3)
```

Unlike branches, snapshots are **standalone refs** - each points to a single commit capturing the code state at enqueue time. They share git's object store (deduplication) but aren't a linear chain.

```bash
# List all snapshots
git ls-remote ~/.nextask/source.git 'refs/nextask/*'

# Inspect a snapshot
git -C ~/.nextask/source.git show <commit>
```

## Source Backends

### Local (MVP)

```bash
nextask init source --path "~/.nextask/source.git"
```

Single machine setup. Source remote is a bare git repository on local filesystem.

### SSH (not yet implemented)

For multi-machine setups. Workers clone over SSH.

### Gitea (not yet implemented)

For teams. Integrates with Gitea API.

## Configuration (not yet implemented)

Future: `~/.config/nextask/config.toml` or `./nextask.toml`

```toml
[database]
url = "postgres://nextask:nextask@localhost:5432/nextask"

[source]
type = "local"
path = "~/.nextask/source.git"

[worker]
workdir = "/tmp/nextask"
```

## License

MIT
