# nextask

A non-intrusive, distributed task queue for ML experiments with full reproducibility.

> **Status**: Rewriting from Python + Redis to Go + PostgreSQL

## Features

- **Reproducible**: Every task captures the exact source code state at enqueue time
- **Distributed**: Workers can run anywhere, pulling tasks from a shared queue
- **Non-intrusive**: Works with your existing setup (uv, conda, bash, docker)
- **Observable**: Full console log capture and streaming

## Quick Start

```bash
# Initialize database
nextask init db --url "postgres://user:pass@host:5432/nextask"

# Enqueue a task
nextask enqueue "python train.py --lr 0.001"

# Start a worker (on any machine)
nextask worker

# Interactive mode - wait and stream logs
nextask enqueue -i "python train.py"

# Attach to a running task
nextask attach <uuid>
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

## Configuration

Create `~/.config/nextask/config.toml` or `./nextask.toml`:

```toml
[database]
url = "postgres://nextask:nextask@localhost:5432/nextask"

[source]
type = "local"  # or "ssh", "gitea"

[source.local]
path = "~/.nextask/source.git"

[defaults]
init = "uv"
```

## Commands

### Enqueue Tasks

```bash
# Basic
nextask enqueue "python train.py"

# With init method
nextask enqueue --init uv "python train.py"
nextask enqueue --init conda:environment.yaml "python train.py"
nextask enqueue --init bash:setup.sh "python train.py"
nextask enqueue --init docker:Dockerfile "python train.py"

# With labels (for worker filtering)
nextask enqueue --label gpu=a100 --label project=rl "python train.py"

# With metadata (for querying)
nextask enqueue --tag experiment=baseline --tag lr=0.001 "python train.py"

# Interactive mode
nextask enqueue -i "python train.py"
```

### Workers

```bash
# Start a worker
nextask worker

# Named worker
nextask worker --name gpu-node-01

# Filter by labels (only accept matching tasks)
nextask worker --accepts gpu=a100

# Run single task and exit
nextask worker --once
```

### Attach/Detach

```bash
# Attach to running task
nextask attach <uuid>

# Detach: Ctrl+P Ctrl+Q (task continues)
# Cancel/Kill: Ctrl+C (prompts for action)
```

### View Tasks

```bash
# List tasks
nextask list
nextask list --status running
nextask list --label gpu=a100
nextask list --tag experiment=baseline
nextask list --since 24h

# Show task details
nextask show <uuid>

# View logs
nextask logs <uuid>
nextask logs -f <uuid>          # follow
nextask logs --stderr <uuid>    # filter
```

### Task Management

```bash
# Cancel pending task
nextask cancel <uuid>

# Kill running task
nextask cancel <uuid>        # SIGTERM
nextask cancel --force <uuid> # SIGKILL
```

## Architecture

```
+------------------+         +------------------+
|  User machine    |         |  Worker node     |
|                  |         |                  |
|  nextask enqueue +-------->+  nextask worker  |
|                  |   DB    |                  |
+--------+---------+         +--------+---------+
         |                            |
         v                            v
+------------------+         +------------------+
|  Source Remote   |<--------+  Clone + Run     |
|  (git)           |         |  task            |
+------------------+         +------------------+
```

## How It Works

1. **Enqueue**: `nextask enqueue` snapshots your source code (including uncommitted changes) to a git ref and inserts a task record into PostgreSQL.

2. **Worker**: `nextask worker` polls the database, claims a pending task atomically, clones the source snapshot, initializes the environment, runs the command, and streams logs back.

3. **Attach**: `nextask attach` or `enqueue -i` polls the database for new logs and streams them to your terminal.

## Source Backends

### Local (default)
```toml
[source]
type = "local"

[source.local]
path = "~/.nextask/source.git"
```

### SSH
```toml
[source]
type = "ssh"

[source.ssh]
url = "git@server:nextask/source.git"
key_file = "~/.ssh/id_nextask"
```

### Gitea
```toml
[source]
type = "gitea"

[source.gitea]
url = "https://gitea.example.com"
org = "nextask"
token_env = "GITEA_TOKEN"
```

## Worker Filtering

Tasks can have labels, and workers can filter by them:

```bash
# Enqueue task requiring A100 GPU
nextask enqueue --label gpu=a100 "python train.py"

# Worker only accepts A100 tasks
nextask worker --accepts gpu=a100

# Worker accepts any task (default)
nextask worker
```

The matching rule: `task.labels @> worker.accepts` (task labels must contain all worker accepts).

## License

MIT
