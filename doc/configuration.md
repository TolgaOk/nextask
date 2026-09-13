# Configuration

Put project settings in `.nextask.toml` and user defaults in `~/.config/nextask/global.toml`.
Only the database is required for queuing tasks. Git and S3 are optional and off unless selected with `--with`.

```toml
[db]
url = "postgres://nextask:${DB_PASSWORD}@db.example:5432/nextask"

[integrations.git]
remote = "https://nextask:${GIT_TOKEN}@git.example/nextask/source.git"

[integrations.s3]
endpoint = "https://${STORAGE_ACCESS}:${STORAGE_SECRET}@fsn1.your-objectstorage.com"
region = "fsn1"
remote = "s3://my-bucket/my-project"

[worker]
workdir = "~/nextask-work"
```

Replace the addresses and bucket with yours. The storage example uses Hetzner.
Choose artifact paths per task, or set them in project config when all tasks use the same paths:

```sh
nextask enqueue 'python train.py' --with git --with s3 --set 's3.include=["outputs/**"]'
```

## Files and overrides

Settings are applied in this order; later values override earlier ones:

1. Built-in defaults.
2. `~/.config/tasktools/config.toml` — shared user settings.
3. `~/.config/nextask/global.toml` — Nextask user settings.
4. `.tasktools.toml` — shared project settings.
5. `.nextask.toml` — Nextask project settings.
6. Environment overrides listed below.
7. Command options, where available: `worker --workdir` and `enqueue --set TOOL.KEY=VALUE`.

Missing files are skipped. Project files are read only from the current directory, not parent directories.
Relative worker paths are resolved from that directory; `~/` expands to your home directory.
Settings merge by key. Lists replace the previous list; they do not append.
Repeated `--set` options use the last value. An override applies only to that command and does not rewrite config.

Shared files use the same settings beneath `[nextask]`, for example:

```toml
[nextask.db]
url = "postgres://nextask:${DB_PASSWORD}@db.example:5432/nextask"

[nextask.integrations.git]
remote = "origin"
```

Other tools' sections are ignored. Unknown Nextask settings cause an error.
There is no `enqueue.with` setting: select integrations on each enqueue command.

## Passwords and environment variables

Keep passwords and keys in environment variables; config holds references such as `${DB_PASSWORD}`.
You choose the variable names. Usernames, hosts, ports and database names can be literal or references:

```toml
[db]
url = "postgres://nextask:${NEXTASK_DB_PW}@${NEXTASK_DB_IP}:${NEXTASK_DB_PORT}/nextask"
```

Export those variables before starting Nextask, or provide them through the worker's service configuration.
Nextask does not load `.env` files. References are supported in connection settings, not every config value.
Use `${NAME}`, not `$NAME` or shell expressions. Values inserted into URL components are escaped automatically,
so a password containing `@`, `:` or `/` can be supplied as-is.

| Environment override | Replaces |
|---|---|
| `NEXTASK_DB_URL` | `db.url` |
| `NEXTASK_GIT_URL` | `integrations.git.remote` |
| `NEXTASK_GIT_REMOTE` | Same; lower priority than `NEXTASK_GIT_URL` |
| `NEXTASK_SOURCE_REMOTE` | Older Git alias; lower priority than both above |
| `NEXTASK_S3_ENDPOINT` | `integrations.s3.endpoint` |
| `NEXTASK_WORKER_WORKDIR` | `worker.workdir` |

You can also reference a complete connection value, such as `url = "${MY_DATABASE_URL}"`.
Complete URLs must already have valid URL escaping. References are expanded once, without expanding references inside their values.
There are no automatic `S3_ACCESS_KEY` or `S3_SECRET_KEY` overrides: use those names in your endpoint template if you prefer them.

| Where | Required credentials |
|---|---|
| Submitting machine | DB; Git when using `--with git` |
| Worker | DB; Git and/or S3 for tasks using them |
| `s3 fetch` machine | S3; no DB credentials or task record needed |

Git and S3 connection templates and task settings travel with the queued task; secret values do not.
Workers resolve the referenced variables from their own environment. Changing config later does not change queued tasks.
The worker needs access to the same Git repository; its credentials may differ from the submitter's.
Task commands receive `NEXTASK_TASK_ID` and the worker's resolved `NEXTASK_DB_URL`, and inherit the worker's environment.

Literal URL passwords are rejected in config, including DB `password` and `sslpassword` query values.
S3 access keys must also be references. Every loaded file is checked, even if a later value overrides it.
Missing or blank referenced variables fail when that connection is used, for example:
`environment variable DB_PASSWORD is required`. Unselected integrations do not require their secrets.

## Database

`[db]` has one setting, `url`, with no default. Use `postgres://` or `postgresql://`.
Connection options go in the URL, for example `?sslmode=verify-full&sslrootcert=/path/to/ca.pem`.
A complete value from the environment may also be a PostgreSQL keyword connection string.
No password is needed in the URL if your database uses another supported authentication method.

Create the PostgreSQL database first, then run `nextask init db` to create or migrate Nextask's tables.
It preserves existing task data and can be run again. The connecting user needs permission to make those schema changes.

## Git

`[integrations.git]` has one setting, `remote`, required when using `--with git`. There is no default remote.

| Form | Example | Access |
|---|---|---|
| HTTPS with token | `https://nextask:${GIT_TOKEN}@git.example/team/source.git` | Token on submitter and worker |
| HTTPS without credentials in URL | `https://git.example/team/source.git` | Public access or Git's configured credential helper |
| SSH | `git@git.example:team/source.git` or `ssh://git@git.example:2222/team/source.git` | SSH key or agent and known host on each machine |
| Remote name | `origin` | Resolved from the submitting repository, including its push URL |
| Local repository | `~/snapshots.git` or `/srv/snapshots.git` | Saved absolute path must also be accessible to the worker |

