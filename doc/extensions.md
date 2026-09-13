# Integrations

Nextask integrations are built-in modules backed by tool CLIs or Go libraries.
Git is implemented using the Git CLI. S3 uses `minio-go/v7`. Separate
tool CLIs can follow when an independent workflow needs them.

Each module declares its options, validates them, and implements
`prepare(ctx, task, options)`. Preparation may publish resources and return a wrapped
command. The worker executes that command normally. Runtime setup, child processes,
and finalization belong to the wrapper.

## CLI and config

```toml
[integrations.git]
remote = "snapshots"
```

Integrations are disabled by default. Use `--with TOOL` to enable one and
`--set TOOL.KEY=VALUE` to override its options. Config supplies options only.
Selection preserves order and removes duplicates. All options are validated before
preparing resources. Listed integrations run outermost first; preparation applies
wrappers in reverse order. Shared config uses `[nextask.integrations.git]`.
`config show --sources` includes effective options.

## Database credentials

Expect the full PostgreSQL connection string, including credentials, in
`NEXTASK_DB_URL`. Set it independently on the enqueue host and each worker.
This environment variable is already supported.

```sh
export NEXTASK_DB_URL='postgres://USER:PASSWORD@HOST:5432/DB?sslmode=require'
nextask enqueue 'some-command'
# On the worker, with its own NEXTASK_DB_URL configured:
nextask worker
```

DB connections are environment-only. Nextask rejects `db.url`, `nextask.db.url`,
`defaults.db_url`, and `--db-url`, with migration guidance for config keys. Each
config file is checked before overrides; errors omit credential values. Runtime
connection values are excluded from TOML serialization. Daemon workers inherit
the DB URL through the environment.

S3 names missing `S3_ACCESS_KEY` / `S3_SECRET_KEY` variables before starting the
payload. Git uses SSH or credential helpers. Credential-bearing Git/S3 connection
URLs and unknown Nextask config settings are rejected, including when an
integration is disabled. Shared sections owned by other tools remain theirs to
validate.

The worker injects its effective connection string as `NEXTASK_DB_URL` into task
processes alongside `NEXTASK_TASK_ID`. The submitter's connection string is not
serialized into the queued task. Config diagnostics redact credentials.

## Git

Enqueue reserves the task ID, captures files in a temporary repository, and pushes
`refs/heads/<project>/<TASK_ID>`. The local repository is read-only. The returned
command fetches the snapshot and checks out its recorded commit before executing the
payload. Git must be installed on the submitter and worker. See the
[integration guide](integrations.md) for file selection and [upgrading](upgrading.md)
for compatibility details.

The task keeps the original `command` for display and a separate
`execution_command` for execution. New prepared tasks use the generic `command`
source type. Legacy Git source descriptors are translated into execution commands.

## Lifecycle

Preparation failure prevents enqueue. Already published resources remain owned by
their tools if a later step fails. Removing a task does not remove external resources.
Wrappers preserve quoting, forward cancellation, wait for children and finalization,
and fail otherwise successful tasks when finalization fails.

Adding another built-in convenience integration requires a Nextask release. Any
standalone tool can already run through an ordinary task command. The S3
integration wraps execution with periodic uploads and a final sync.


## S3

