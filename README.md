# `nextask`

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.1.1](https://img.shields.io/badge/v0.1.1-green)](https://github.com/TolgaOk/nextask) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Manage your runs from your local machine. `nextask` is a **distributed** task queue with live log streaming and Git-based source snapshotting for full **reproducibility**.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/install | bash
```

## Usage

Enqueue tasks, start workers to process them, monitor their output and status, and organize them with tags.

The example below shows the local CLI in the left pane and a worker running on a remote machine in the right pane.

<img src="doc/demo.gif" alt="nextask demo" width="100%">

See `nextask <command> --help` for all options and `nextask --help` for all commands.

## Agent-ready

`nextask` is agent-ready by design. Install the [skills](skills/) to let agents set up services, deploy workers, and manage tasks:

```sh
npx skills add https://github.com/TolgaOk/nextask/skills
```

**Parallel monitoring.** Monitor logs, check task statuses, and track results without interrupting the agent's workflow.

**Resource management.** Tasks are serialized through a queue, so agents can enqueue many tasks without overloading workers.

## How it works

`nextask` has three main components: the **CLI**, the persistent **database and Git remote**, and the potentially distributed **workers**.

<img src="doc/nextask_architecture.svg" alt="nextask architecture" width="80%">

## Configuration

Config files:

```
~/.config/nextask/global.toml            # global defaults
.nextask.toml                            # per-project
```

> **Priority:** CLI flags > environment variables > `.nextask.toml` > `global.toml`.

Example config:

```toml
[db]
url = "postgres://user@localhost:5432/nextask"   # or NEXTASK_DB_URL

[source]
remote = "~/.nextask/source.git"                 # bare repository used as the Git remote

[worker]
workdir = "/tmp/nextask"                         # or NEXTASK_WORKER_WORKDIR
heartbeat_interval = "1m"                        # worker heartbeat frequency
stale_threshold = 3                              # missed heartbeats before marking a worker stale
log_flush_lines = 100                            # flush after this many lines
log_flush_interval = "500ms"                     # maximum time between flushes
log_buffer_size = 10000                          # log-line channel capacity
```
