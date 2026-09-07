# Configuration

Config files:

```
~/.config/tasktools/config.toml           # optional shared user config
~/.config/nextask/global.toml             # Nextask user config
.tasktools.toml                          # optional shared project config
.nextask.toml                            # Nextask project config
```

Priority is CLI flags, environment variables, project files, user files, then
built-in defaults. Within each scope, the Nextask file overrides the shared
`[nextask]` section. Project files are read from the current directory. Missing files are optional.

Connection settings accept `${VARIABLE}` references with names you choose. Use a
complete URL from the environment, or reference credentials inside a URL template:

```toml
[db]
url = "postgresql://nextask:${DB_PASSWORD}@db.example:5432/nextask?sslmode=require"

[integrations.git]
remote = "https://nextask:${GIT_TOKEN}@git.example/nextask/source.git"

[integrations.s3]
endpoint = "https://${STORAGE_ACCESS}:${STORAGE_SECRET}@fsn1.your-objectstorage.com"
```

Alternatively, set `db.url = "${MY_DATABASE_URL}"`. `NEXTASK_DB_URL` overrides
`db.url`; `NEXTASK_GIT_URL` overrides `integrations.git.remote`;
`NEXTASK_S3_ENDPOINT` overrides `integrations.s3.endpoint`. These environment
variables contain complete connection values. Nextask stores references to them.

Passwords and S3 access keys in config must be environment references. Usernames,
hosts, ports, and paths can be literal. Each file is checked before later overrides.
Missing or blank referenced variables are named in errors. References are expanded
once, with URL escaping applied to each component. Complete URL environment values
must already use URL escaping. Database keyword connection strings are supported
through a complete environment value.

The CLI resolves its DB connection when loading configuration. Workers resolve Git
and S3 credentials at execution time; the submitter also resolves Git credentials
when publishing a snapshot. Set the referenced variables on the relevant machines.
`--db-url` and `defaults.db_url` are unsupported. Unknown config keys are rejected.

Example config:

```toml
[integrations.git]
remote = "snapshots"                            # existing Git remote name, URL, or path; or NEXTASK_GIT_URL

[worker]
workdir = "/tmp/nextask"                         # or NEXTASK_WORKER_WORKDIR
heartbeat_interval = "1m"                        # worker heartbeat frequency
stale_threshold = 3                              # missed heartbeats before marking a worker stale
log_flush_lines = 100                            # flush after this many lines
log_flush_interval = "500ms"                     # maximum time between flushes
log_buffer_size = 10000                          # log-line channel capacity
```

Shared config uses the same Nextask sections under `[nextask]`:

```toml
[nextask.worker]
workdir = "~/tasks"

[nextask.integrations.git]
remote = "ssh://git@server/snapshots.git"
```

Nextask reads its own section in shared files. Other tools own their sections.
Standalone Nextask config continues to work without a shared file or companion tools. Relative paths resolve
from the current directory; `~/` expands to the current user's home. Empty
environment variables are ignored. For core settings, zero numeric or duration values select the built-in defaults.
Integration options have their own validation and zero-value behavior.

```sh
nextask config show --sources
```

This prints effective values and their origins. `nextask config` also shows
effective values. Credentials, URL query values, and fragments are redacted;
keyword-style database connection strings are hidden entirely. The displayed `db.url`
entry shows the resolved DB connection with credentials hidden.

Tools may use the worker's `NEXTASK_TASK_ID` as their own caller-supplied ID and
`NEXTASK_DB_URL` as their database connection. They own their tables and migrations.
The join key in Nextask is `tasks.id`; task metadata includes `command`, `status`,
`tags`, `exit_code`, `created_at`, `started_at`, and `finished_at`. No Nextask foreign
key is required in another tool's tables.

See [integrations](integrations.md) for Git and S3 usage and [upgrading](upgrading.md) for migration steps.

## Worker files and shutdown

Each execution creates a fresh `<worker.workdir>/<TASK_ID>` directory. If that path
already exists, the task fails with a clear error and preserves the existing files.
Move that directory aside or use a new task ID. `worker --rm` removes only the
directory created by that execution, after its integrations, log flushing, and
completion journal write finish.

Log batches retry temporary database failures. Shutdown allows up to five seconds
to flush pending batches; longer outages can leave output only in local task logs.
Those files are also removed when `--rm` is selected. Local log write or close
failures are reported and fail an otherwise successful task; an existing payload
failure code is preserved.

A worker saves each finished result to `<worker.workdir>/.nextask/completions/`
before removing task files or writing the result to the database. Temporary database
failures retry before the worker claims another task. Shutdown allows another 30
seconds to save the result. Permanent errors or an exhausted shutdown deadline stop
the worker with an error and retain the journal. Terminal status notifications follow
a confirmed database write.

Use a separate workdir for each database. On startup, a worker replays pending
records from the same workdir before claiming new tasks, then removes acknowledged
records. Replay restores status, exit code, and finish time without rerunning
commands or integrations. Records identify the original claim, so deleted tasks and
reused task IDs cannot be overwritten. Already-saved results are safe to replay.
No new flag or schema migration is needed.

`--rm` preserves the journal. A journal write failure preserves task files and stops
the worker; a corrupt record blocks startup recovery and reports its path. Replay
never removes task directories, so a crash between journaling and cleanup can leave
files behind. Logs and artifacts are not part of the completion journal.

Workers check stored cancellation requests before execution and once per second
while a task runs. Notifications speed up cancellation, and status checks recover
missed requests and confirmations. Worker retries use the configured
`retry.initial_interval` and `retry.max_interval`. Claim attempts retry temporary
errors; permanent errors stop the worker. Delivered worker-stop notifications cancel
active claims and retry delays as well as running tasks.

Once registered, a worker attempts to mark itself stopped even if later startup
fails. Registration cleanup has an independent five-second deadline. Cleanup errors
are reported alongside the original error; shutdown confirmation follows the status
update.

Recovery requires a published journal record and surviving storage. Use a persistent
`--workdir` if results must survive reboot or instance replacement; the default
`/tmp/nextask` may be cleared. A crash before journaling can still leave a task stale.
Commands are never automatically retried, and killing only the worker process can
leave its child commands running. Abrupt instance loss preserves completed uploads.
