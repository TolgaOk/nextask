# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

A distributed task queue with live logs, optional Git snapshots, and S3-compatible artifact storage.

<img src="doc/nextask-diagram.svg" alt="Nextask CLI, PostgreSQL queue, and workers, with optional Git snapshots and S3 artifact storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Enqueue with Git and S3

With the remotes configured, snapshot your project and sync selected outputs:

```sh
nextask enqueue --with git --with s3 --attach \
  --set 's3.include=["outputs/**"]' './job.sh'
```

## Other commands

```sh
nextask init db                       # initialize once
nextask worker                        # leave running in a worker terminal
nextask list --limit 10                # from another terminal
nextask s3 fetch TASK_ID --to artifacts
```

## Configuration

Set `NEXTASK_DB_URL` on the submitter and workers. Configure Git/S3 in `.nextask.toml` (project) or `~/.config/nextask/global.toml` (global). Reference secrets with `${VAR}`; keep their values in the environment.

## Read more

- [Configuration](doc/configuration.md)
- [Git snapshots](doc/integrations.md)
- [S3 artifacts](doc/s3.md)
- [Logs and waiting](doc/watching.md)

Use `nextask --help` for all commands.
