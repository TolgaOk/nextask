# nextask

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev) [![v0.1.0](https://img.shields.io/badge/v0.1.0-green)](https://github.com/TolgaOk/nextask) [![macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](https://github.com/TolgaOk/nextask)

Distributed task queue with source snapshotting for full reproducibility.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash
```

Or with Go:

```sh
go install github.com/TolgaOk/nextask/cmd/nextask@latest
```

## Usage

```sh
nextask init db                                                    # setup database tables

nextask enqueue "python train.py --lr 0.001" --snapshot            # enqueue with source snapshot
nextask enqueue "bash run.sh" --tag gpu=a100,sweep=exp3            # enqueue with tags
nextask enqueue "python eval.py" --snapshot --attach               # enqueue and watch live output

nextask worker                                                     # start picking up tasks
nextask worker --filter gpu=a100 --daemon                          # background worker with tag filter
nextask worker --once --timeout 2h                                 # single task, max 2 hours

nextask list --status running --tag sweep=exp3                     # filter tasks
nextask log <id> --attach                                          # stream live output
nextask log <id> --tail 50                                         # last 50 lines
nextask show <id>                                                  # task details
nextask cancel <id>                                                # cancel pending or running
```

## How it works

Simplified architecture:

```
 ┌──────────────┐    logs     ┌────────────┐    logs     ┌──────────────┐
 │              │◀────────────│ PostgreSQL │◀────────────│    worker    │
 │    nextask   │             │   queue    │             │   (remote)   │
 │      CLI     │   enqueue   │            │   claim     │              │
 │              │──────────┬─▶│  ○ tasks   │──┬─────────▶│   execute    │
 └──────────────┘          │  │  ○ logs    │  │          └──────────────┘
                           │  │  ○ workers │  │
                           │  └────────────┘  │
                --snapshot │                  │ clone
                           │  ┌────────────┐  │
                           └─▶│ git remote │──┘
                              └────────────┘
```

> **Snapshots** capture the full working tree, including uncommitted changes, and push to configured remote, without modifying the local repository. Workers then clone from the remote, so the exact source is preserved and reproducible.

> **Workers** claim tasks from the queue via atomic operations if they match the filters. Workers implement a heartbeat system to detect stale workers.

>**Outputs** (logs) are written per-line to PostgreSQL with stream separation (stdout/stderr).

## Configuration


Config files:

```
~/.config/nextask/global.toml            # global defaults
.nextask.toml                            # per-project (higher priority)
```


```toml
[db]
url = "postgres://user@localhost:5432/nextask"   # or NEXTASK_DB_URL

[source]
remote = "~/.nextask/source.git"                 # or NEXTASK_SOURCE_REMOTE
                                                 # supports: local path, git URL, remote name

[worker]
workdir = "/tmp/nextask"                         # or NEXTASK_WORKER_WORKDIR
heartbeat_interval = "1m"                        # how often workers ping
stale_threshold = 3                              # missed heartbeats before stale
```
