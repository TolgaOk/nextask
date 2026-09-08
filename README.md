# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0-alpha](https://img.shields.io/badge/v0.2.0--alpha-orange)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0-alpha) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Run work on your machine or another, with the feel of a local command. Queue a task, watch its output live, and keep developing while an available worker handles it. Return to its code, logs, and artifacts whenever you need them.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and persistent S3 artifact storage" width="100%">

## Install

Install Nextask on your machine and wherever you want to run workers:

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash -s -- --version 0.2.0-alpha
```

## Usage

Run a command as if it were local. An available worker picks it up automatically, and its output appears live in your terminal:

```sh
nextask enqueue 'hostname' --attach
```

You can also run everything on one machine. Each worker handles one task at a time, so two workers allow at most two queued tasks to run together. Agents can enqueue more work while the rest waits, keeping the number of simultaneous tasks under your control.

Make experiments easier to reproduce by keeping the exact code used for each task:

```sh
nextask enqueue 'python train.py' --with git --with s3
```

Git saves your current code, including uncommitted changes, without changing your local repository. Keep editing while the worker runs that saved version.

S3 provides persistent artifact storage at `<remote>/<TASK_ID>/`. Saved artifacts remain available after the worker is gone or the task is removed, ready to revisit or reuse in later experiments.

Follow the same task from multiple terminals, or let agents watch alongside you:

```sh
nextask log TASK_ID --attach
```

Each viewer can read past logs and follow new output independently. Stopping a log viewer leaves the task running.

Connections and artifact choices come from config. Passwords and keys stay in environment variables.

## Read more

- [Connect your database, Git, and storage](doc/configuration.md)
- [Choose Git and S3 for each task](doc/integrations.md)
- [Follow progress and wait for results](doc/watching.md)

Use `nextask --help` for all commands.
