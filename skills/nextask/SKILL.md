---
name: nextask
description: Submit, monitor, and manage tasks and workers with the nextask CLI. Use when the user asks to use Nextask or work with an existing Nextask queue.
user-invocable: false
---

Use `nextask --version` and command-specific `--help` for the installed CLI.
Use `nextask config show --sources` to inspect settings without printing credentials.

## Critical points

- Quote the command passed to `enqueue`; it runs through `sh` in a fresh worker task directory.
  Local project files are available only if supplied, such as through `--with git`.
- Integrations are off by default; select `--with git` and/or `--with s3` explicitly.
  `--set TOOL.KEY=VALUE` overrides one task's settings; lists replace configured lists.
- Git snapshots include uncommitted changes and push to `<project>/<TASK_ID>` without changing the local repository.
  Both machines need Git and access to the remote; the repository needs an initial commit and cannot contain submodules.
- S3 requires explicit `include` or `final_include` patterns; `exclude` always wins.
  Abrupt worker loss preserves only artifacts already uploaded.
  `s3 fetch TASK_ID --to DIR` needs storage settings but no DB record.
- `wait` waits for all selected tasks; `wait --any` returns the first finished task's exit code.
  `log --attach` streams output without returning the task's exit code; `enqueue --attach` returns it.
- Ctrl+C on `log` or `wait` only stops watching; on `enqueue --attach` it requests cancellation.
- Each worker runs one task at a time.
  A stale task is not automatically resumed or requeued.
- `remove` deletes task records and logs but keeps Git snapshots and S3 artifacts.
- Connection templates use `${VARIABLE}` references; workers need those variables in their own environment.
  Task commands receive `NEXTASK_TASK_ID` and the worker's resolved `NEXTASK_DB_URL`.

Read [CLI reference](https://github.com/TolgaOk/nextask/blob/main/doc/cli.md) for commands, [configuration](https://github.com/TolgaOk/nextask/blob/main/doc/configuration.md) for connection settings, and [S3 storage](https://github.com/TolgaOk/nextask/blob/main/doc/s3.md) for artifact options.
