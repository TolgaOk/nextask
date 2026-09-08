# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

A distributed task queue for shell commands. Submit tasks from your machine, run them on workers, and stream logs live. Add Git snapshots and S3-compatible artifact storage when needed.

<img src="doc/nextask-diagram.svg" alt="Nextask CLI, PostgreSQL queue, and workers, with optional Git snapshots and S3 artifact storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

For upgrades from 0.1, follow the [migration guide](doc/upgrading.md).

## Usage

Set `NEXTASK_DB_URL` to your PostgreSQL connection URL on the submitter and workers. Initialize the database once:

```sh
nextask init db
```

Start a worker on any machine connected to that database:

```sh
nextask worker
```

From another terminal, submit a command and follow its output:

```sh
nextask enqueue 'hostname' --attach
nextask list --status running --limit 10
```

## Git and artifacts

After [configuring the remotes and credentials](doc/integrations.md), snapshot your project and upload selected outputs:

```sh
nextask enqueue --with git --with s3 \
  --set 's3.include=["outputs/**"]' './job.sh'
nextask s3 fetch TASK_ID --to ./artifacts
```

Integrations are opt-in per task. Git snapshots leave your local repository unchanged. Workers upload selected files periodically and when commands finish.

## Agent skills

Install the [skills](skills/) to let agents set up services, deploy workers, and manage tasks:

```sh
npx skills add https://github.com/TolgaOk/nextask/skills
```

## Documentation

[Configuration](doc/configuration.md) · [Git integration](doc/integrations.md) · [S3 storage](doc/s3.md) · [Logs and waiting](doc/watching.md)

Use `nextask --help` or `nextask <command> --help` for commands and options.
