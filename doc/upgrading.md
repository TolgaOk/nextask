# Upgrading to 0.2.0

Move database connections to `NEXTASK_DB_URL` and remove DB URL entries from all
Nextask/shared config files, including empty entries. Replace `--db-url` usage with
the environment variable. Set it independently for each worker; daemon workers
inherit it through their environment. Move Git URL credentials into SSH or a Git
credential helper and use credential-free remote URLs. No Nextask-specific Git
secret environment variable is required.

Run `nextask init db` to add execution-command and cleanup-timeout columns, and upgrade workers before
enqueueing integrated tasks. Older workers reject the new command source type.
Existing queued Git tasks with a recorded commit are translated into the same
restoration command by the new worker. Tasks without a recorded commit fail with a
re-enqueue instruction rather than running a moving branch tip.

`--snapshot` and `--remote` remain deprecated aliases for `--with git` and
`--set git.remote=...`. Existing `source.remote` and `NEXTASK_SOURCE_REMOTE` settings
are accepted. The canonical setting wins over its alias in the same file;
`NEXTASK_GIT_REMOTE` wins over the old environment variable.

`nextask remove` deletes the task and its logs. Snapshot retention is explicit;
remove a remote snapshot with `git push snapshots --delete <project>/<TASK_ID>`.

Workers accept `--tag key=value`; `--filter` remains an alias. Both list commands
accept repeated or comma-separated `--status` values. Duration flags share units
such as `30s`, `1h`, and `7d`. Help describes where zero disables a deadline or
requests immediate idle exit.

Worker diagnostics now go to stderr. List and show timestamps use local time,
and tag displays are sorted. Empty JSON lists return `[]`; empty CSV lists retain
headers. `--json` and `--csv` are mutually exclusive, and `--wrap` applies only to
table output.
