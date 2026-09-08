# Configuration

Put project settings in `.nextask.toml` and user defaults in `~/.config/nextask/global.toml`.

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

Change the addresses and bucket for your services. The storage example uses Hetzner.
Create the bucket first. Choose files to upload when [enqueueing](integrations.md).

## Passwords and keys

Keep secret values in environment variables. Config contains their names, such as `${DB_PASSWORD}`.
You choose the names. Usernames, hosts and ports can be written directly in config.

- Set DB variables on the machine submitting tasks and on each worker.
- Set Git variables on both, or use SSH keys or Git's saved credentials.
- Set storage variables on workers and on machines downloading files.
- Missing variables produce an error naming the variable.
- Secrets written directly in any config file are rejected, even if another setting overrides that file.

You can also supply complete connection URLs:

| Environment variable | Replaces |
|---|---|
| `NEXTASK_DB_URL` | `db.url` |
| `NEXTASK_GIT_URL` | `integrations.git.remote` |
| `NEXTASK_S3_ENDPOINT` | `integrations.s3.endpoint` |

A custom variable works too: `url = "${MY_DATABASE_URL}"`. Complete URLs must already use valid URL escaping.

## Which setting is used?

Command flags override environment variables, then project files, then user files, then defaults.
Optional shared files are `.tasktools.toml` and `~/.config/tasktools/config.toml`.
In those files, use sections such as `[nextask.db]`. Nextask's own file takes priority within each location.
Project files are read from the current directory.

```sh
nextask config show --sources
```

This shows settings and where they came from, with secrets hidden. Git and S3 still require `--with` on each task.

## Worker files

- Each task gets a new `<workdir>/<TASK_ID>` directory. An existing directory causes an error.
- `worker --rm` removes that directory after uploads and log saving finish, including local logs.
- A worker saves finished results locally if it cannot update the database. Restarting with the same workdir restores those saved results, without rerunning commands. Logs and uploaded files are separate.
- Use a persistent workdir if results must survive a reboot. The default `/tmp/nextask` may be cleared. Use a different workdir for each database.
- Interrupted tasks are not automatically restarted. Killing only the worker process can leave its command running.
