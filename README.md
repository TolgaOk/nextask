# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Run tests and data processing on your other machines while you keep working. Follow progress from your terminal, save the code used for each task, and collect the results.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and S3 storage" width="100%">

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Usage

Run your Python tests on a remote worker, watch progress live, and save the report to S3:

```sh
nextask enqueue --with git --with s3 --attach \
  --set 's3.include=["results.xml"]' \
  'pytest --junitxml=results.xml'
```

Use your own Git and S3 services, with connection settings in config and passwords and keys in environment variables.

## Read more

- [Configure Nextask](doc/configuration.md)
- [Submit tasks with Git and S3](doc/integrations.md)
- [Upload and download files](doc/s3.md)
- [Read logs and wait for tasks](doc/watching.md)

Use `nextask --help` for all commands.
