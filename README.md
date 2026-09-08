# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

A distributed task queue with live logs, optional Git snapshots, and S3-compatible artifact storage.

<img src="doc/nextask-diagram.svg" alt="Nextask CLI, PostgreSQL queue, and workers, with optional Git snapshots and S3 artifact storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Usage

Set `NEXTASK_DB_URL` to the same PostgreSQL database on the submitter and workers.

```sh
nextask init db                      # initialize once
nextask worker                       # leave running in a worker terminal
nextask enqueue 'hostname' --attach   # run from another terminal
```

See `nextask --help` for commands and options.

## Read more

- [Configuration](doc/configuration.md)
- [Git snapshots](doc/integrations.md)
- [S3 artifacts](doc/s3.md)
- [Logs and waiting](doc/watching.md)
- [Upgrading from 0.1](doc/upgrading.md)
- [Agent skills](skills/)
