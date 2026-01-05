# nextask

Distributed task queue providing full reproducibility with non-intrusive source snapshotting.

Tasks are stored and managed in *PostgreSQL* with full stdout/stderr capture from _workers_. During `enqueue`, *nextask* can snapshot the working repository—including unstaged changes—to a remote git server, preserving the exact source code for execution by available _workers_.

## Installation

```bash
go install github.com/TolgaOk/nextask/cmd/nextask@latest
```

Or build from source:

```bash
git clone https://github.com/TolgaOk/nextask
cd nextask
go build -o nextask ./cmd/nextask
```

## Quick Start

```bash
# Initialize database
$ nextask init db --db-url "postgres://user@localhost:5432/nextask"

# Initialize source repository for snapshots (default: `~/.nextask/source.git`)
$ nextask init source

# Enqueue a task with source snapshot (optional)
$ nextask enqueue "python train.py" --db-url "..." --snapshot --remote ~/.nextask/source.git

# Start a worker (potentially in a remote machine)
$ nextask worker --db-url "..." --workdir /tmp/nextask

# List tasks
$ nextask list --db-url "..."
```

See `doc/CLI.md` for further details.

