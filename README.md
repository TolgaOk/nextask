# nextask

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.2.0](https://img.shields.io/badge/v0.2.0-blue)](https://github.com/TolgaOk/nextask/releases/tag/v0.2.0) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Run a task on a remote machine, as if it were running in your **local** terminal.

<img src="doc/nextask-diagram.svg" alt="Nextask connects your machine to a PostgreSQL task queue, workers, Git, and persistent S3 artifact storage" width="100%">

Under the hood, `nextask` delegates the task to an available worker, streaming the logs back to your terminal, snapshotting the code at the time of submission, and storing the task artifacts produced in the task.

## Install


```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash
```

## Quick start

Once a task is enqueued, an available worker picks it up.
If provided `--attach` flag, `nextask` will hold the connection to the DB, streaming the logs to your terminal.

```sh
nextask enqueue 'python train.py' --attach
```

Provided the `with git` flag, `nextask` will take a snapshot of the repository (including uncommitted changes) the task call is made from and push it to the `<project>/<TASK_ID>` branch in the remote Git repository (see [config](doc/configuration.md) to set).
If also provided the `with s3` flag, `nextask` will store the artifacts produced in the task at `<remote>/<task_id>/` within the S3 bucket (configured in your [config](doc/configuration.md)).

```sh
nextask enqueue 'python train.py' --with git --with s3
```

You can access the logs of a task by providing the task ID or live watch the task logs by providing the `--attach` flag.

```sh
nextask log TASK_ID --attach
```

Each `log` command is a viewer that request the logs from the DB.
Hence, you can read past logs and follow new output independently.

`nextask` is build **agentic** workflow in mind.
Agents can `wait`, `log`, and `enqueue` tasks, all managed by the DB.

## Read more

- [configuration](doc/configuration.md)
- [Git and S3](doc/integrations.md)
- [CLI reference](doc/cli.md)

Use `nextask --help` for all commands.
