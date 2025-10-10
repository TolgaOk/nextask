# nextask

Simple Redis-based task queue for distributed tasks running targeting the ML experiment.

> ⚠️ **Warning**: This is a vibe coding project. Use in production at your own risk.

## Installation

```bash
uv pip install -e .
```

## Usage

`nextask` provide two APIs: CLI and Python. Use CLI to have an overview of the tasks and Python API to fetch the next task (hence, the name).

### Python API

Worker loop that processes tasks from the queue. The queue yields the oldest `PENDING` task from REDIS storage.

```python
from nextask import TaskQueue, RecordStatus


queue = TaskQueue(host="localhost", port=6379)

for record in queue(prefix="/experiments"):
    result = train_model(record.data)
    
    queue.update_data(record.path, {"reward": result})
    queue.set_status(record.path, RecordStatus.COMPLETED)
```

### CLI

Create a new task:
```bash
$ nextask add /experiments/run1 --data '{"lr": 0.001}'
╭────────────────────────────── ✓ Record Created ──────────────────────────────╮
│ Path: /experiments/run1                                                      │
│ Status: pending                                                              │
│ Data: {                                                                      │
│   "lr": 0.001                                                                │
│ }                                                                            │
╰──────────────────────────────────────────────────────────────────────────────╯
```

List tasks by prefix:
```bash
$ nextask list --prefix /experiments
                               Records (found 2)                                
╭──────────────┬────────────────────┬─────────────────────┬──────────────────╮
│ Status       │ Path               │ Created             │ Data             │
├──────────────┼────────────────────┼─────────────────────┼──────────────────┤
│ pending      │ /experiments/run1  │ 2025-10-10 15:43:05 │ {"lr": 0.001}    │
│ pending      │ /experiments/run2  │ 2025-10-10 15:43:44 │ {"lr": 0.01}     │
╰──────────────┴────────────────────┴─────────────────────┴──────────────────╯
```

View queue statistics:
```bash
$ nextask stats
Queue Statistics (prefix: /)            
╭───────────────┬───────╮
│ Metric        │ Value │
├───────────────┼───────┤
│ Total records │     2 │
│ Pending       │     2 │
│ Running       │     0 │
│ Completed     │     0 │
│ Failed        │     0 │
│               │       │
│ Avg duration  │ 0.00s │
╰───────────────┴───────╯
```

See `doc/API.md` and `doc/CLI.md` for complete documentation.
