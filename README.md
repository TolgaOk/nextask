# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Queue commands for your machines to run and watch their output live. Optionally save code with Git and output files in S3-compatible storage.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and S3 storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Enqueue with Git and S3

Save your code with Git, run `job.sh` on a worker, and upload files from `outputs/` to S3:

```sh
nextask enqueue --with git --with s3 --attach \
  --set 's3.include=["outputs/**"]' './job.sh'
```

## Other commands

```sh
nextask init db                        # set up the database once
nextask worker                        # leave running in a worker terminal
nextask list --limit 10                # from another terminal
nextask s3 fetch TASK_ID --to downloads
```

## Configuration

Set `NEXTASK_DB_URL` on your machine and each worker. Put Git and S3 settings in `.nextask.toml` for the project, or `~/.config/nextask/global.toml` for all projects. Keep passwords and keys in environment variables, and use `${VARIABLE_NAME}` in the config.

## Read more

- [Configure Nextask](doc/configuration.md)
- [Submit tasks with Git and S3](doc/integrations.md)
- [Upload and download files](doc/s3.md)
- [Read logs and wait for tasks](doc/watching.md)

Use `nextask --help` for all commands.
