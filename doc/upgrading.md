# Upgrading to 0.2.0-alpha

Replace literal credentials in config with `${VARIABLE}` references, or supply
complete connection URLs through `NEXTASK_DB_URL`, `NEXTASK_GIT_URL`, and
`NEXTASK_S3_ENDPOINT`. `db.url` accepts a URL template or a whole variable reference.
Replace `--db-url` with one of those DB settings. Set referenced variables independently
on each worker; daemon workers inherit their environment. Git SSH keys and credential
helpers continue to work.

S3 credentials now come from the endpoint URL. Existing `S3_ACCESS_KEY` and
`S3_SECRET_KEY` variables work when explicitly referenced, for example
`https://${S3_ACCESS_KEY}:${S3_SECRET_KEY}@fsn1.your-objectstorage.com`.
Re-enqueue S3 tasks prepared with the previous credential-free endpoint format.

Run `nextask init db` to add execution-command and cleanup-timeout columns, and upgrade workers before
enqueueing integrated tasks. Older workers reject the new command source type.
Existing queued Git tasks with a recorded commit are translated into the same
restoration command by the new worker. Tasks without a recorded commit fail with a
re-enqueue instruction rather than running a moving branch tip.

`--snapshot` and `--remote` remain deprecated aliases for `--with git` and
`--set git.remote=...`. Existing `source.remote` and `NEXTASK_SOURCE_REMOTE` settings
are accepted. The canonical setting wins over its alias in the same file;
`NEXTASK_GIT_URL` wins over `NEXTASK_GIT_REMOTE`, which wins over the old variable.

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
