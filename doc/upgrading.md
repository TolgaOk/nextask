# Upgrading to 0.2.0

If you already use `0.2.0-alpha`, this release introduces no additional schema or configuration changes.
Upgrade the CLI and workers together.
The migration steps and command changes below apply when upgrading from `0.1.x`.

1. Back up the database and install the new version on the machine submitting tasks and on every worker.
2. Replace passwords and keys in config with `${VARIABLE}` references.
   Set those variables on the machines that need them.
   Every config file is checked, including global defaults.
3. Run `nextask init db`.
   It adds columns without removing existing tasks or logs.
4. Restart workers before submitting tasks with Git or S3.

See [configuration](configuration.md) for complete examples.
S3 keys now belong in the endpoint, for example `https://${S3_ACCESS_KEY}:${S3_SECRET_KEY}@fsn1.your-objectstorage.com`.
Submit older S3 tasks again if their saved endpoint has no key references.
Older Git tasks need a saved commit and a supported remote URL; otherwise submit them again.

## Command changes

| Old setting or flag | Use now |
|---|---|
| `--db-url`, `defaults.db_url` | `db.url` or `NEXTASK_DB_URL` |
| `--snapshot` | `--with git` |
| `--remote` | `--set git.remote=...` |
| `source.remote`, `NEXTASK_SOURCE_REMOTE` | `integrations.git.remote`, `NEXTASK_GIT_URL` |
| `worker --filter` | `worker --tag` |

The old Git and worker names still work.
`--db-url` and `defaults.db_url` do not.
Empty JSON lists now return `[]`; CSV lists keep their headers.
Use either `--json` or `--csv`.
