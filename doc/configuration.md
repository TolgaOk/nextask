# Configuration

Below is an example config file that contains all settings.
Config files are located for project settings in `.nextask.toml` and for global in `~/.config/nextask/global.toml`.

```toml
[db]
url = "postgres://nextask:${DB_PASSWORD}@db.example:5432/nextask"

[integrations.git]
remote = "https://nextask:${GIT_TOKEN}@git.example/nextask/source.git"

[integrations.s3]
endpoint = "https://${STORAGE_ACCESS}:${STORAGE_SECRET}@fsn1.your-objectstorage.com"
region = "fsn1"
remote = "s3://my-bucket/my-project"
root = "."
include = []                 # Choose per task, e.g. ["outputs/**"]
exclude = []                 # Exclusions always win
final_include = []           # Extra files saved only at the end
interval = "60s"             # 0s: final upload only
final_sync = true
final_timeout = "2m"
concurrency = 4
upload_timeout = "5m"
retries = 3
min_age = "0s"
max_file_size = "unlimited"
on_final_error = "fail"      # fail or warn
symlinks = "skip"            # skip or error

[worker]
workdir = "~/nextask-work"
heartbeat_interval = "1m"
stale_threshold = 3          # Missed heartbeat intervals
log_flush_lines = 100
log_flush_interval = "500ms"
log_buffer_size = 10000

[retry]
initial_interval = "500ms"   # Database retries
max_interval = "30s"
```

Change the addresses and bucket for your services.
The storage example uses Hetzner but you can use any S3-compatible storage.
Create the bucket first.
Choose files to upload when [enqueueing](integrations.md).

## Passwords and keys

We highly suggest that you keep secret values in environment variables or in a secrets manager.
The example config contains their names, e.g., `${DB_PASSWORD}`, and build the full url.

You can also supply complete connection URLs through environment variables on your machine and remote workers.
If you keep them in a `.env` file, load it into the environment before starting Nextask:

| Environment variable | Replaces |
|---|---|
| `NEXTASK_DB_URL` | `db.url` |
| `NEXTASK_GIT_URL` | `integrations.git.remote` |
| `NEXTASK_S3_ENDPOINT` | `integrations.s3.endpoint` |


## Config hierarchy

Command flags override environment variables, then project files, then user files, then defaults.

```sh
nextask config show --sources
```

This shows settings and where they came from, with secrets hidden.
Git and S3 still require `--with` on each task.