For Gitea over HTTPS, use your username and an access token as the password reference.
The submitter needs push access; the worker needs read access. Git runs without interactive password prompts.
Nextask uses Git's credential helper when configured; that is a program Git asks for saved credentials.

Nextask pushes a snapshot to `<project>/<TASK_ID>`, where `project` is the local repository folder name.
The worker checks out the recorded commit. Your local files, staging area and branches stay unchanged.
See [Git usage](integrations.md#what-git-saves) for repository requirements.
The older `[source] remote` setting is still accepted; `[integrations.git] remote` wins within the same file.

## S3 settings

Set these under `[integrations.s3]` or override them with `enqueue --set s3.KEY=VALUE`.
Uploads require `--with s3` and at least one `include` or `final_include` pattern.
Use TOML arrays for patterns, e.g. `include = ["outputs/**"]`. Quote string values and durations; use bare booleans and integers.

| Setting | Default | Meaning |
|---|---|---|
| `endpoint` | Required | Service URL with key variables; no extra path or query |
| `remote` | Required | `s3://bucket/path` |
| `region` | Automatic | Ask the service; set explicitly if the provider requires it |
| `root` | `.` | Relative directory to read inside the task directory; no `..` |
| `include` | None | Files uploaded regularly and at the end |
| `exclude` | None | Files never uploaded |
| `final_include` | None | Extra files uploaded at the end |
| `interval` | `60s` | Time between checks; `0s` means final upload only |
| `final_sync` | `true` | Upload when the command finishes, even if it fails |
| `final_timeout` | `2m` | Time allowed for the final upload |
| `concurrency` | `4` | Files uploaded at once; 1–256 |
| `upload_timeout` | `5m` | Time allowed per file, including retries |
| `retries` | `3` | Extra attempts after temporary errors; 0–100 |
| `min_age` | `0s` | Wait this long after a file changes; ignored for final upload |
| `max_file_size` | `"unlimited"` | Skip larger files, e.g. `2GiB` |
| `on_final_error` | `fail` | Fail a successful task if the final upload fails; `warn` only reports it |
| `symlinks` | `skip` | Skip symbolic links; `error` rejects them |

Durations accept values such as `30s`, `2m` and `1h`, up to `24h`.
Only `interval` and `min_age` allow zero. Nextask rejects settings that disable all uploads.

Create the bucket first. `endpoint` is the HTTP(S) service address with credential references;
`remote` is the separate bucket and optional prefix, with no credentials. Neither has a default.
Git and S3 endpoints cannot contain query strings or fragments; S3 endpoints cannot contain an extra path.
Workers need permission to inspect and upload objects; `s3 fetch` needs permission to list and read them.

Patterns are relative to `root`: `outputs/**` includes its contents, while `**/*.json` matches JSON files at any depth.
`exclude` always wins over `include` and `final_include`, regardless of option order.
`.git` and `.nextask` are always skipped. Files are stored at `<remote>/<TASK_ID>/<path-relative-to-root>`.
Only selected files are saved; changed files replace the stored copy and nothing is deleted automatically.

`s3 fetch` uses `endpoint`, `region`, `remote` and `retries` from config.
Upload filters and timeouts do not apply to downloads; use the fetch command's own flags.
See [S3 storage](s3.md) for examples, final uploads and failure behavior.

## Workers and database retries

| Section | Setting | Default | Meaning |
|---|---|---|---|
| `[worker]` | `workdir` | `"/tmp/nextask"` | Base directory for task files and saved results |
| `[worker]` | `heartbeat_interval` | `"1m"` | How often workers report that they are alive |
| `[worker]` | `stale_threshold` | `3` | Missed heartbeat intervals before workers and running tasks appear stale |
| `[worker]` | `log_flush_lines` | `100` | Send a log batch to the DB when it reaches this many entries |
| `[worker]` | `log_flush_interval` | `"500ms"` | How often to send a smaller pending batch |
| `[worker]` | `log_buffer_size` | `10000` | Queued log entries in memory; a full queue can slow task output |
| `[retry]` | `initial_interval` | `"500ms"` | Starting delay for worker DB retries and CLI reconnections |
| `[retry]` | `max_interval` | `"30s"` | Limit on the growing base delay; actual waits vary randomly |

Write durations as quoted strings and counts as integers. Zero selects the default for these numeric worker and retry settings;
negative values are rejected. `retry.max_interval` must be at least `retry.initial_interval`.
These retry settings do not rerun failed tasks or control S3 retries.
Use the same heartbeat settings on workers and viewing machines so they agree about when a worker is stale (3 minutes by default).
Each worker runs one task at a time; start more workers for more parallel tasks. Worker lifetime and cleanup are [CLI options](cli.md#workers).

- Each task gets a new `<workdir>/<TASK_ID>` directory. An existing directory causes an error.
- Local logs are saved in `.nextask/log/out.txt` and `.nextask/log/err.txt` inside that task directory.
- `worker --rm` removes the task directory after uploads and log saving finish, including local logs.
- A worker saves finished results locally if it cannot update the DB. Restarting with the same workdir restores those results without rerunning commands. This does not replay missing logs or uploads.
- Use a persistent workdir if results must survive a reboot; `/tmp/nextask` may be cleared. Use a different workdir for each database.
- Interrupted tasks are not automatically restarted. Killing only the worker process can leave its command running.

## Check your settings

```sh
nextask config show --sources
```

Shows effective settings and their origins, with URL credentials hidden. It also validates the configuration;
referenced DB variables must be set, but this command does not test service access.
Missing required Git or S3 settings are reported when that integration is used.
Errors in config files name the file and setting, or the line for invalid TOML.
