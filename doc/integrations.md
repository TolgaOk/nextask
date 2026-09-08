# Enqueue with Git and S3

[Configure the database, Git remote and storage](configuration.md), then run:

```sh
nextask enqueue --with git --with s3 --attach \
  --set 's3.include=["outputs/**"]' './job.sh'
```

This saves a Git snapshot, queues the task, uploads matching output files from the worker, and shows live logs.
The worker needs Git and any software required by your command.

## Choose what to use

```sh
nextask enqueue 'echo hello'                     # no Git or storage
nextask enqueue --with git './job.sh'             # Git only
nextask enqueue --with s3 \
  --set 's3.include=["outputs/**"]' './job.sh'      # storage only
```

Git and S3 are off unless selected with `--with`. Config supplies settings; it does not turn them on.
Use `--set TOOL.KEY=VALUE` to change a setting for one task. Lists replace the configured list.
If you set an option more than once, the last value is used.

## What Git saves

- The current project files, including edits, deletions and new files that Git does not ignore.
- Your local files, staging area and branches stay unchanged.
- The snapshot is pushed to `<project>/<TASK_ID>` on the configured remote. An existing branch with that name causes an error.
- The worker downloads the saved commit and runs the command from that copy.

The repository needs at least one commit. Submodules are not supported.
Avoid editing files during enqueue. Both machines need access to the same Git remote; they may use different passwords or keys.
The remote can be a Git remote name, URL or repository path. To choose one for a task:

```sh
nextask enqueue --with git --set git.remote=snapshots './job.sh'
```

## Task IDs

Nextask generates an ID, or you can supply one with `--id export-42`.
IDs must be unique: 1–53 letters, digits, `_` or `-`, starting with a letter or digit.
Commands receive `NEXTASK_TASK_ID` and the worker's `NEXTASK_DB_URL`.

Deleting a task does not delete its Git snapshot or stored files. Failed enqueue can also leave a snapshot already pushed.
See [S3 storage](s3.md) for uploads and downloads.
