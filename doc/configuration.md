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

Supply the database connection through the environment on the CLI host and each
worker:

```sh
export NEXTASK_DB_URL='postgres://USER:PASSWORD@HOST:5432/DB?sslmode=require'
nextask init db
```

Nextask config files hold non-secret settings. Database URL settings are rejected,
including `db.url`, `nextask.db.url`, and `defaults.db_url`. `--db-url` has been
removed. Unknown Nextask config keys are also rejected. Each file is checked before
later files or environment overrides can replace its values.

Example config:

```toml
[integrations.git]
remote = "snapshots"                            # existing Git remote name, URL, or path; or NEXTASK_GIT_REMOTE

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
entry is a diagnostic view of `NEXTASK_DB_URL`. It is not a config-file setting.

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
directory created by that execution, after its integrations and log flushing finish.

Log batches retry temporary database failures. Shutdown allows up to five seconds
to flush pending batches; longer outages can leave output only in local task logs.
Those files are also removed when `--rm` is selected. Local log write or close
failures are reported and fail an otherwise successful task; an existing payload
failure code is preserved.

A worker retains a finished result and retries temporary database failures before
claiming another task. Worker shutdown allows another 30 seconds to save that
result. Permanent errors or an exhausted shutdown deadline stop the worker with
an error. A terminal status notification is sent only after the database write
succeeds. Results awaiting that write are held in memory; hard kills or an outage
that outlasts shutdown can still leave a task stale.

Workers check stored cancellation requests before execution and once per second
while a task runs. Notifications speed up cancellation, and status checks recover
missed requests and confirmations. Worker retries use the configured
`retry.initial_interval` and `retry.max_interval`.

Abrupt instance loss preserves completed uploads. A hard-killed worker cannot
finalize its task; the task becomes stale and is not automatically retried. Killing
only the worker process can leave its child commands running on the same host.
