# nextask

Redis-based task distribution for ML experiments.

## Installation

```bash
uv pip install -e .
```

## Quick Start

```python
from nextask import TaskQueue, RunStatus

# Connect to Redis
queue = TaskQueue(host="localhost", port=6379)

# Create runs
queue.create_run("/runs/ppo/2025-05-18/exp-001", data={"lr": 0.001})
queue.create_run("/runs/ppo/2025-05-18/exp-002", data={"lr": 0.01})

# Pythonic iteration - queue is callable and returns iterator
for run in queue(prefix="/runs/ppo"):
    # Each run is atomically claimed with status=RUNNING
    # ... do work ...
    queue.set_data(run.path, {"reward": 42.0})
    queue.set_status(run.path, RunStatus.COMPLETED)

# Read runs by path prefix
runs = queue.get_runs("/runs/ppo/2025-05-18")
```

## API

### Connection
- `TaskQueue(host, port, db=0, password=None)` - Connect to Redis

### Run Management
- `create_run(path, data=None, status="pending")` - Create a new run, returns `Run`
- `get_run(path)` - Get single run by exact path, returns `Run` or `None`
- `get_runs(prefix="/")` - Get all runs matching prefix, returns `list[Run]`
- `get_next_unfinished(prefix="/")` - **Atomically** claim next pending/failed run, returns `Run` or `None`
- `queue(prefix="/", wait=True, wait_interval=5.0)` - **Pythonic iterator** - returns iterator over runs

### Status & Data
- `set_status(path, status)` - Update run status (pending/running/completed/failed)
- `get_status(path)` - Get run status
- `set_data(path, data)` - Update run data (merges with existing)
- `get_data(path)` - Get run data

## Run Structure

All methods return `Run` dataclass instances:

```python
run.path          # str: /runs/ppo/2025-05-18/exp-001
run.status        # RunStatus: RunStatus.PENDING
run.data          # dict: {"lr": 0.001, "reward": 42.0}
run.created_at    # float: 1234567890.123
run.updated_at    # float: 1234567890.456

# Properties
run.is_completed  # bool
run.is_finished   # bool
run.duration      # float (seconds)
run.age           # float (seconds)
```

## Statuses

Use `RunStatus` enum or strings:
- `RunStatus.PENDING` or `"pending"` - Ready to run
- `RunStatus.RUNNING` or `"running"` - Currently executing
- `RunStatus.COMPLETED` or `"completed"` - Successfully finished
- `RunStatus.FAILED` or `"failed"` - Execution failed

## Pythonic Iterator API

The queue is **callable** and returns an iterator:

```python
# Infinite worker loop - waits for new runs
for run in queue(prefix="/runs/ppo"):
    process(run)

# One-time batch processing - stops when done  
for run in queue(wait=False):
    process(run)
```

All iterations use atomic Lua scripts to prevent race conditions in distributed environments.

## CLI Tool

Nextask includes a command-line interface for managing tasks and Redis instances:

```bash
# Task management
nextask add /experiments/run1 --data '{"lr": 0.001}'
nextask list --prefix /experiments --status pending
nextask show /experiments/run1
nextask update /experiments/run1 --status completed
nextask stats --prefix /experiments
nextask clear --status failed

# Redis management
nextask redis status
nextask redis start --port 6380 --name dev --daemonize
nextask redis list
nextask redis stop --name dev
```

### Available Commands

**Task Management:**
- `add` - Create new tasks with JSON data
- `list` - View all tasks with filters (prefix, status, limit, json output)
- `show` - Display detailed task information
- `update` - Modify task status or data
- `stats` - View queue statistics
- `clear` - Remove tasks (with confirmation)

**Redis Management:**
- `redis status` - Check Redis connection and server info
- `redis start` - Launch a local Redis server
- `redis stop` - Stop managed Redis instances
- `redis list` - View all nextask-managed Redis servers

Global options: `--host`, `--port`, `--db` to connect to different Redis instances.

## Examples

- `example.py` - Basic usage demonstration
- `worker_example.py` - Distributed worker implementation
- `config_example.py` - Loading runs from JSON configuration

