# CLI reference

Add `--help` to any command for all options.
Connection settings are covered in [configuration](configuration.md).

## Tasks

```sh
nextask enqueue 'hostname' --attach                         # submit and follow output
nextask enqueue 'python train.py' --with git --with s3      # save code and artifacts
nextask enqueue 'hostname' --id check-1 --tag batch=checks  # choose an ID and tag
nextask list --status pending,running --limit 10            # list matching tasks
nextask list --tag batch=checks --since 1h --json           # filter by tag and age
nextask show TASK_ID                                        # show task details
nextask cancel TASK_ID --timeout 30s                        # cancel and wait for confirmation
nextask remove TASK_ID                                      # delete the task and its logs
```

Quote the command passed to `enqueue`.
Git and S3 require `--with`; their settings come from config.
Removing a task keeps its Git snapshot and stored artifacts.

## Logs and waiting

```sh
nextask log TASK_ID --tail 20 --attach  # recent lines, then live output
nextask log TASK_ID --stream stderr     # show error output
nextask wait task-a task-b              # wait for both
nextask wait task-a task-b --any        # return when either finishes
nextask wait --tag batch=checks         # wait for matching tasks
nextask wait task-a --timeout 30s       # stop waiting after 30 seconds
```

- `wait` waits for all selected tasks and returns the first failure code it sees.
- `--any` returns the first finished task's code, including tasks already finished.
  Other tasks keep running.
- Waiting by tag includes matching tasks added while waiting.
  It ends when all selected tasks finish, or one with `--any`.
- A timeout returns `124`.
  Missing tasks and workers that stop reporting also cause an error.
- `log --attach` shows output without returning the task's exit code.
  `enqueue --attach` returns that code.
- Multiple terminals or agents can follow the same task independently.

## Workers

```sh
nextask worker                                  # start a worker
nextask worker --daemon --tag batch=checks      # run in background; only matching tasks
nextask worker --once --rm                      # run at most one task, then remove its files
nextask worker --timeout 2h --exit-if-idle 5m   # stop after 2 hours or 5 idle minutes
nextask worker list --status running --limit 5  # list active workers
nextask worker stop WORKER_ID --timeout 30s     # stop and wait for confirmation
```

Each worker runs one task at a time.
Stopping a worker also interrupts its current task.
Use `--workdir DIR` to choose where task files are kept.
`--rm` removes a task's directory after it finishes.

Both `list` commands support `--limit` (default 50), `--offset`, `--since`, `--status`, and either `--json` or `--csv`.
Task statuses are `pending`, `running`, `completed`, `failed`, `cancelled`, and `stale`.
Worker statuses are `running`, `stopped`, and `stale`.
`stale` means the worker has stopped reporting.

## Artifacts

```sh
nextask s3 fetch TASK_ID --to ./artifacts            # retrieve saved artifacts
nextask s3 fetch TASK_ID --to ./artifacts --dry-run  # preview without downloading
```

Fetch uses the configured artifact storage and needs no DB connection.
`--to` is required; add `--overwrite` to replace existing files.
Repeat `--include` and `--exclude` to choose files; exclusions win.
See [S3 storage](s3.md) for storage settings and limits.

## Configuration and help

```sh
nextask init db                # create or update database tables
nextask config show --sources  # show settings and their origins, with secrets hidden
nextask --version              # show the installed version
nextask --help                 # list commands
```

Durations accept values such as `30s`, `1h`, and `7d`; storage durations are limited to `24h`.
Check each command's help before using zero, since its meaning differs by option.

## Ctrl+C

| Command | What happens |
|---|---|
| `wait`, `log --attach` | Stop watching; the task keeps running. |
| `enqueue --attach` | Request cancellation and wait for the result. Press again to exit. |
| `cancel`, `worker stop` | After the request is sent, stop waiting for confirmation. The request remains in effect. |
