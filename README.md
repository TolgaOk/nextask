# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Submit commands from your machine, let workers run them, and watch their output live. Nextask stores tasks and logs in PostgreSQL, with optional Git snapshots and S3-compatible file storage.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and S3 storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Usage

List your project's files and save the list to S3:

```sh
nextask enqueue --with git --with s3 --attach \
  --set 's3.include=["files.txt"]' \
  'git ls-files | tee files.txt'
```

Git leaves your local files unchanged. `--attach` shows the output.

Connection settings go in config; passwords and keys come from environment variables.

## Read more

- [Configure Nextask](doc/configuration.md)
- [Submit tasks with Git and S3](doc/integrations.md)
- [Upload and download files](doc/s3.md)
- [Read logs and wait for tasks](doc/watching.md)

Use `nextask --help` for all commands.
