---
name: nextask
description: Enqueue, monitor, and manage distributed tasks using the nextask CLI. Handles enqueueing commands with source snapshots, streaming live output, filtering and tagging tasks, managing workers, running sweeps, and batch processing. Use when the user mentions nextask, wants to run commands on remote workers, check task status, stream logs, manage a task queue, or organize runs with tags. Triggers even for "run this on the server", "check my training jobs", or "sweep over these parameters".
user-invocable: false
---

See the [README](https://github.com/TolgaOk/nextask) for install instructions and full documentation.

Use it to run commands on remote worker machines.

## Common workflows

### Enqueue a task
```bash
nextask enqueue "python train.py --lr 0.001" --snapshot --tag model=resnet,lr=0.001
```
- `--snapshot` captures the current working tree (including uncommitted changes) and pushes to a git remote
- `--remote <url|path>` override source remote for this snapshot
- `--tag key=value` adds metadata for filtering (repeatable)
- `--attach` / `-a` streams live output after enqueuing

### Monitor tasks
```bash
nextask list                              # all tasks
nextask list --status running             # filter by status
nextask list --tag sweep=exp3             # filter by tag
nextask list --command "train"            # search in command
nextask list --since 1h                   # recent tasks
nextask list --json                       # JSON output

nextask show <id>                         # task details

nextask log <id>                          # view output
nextask log <id> --attach                 # stream live output
nextask log <id> --tail 50 --attach       # last 50 lines + stream
nextask log <id> --stream stderr          # stderr only

nextask wait <id>                         # block until done
nextask wait --tag sweep=exp3             # wait for all matching
nextask wait <id1> <id2> --any            # wait for first to finish
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
nextask worker --once              # run one task then exit
nextask worker --once --rm         # run one task, clean up workdir, exit
nextask worker --daemon            # run as background process
nextask worker --timeout 24h       # stop after duration
nextask worker --exit-if-idle 5m   # exit if no tasks for 5 minutes
nextask worker --workdir /data/nextask  # custom workdir

nextask worker list                # show registered workers
nextask worker stop <id>           # stop a worker
```

> Example usages

### Hyperparameter sweep
```bash
for lr in 0.1 0.01 0.001; do
  nextask enqueue "python train.py --lr $lr" --snapshot --tag sweep=lr,lr=$lr
done
nextask wait --tag sweep=lr        # block until all finish
nextask list --tag sweep=lr        # compare results
```

### Run and watch
```bash
nextask enqueue "python train.py" --snapshot --attach
# Ctrl+C cancels the task
```

### Batch processing
```bash
for dataset in train val test; do
  nextask enqueue "python process.py --data $dataset" --tag job=preprocess,data=$dataset
done
nextask wait --tag job=preprocess
```

### One-off remote execution
```bash
nextask enqueue "nvidia-smi" --attach
# runs on whichever worker picks it up, output streamed back
```

### Route to specific workers
```bash
# Enqueue with tags
nextask enqueue "python train.py" --snapshot --tag gpu=a100

# Worker only claims matching tasks
nextask worker --filter gpu=a100
```

### Tagging and querying

Tags are key=value pairs for organizing, filtering, and routing tasks.

```bash
# Add tags at enqueue time (comma-separated or repeated)
nextask enqueue "python run.py" --tag project=myapp,env=staging

# Filter by any combination
nextask list --tag project=myapp
nextask list --tag project=myapp --status completed
nextask wait --tag project=myapp

# Route tasks to specific workers
nextask enqueue "python run.py" --tag gpu=a100
nextask worker --filter gpu=a100   # only picks up matching tasks
```

### Setup
```bash
nextask init db                    # create database tables
nextask config                     # show configuration
```

## Related skills
- `nextask-setup-services` — deploy PostgreSQL, PgBouncer, and a git server for snapshots. Use whenever services need to be set up, changed, or are not working.
- `nextask-setup-worker` — set up workers (local, remote, cloud). Use whenever a worker needs to be added or reconfigured.

## Configuration

Set `db.url` via `--db-url`, `NEXTASK_DB_URL`, or in `.nextask.toml` / `~/.config/nextask/global.toml`.
Set `source.remote` for snapshot storage.

## Key concepts
- Tasks go through: `pending` → `running` → `completed` | `failed` | `cancelled` | `stale`
- Workers claim tasks atomically from PostgreSQL
- Heartbeats detect stale workers; tasks on dead workers become `stale`
- Logs are captured in batches with stdout/stderr separation
- Snapshots preserve exact source code state without modifying the local repo. Pushed to `<source-remote>/refs/heads/<project-dir-name>/<taskID>` (e.g., for task `fmc17eq5` in a repo named `myproject`: `refs/heads/myproject/fmc17eq5`)

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Tasks stuck `pending` | Check `nextask worker list`, ensure `--filter` tags match `--tag` |
| Tasks go `stale` | Worker crashed, check logs and restart |
| Worker can't reach DB | Verify URL, check firewall, test with `nextask list --db-url "..."` from worker host |
| `git clone` fails in worker | Wrong source remote or token expired, verify with `git ls-remote` |
| `nextask list` shows nothing | Check `--db-url` or config, try `nextask config` to see loaded values |