The optional `s3` integration uploads to S3-compatible object storage using
[`minio-go/v7`](https://github.com/minio/minio-go). Hetzner is the initial target.
[Hetzner documents the client and regional endpoint configuration](https://docs.hetzner.com/storage/object-storage/getting-started/using-libraries/#for-go).
See the [S3 guide](s3.md) for setup, options, and operational details.

### Usage

```sh
nextask enqueue --with s3 './job.sh'
nextask enqueue --with git --with s3 './job.sh'
nextask enqueue --with s3 --set s3.interval=30s './job.sh'
nextask enqueue --with s3 --set s3.interval=0s './job.sh' # final upload only
```

```toml
[integrations.s3]
endpoint = "https://fsn1.your-objectstorage.com"
region = "fsn1"
remote = "s3://my-bucket/my-project"

root = "."
include = ["results/latest.json", "progress/**"]
exclude = ["**/*.tmp", "**/*.partial"]
final_include = ["reports/**", "results/final/**"]

interval = "60s"
final_sync = true
final_timeout = "2m"

concurrency = 4
upload_timeout = "5m"
retries = 3
on_final_error = "fail"
```

Use the endpoint and region for the destination bucket. The same block works in
Nextask user config; shared config uses `[nextask.integrations.s3]`. Configuration
supplies options only. Every task must explicitly select `--with s3`.

```sh
nextask enqueue --with s3 \
  --set s3.remote=s3://my-bucket/exports \
  --set 's3.include=["exports/**","summary.json"]' \
  --set 's3.exclude=["**/*.tmp","exports/cache/**"]' \
  --set s3.interval=30s \
  --set s3.concurrency=2 \
  --set s3.final_timeout=5m \
  './export.sh'
```

The common integration option mechanism accepts typed TOML values. Arrays
use JSON syntax in `--set` and replace the configured list. Repeated assignments
use the last value. Options are validated before preparing resources.

### Options

Every key supports `--set s3.KEY=VALUE`.

| Option | Default | Meaning |
|---|---|---|
| `endpoint` | Required | Object-storage service URL; HTTPS expected |
| `region` | Auto-detect where supported | Provider region |
| `remote` | Required | `s3://bucket/base-prefix`; append the task ID |
| `root` | `.` | Upload root relative to the worker task directory |
| `include` | Empty | Patterns uploaded periodically and at completion |
| `exclude` | Empty | Exclusions applied to every upload pass |
| `final_include` | Empty | Additional patterns uploaded only at completion |
| `interval` | `60s` | Delay between periodic passes; `0s` disables periodic uploads |
| `final_sync` | `true` | Upload after command completion, including failure |
| `final_timeout` | `2m` | Maximum duration of the final upload pass |
| `concurrency` | `4` | Concurrent file uploads; upload passes never overlap |
| `upload_timeout` | `5m` | Per-file time limit, including retries |
| `retries` | `3` | Additional attempts for transient upload failures |
| `min_age` | `0s` | Skip recently modified files during periodic passes; ignored for final upload |
| `max_file_size` | `unlimited` | Optional limit such as `2GiB`; report skipped files |
| `on_final_error` | `fail` | `fail` fails an otherwise successful task; `warn` reports upload failure |
| `symlinks` | `skip` | Skip symbolic links, or use `error` to reject them |

At least one inclusion pattern is required. Final uploads use `include` plus
`final_include`. Exclusions win. Always exclude `.git` and Nextask's internal files.

### Execution and retention

Enqueue records resolved integration settings with the task. The worker reads
`S3_ACCESS_KEY` and `S3_SECRET_KEY` from its environment at execution time;
credential values stay out of queued commands and configuration diagnostics.

Upload selected files to `<remote>/<TASK_ID>/<relative-path>`. Changed files
replace the same object keys. Nextask creates no directory per upload pass and
performs no automatic remote deletion. Removing a task preserves its stored objects.

Run periodic passes alongside the command, with no overlapping passes. Report
periodic failures and retry on subsequent passes. Task completion waits for final
sync on command success or failure. Preserve an existing command failure; apply
`on_final_error` to otherwise successful tasks.

Graceful cancellation stops the command and allows a bounded final-upload window.
Each wrapper declares its cleanup time. The task records the combined deadline,
and the worker adds it to its command-stop grace. Runtime wrappers pass inner
cleanup deadlines through when they compose. Cancellation remains cancellation.
Abrupt instance loss preserves only completed uploads. Uploads do not constitute
an atomic filesystem snapshot.

Tests cover option typing/precedence, filters and final-only selection, changed
files, timeouts/retries, credentials, command failure, cancellation, retention,
and composition with Git using disposable PostgreSQL and a local S3 fixture.

Live verification on Hetzner `fsn1` passed on 2026-09-06: periodic/final uploads,
unchanged-file skipping, exclusions, retention, a 70 MiB multipart readback with
SHA-256 verification, command failure, and cancellation. Test objects and bucket
were removed afterward.
