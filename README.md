# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Keep developing on your laptop while your other machines handle training, tests, and data processing. Nextask queues your commands and lets you follow their progress from one terminal.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and persistent S3 artifact storage" width="100%">

## Install

Install Nextask on your machine and wherever you want to run workers:

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Usage

When your training script is ready, send it to a worker with a copy of your code:

```sh
nextask enqueue 'python train.py' --with git --with s3
```

Keep working locally while the worker runs that saved version. S3 provides persistent storage for task artifacts: saved results remain available after the task ends or the worker is gone, ready to revisit or reuse in later tasks.

Your config supplies the Git remote, artifact storage, and what to keep, so you can reuse those choices across tasks. Passwords and keys stay in environment variables.

## Read more

- [Connect your database, Git, and storage](doc/configuration.md)
- [Choose Git and S3 for each task](doc/integrations.md)
- [Follow progress and wait for results](doc/watching.md)

Use `nextask --help` for all commands.
