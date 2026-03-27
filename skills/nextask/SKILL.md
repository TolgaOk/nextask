---
name: nextask
description: Enqueue, monitor, and manage distributed tasks using the nextask CLI. Handles enqueueing commands with source snapshots, streaming live output, filtering tasks by tags, managing workers, and running hyperparameter sweeps. Use when the user mentions nextask, wants to run commands on remote workers, check task status, stream logs, or manage a task queue, even if they just say "run this on the server" or "check my training jobs."
user-invocable: false
---

You have access to `nextask`, a CLI tool for distributed task execution. Use it to run commands on remote worker machines.

## Common workflows

### Enqueue a task
```bash
nextask enqueue "python train.py --lr 0.001" --snapshot --tag model=resnet
```
- `--snapshot` captures the current working tree (including uncommitted changes) and pushes to a git remote
- `--tag key=value` adds metadata for filtering
- `--attach` streams live output after enqueuing

### Monitor tasks
```bash
nextask list --status running --tag sweep=exp3
nextask show <id>
nextask log <id> --attach          # stream live output
nextask wait <id>                  # block until done
```

### Cancel / remove
```bash
nextask cancel <id>
nextask remove <id>                # deletes task, logs, and snapshot
```

### Workers
```bash
nextask worker                     # start picking up tasks
nextask worker --filter gpu=a100   # only matching tasks
nextask worker list                # show registered workers
nextask worker stop <id>           # stop a worker
```

### Setup
```bash
nextask init db                    # create database tables
nextask config                     # show configuration
```

## Configuration

Set `db.url` via `--db-url`, `NEXTASK_DB_URL`, or in `.nextask.toml` / `~/.config/nextask/global.toml`.
Set `source.remote` for snapshot storage.

## Key concepts
- Tasks go through: `pending` → `running` → `completed` | `failed` | `cancelled` | `stale`
- Workers claim tasks atomically via PostgreSQL `FOR UPDATE SKIP LOCKED`
- Heartbeats detect stale workers; tasks on dead workers become `stale`
- Logs are captured per-line with stdout/stderr separation
- Snapshots preserve exact source code state without modifying the local repo
