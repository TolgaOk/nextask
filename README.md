# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Submit commands from your machine, let workers run them, and watch their output live. Nextask stores tasks and logs in PostgreSQL, with optional Git snapshots and S3-compatible file storage.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and S3 storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Usage

Run a task with a copy of your code and upload its output files:

```sh
nextask enqueue --with git --with s3 --attach \
  --set 's3.include=["outputs/**"]' './job.sh'
```

Git saves your current project without changing your local repository. The worker runs that saved code and uploads files from `outputs/` every 60 seconds and when the command finishes. `--attach` shows live logs and waits for the result. Git and S3 are optional; leave out the corresponding `--with` flag when you do not need them.

List tasks or download a task's output files using the ID printed by enqueue:

```sh
nextask list --limit 10
nextask s3 fetch TASK_ID --to downloads
```

Uploaded files remain available even after you remove the task from the database.

Settings live in `.nextask.toml` for a project or `~/.config/nextask/global.toml` for user defaults. Passwords and keys come from environment variables.

## Read more

- [Configure Nextask](doc/configuration.md)
- [Submit tasks with Git and S3](doc/integrations.md)
- [Upload and download files](doc/s3.md)
- [Read logs and wait for tasks](doc/watching.md)

Use `nextask --help` for all commands.
