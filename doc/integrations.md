# Integrations

All integrations are disabled by default. Git and S3 ship with Nextask; select
each with `--with TOOL`. Config only supplies integration options.
Plain tasks work without Git. Install Git on the submitter and workers for Git tasks,
and configure a remote both machines can reach using their own Git credentials.
Use SSH keys, Git credential helpers, or an HTTP(S) URL with credential references,
such as `https://nextask:${GIT_TOKEN}@git.example/repo.git`. A complete URL can also
come from `NEXTASK_GIT_URL` or a custom reference. Remote names remain supported;
their fetch and push URLs follow the same rules. Literal passwords, URL queries,
and fragments are rejected. HTTP and SSH usernames can be literal.

Git resolves credentials independently on the submitter and worker. The queued
snapshot contains the credential reference and the resolved repository URL with
credentials removed. The worker can use a different token for that same repository;
a changed destination fails before checkout. Credentials reach Git through a
process-scoped credential helper and are not written into `.git/config`.

```sh
nextask enqueue --with git --set git.remote=snapshots './job.sh'
nextask enqueue --with git './job.sh'        # use the configured remote
nextask enqueue 'echo hello'                 # plain task
nextask enqueue --with git --set git.remote=archive './job.sh'
nextask worker
```

`--with TOOL` is repeatable, preserves order, and removes duplicates. `--set`
accepts repeatable `TOOL.KEY=VALUE` overrides and requires that tool to be selected.
Names and options are validated before preparation. TOML options use native types;
`--set` lists use JSON arrays and replace the configured list. Repeated assignments
use the last value. Config cannot enable an integration.

Enqueue reserves the task ID, prepares integrations, then publishes the task.
Preparation failure rolls back the task; resources already published before a later
failure remain at their remote. Integrations wrap commands in reverse order so the
first selected integration runs outermost. The original command remains visible in
task listings; the separate execution command contains setup and any finalization.

Git snapshots the repository root, including current tracked file contents,
deletions, and non-ignored untracked files. Staged files are captured as they exist
on disk. Symlinks and executable modes are preserved. File bytes are stored without
running Git clean filters. Submodules and special files are currently rejected.
The repository needs an initial commit and a directory name valid in a Git ref.

All snapshot objects, index entries, and refs are written to temporary storage.
The local working tree and `.git` remain unchanged. Publication creates
`refs/heads/<project>/<TASK_ID>` and refuses an existing snapshot ref. The worker
fetches that ref, checks out the recorded commit in detached HEAD state, and runs
the payload from the task directory. A missing commit fails the task before the
payload starts. The worker checkout has an `origin` remote for explicit Git use.

Cancellation reaches the wrapper and its children through the worker's process
group. Wrappers declare cleanup time, which the worker adds to its stop deadline.
Git has no finalization step. Other integrations must wait for their children
and finalization, and report finalization errors through the command's exit status.
Snapshot capture reads files over time; avoid concurrent file edits during enqueue
when a consistent snapshot is required.

## S3 storage

Configure a destination and explicit file selection in `.nextask.toml`:

```toml
[integrations.s3]
endpoint = "https://${STORAGE_ACCESS}:${STORAGE_SECRET}@fsn1.your-objectstorage.com"
region = "fsn1"
remote = "s3://my-bucket/my-project"
include = ["outputs/**"]
exclude = ["**/*.tmp"]
final_include = ["reports/**"]
interval = "60s"
```

Set the referenced variables on the worker using the provider's object-storage
access/secret key pair. Variable names are your choice; there are no implicit
`S3_ACCESS_KEY` or `S3_SECRET_KEY` lookups. A complete authenticated endpoint can
also come from `NEXTASK_S3_ENDPOINT`. No separate storage CLI is needed.
The bucket must already exist. Shared config uses `[nextask.integrations.s3]`.
Missing credentials are named before the command starts. Config rejects literal
credentials even when the integration is disabled.

```sh
nextask enqueue --with s3 './job.sh'
nextask enqueue --with git --with s3 './job.sh'
nextask enqueue --with s3 --set s3.interval=0s './job.sh' # final upload only
nextask enqueue --with s3 --set 's3.include=["exports/**","summary.json"]' './job.sh'
```

Files upload to `<remote>/<TASK_ID>/<relative-path>`. Changed content replaces the
same object key; unchanged content is skipped. Final sync runs on success, failure,
and graceful cancellation with a bounded deadline. Abrupt instance loss preserves
only uploads already completed. Removing a task keeps its stored objects.

See the [S3 guide](s3.md) for all options, defaults, limits, and error handling.

## Task identity and environment

Supply an ID with `nextask enqueue --id export-42 'echo ready'`, or omit `--id`
to generate one. IDs contain 1–53 ASCII letters, digits, underscores or hyphens,
starting with a letter or digit. Duplicate IDs are rejected before snapshot work.

Task commands receive `NEXTASK_TASK_ID` and `NEXTASK_DB_URL` from their worker,
overriding inherited values. Use these to pass task identity and the worker's database
connection to independently usable tools. Other environment variables are inherited.
