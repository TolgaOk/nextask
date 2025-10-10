# API Reference

## TaskQueue

Redis-based task queue with hierarchical path organization for managing distributed ML experiment runs.

### Constructor

```python
TaskQueue(host: str = "localhost", port: int = 6379, db: int = 0, password: Optional[str] = None)
```

Initialize connection to Redis server.

**Parameters:**
- `host`: Redis server hostname
- `port`: Redis server port
- `db`: Redis database number
- `password`: Optional Redis password for authentication

---

### Run Management

#### create_run

```python
create_run(path: str, data: Optional[dict[str, Any]] = None, status: Union[RunStatus, str] = RunStatus.PENDING) -> Run
```

Create a new run at the specified path.

**Parameters:**
- `path`: Hierarchical path for the run (e.g., `/runs/ppo/2025-05-18/exp-001`)
- `data`: Optional dictionary of run parameters and data (must be JSON serializable)
- `status`: Initial status, defaults to `RunStatus.PENDING`

**Returns:** Created Run object with path, status, data, and timestamps.

**Raises:** `TypeError` if data is not JSON serializable.

---

#### get_run

```python
get_run(path: str) -> Optional[Run]
```

Get a single run by exact path.

**Parameters:**
- `path`: Exact run path

**Returns:** Run object or `None` if not found.

---

#### get_runs

```python
get_runs(prefix: str = "/") -> list[Run]
```

Get all runs matching the path prefix, sorted by creation time.

**Parameters:**
- `prefix`: Path prefix to filter runs, defaults to `/` for all runs

**Returns:** List of Run objects matching the prefix.

---

#### Callable Iterator Interface

```python
queue(prefix: str = "/", *, wait: bool = True, wait_interval: float = 5.0) -> TaskIterator
```

**Pythonic API** - Make the TaskQueue instance callable to return an iterator. Each iteration **atomically claims** and yields a run with status set to RUNNING. Prioritizes pending runs first, then failed runs, ordered by timestamp.

This operation is **atomic and distributed-safe**. Multiple workers can safely iterate concurrently. The iterator only yields runs - use the `TaskQueue` instance methods to update status and data.

**Parameters:**
- `prefix`: Path prefix to filter runs, defaults to `/` for all runs
- `wait`: If True, wait for new runs when queue is empty (default: True)
- `wait_interval`: Seconds to wait between checks when queue is empty (default: 5.0)

**Returns:** TaskIterator that yields Run objects with status set to RUNNING.

**Raises:** `RuntimeError` if Lua script execution fails, `StopIteration` if no runs available and `wait=False`.

**Example:**
```python
# Infinite worker loop
queue = TaskQueue()
for run in queue(prefix="/runs/ppo"):
    process(run)
    queue.set_status(run.path, RunStatus.COMPLETED)  # Call on queue, not iterator

# One-time batch processing (no waiting)
for run in queue(wait=False):
    process(run)
    queue.set_data(run.path, {"result": 42})         # Call on queue, not iterator
```

---

### Status Management

#### set_status

```python
set_status(path: str, status: Union[RunStatus, str]) -> None
```

Update the status of a run and its updated timestamp.

**Parameters:**
- `path`: Run path
- `status`: New status (RunStatus enum or string: `pending`, `running`, `completed`, or `failed`)

**Raises:** `ValueError` if run not found.

**Note:** Call this method on the `TaskQueue` instance, not on the iterator.

---

#### get_status

```python
get_status(path: str) -> Optional[str]
```

Get the status of a run.

**Parameters:**
- `path`: Run path

**Returns:** Status string or `None` if run not found.

---

### Data Management

#### set_data

```python
set_data(path: str, data: dict[str, Any]) -> None
```

Update run data by merging with existing data and updating the timestamp.

**Parameters:**
- `path`: Run path
- `data`: Dictionary of data to merge with existing run data (must be JSON serializable)

**Raises:** `ValueError` if run not found, `TypeError` if data is not JSON serializable.

**Note:** Call this method on the `TaskQueue` instance, not on the iterator. The method is **not atomic** when called concurrently on the same run (see Non-Atomic Operations below).

---

#### get_data

```python
get_data(path: str) -> Optional[dict[str, Any]]
```

Get run data dictionary.

**Parameters:**
- `path`: Run path

**Returns:** Data dictionary or `None` if run not found.

---

## Run Dataclass

All methods that return run information use the `Run` dataclass:

```python
@dataclass
class Run:
    path: str              # Hierarchical path of the run
    status: RunStatus      # Current execution status (enum)
    data: dict[str, Any]   # Run parameters and results (JSON serializable)
    created_at: float      # Unix timestamp of creation
    updated_at: float      # Unix timestamp of last update
```

### Run Properties

- `is_pending: bool` - Check if run is pending
- `is_running: bool` - Check if run is currently running
- `is_completed: bool` - Check if run completed successfully
- `is_failed: bool` - Check if run failed
- `is_finished: bool` - Check if run is finished (completed successfully)
- `is_unfinished: bool` - Check if run is unfinished (pending or failed, needs processing)
- `duration: float` - Duration between creation and last update in seconds
- `age: float` - Age of the run from creation to now in seconds

---

## RunStatus Enum

Status values are defined as a string enum:

```python
class RunStatus(str, Enum):
    PENDING = "pending"      # Run is ready to be executed
    RUNNING = "running"      # Run is currently being executed
    COMPLETED = "completed"  # Run finished successfully
    FAILED = "failed"        # Run execution failed
```

Methods accept both `RunStatus` enum values or strings for convenience.

---

## Atomicity and Distributed Safety

### Atomic Operations

The queue iterator uses Redis Lua scripts to ensure atomicity in distributed environments:

- **No Race Conditions**: Multiple workers can safely call this method concurrently
- **Exactly-Once Execution**: Each run is claimed by only one worker
- **Automatic Status Update**: Run status is set to RUNNING atomically during claim
- **Consistency Guarantees**: Handles edge cases like stale indexes and data inconsistencies

### Implementation Details

- Lua scripts are loaded from `nextask/lua/` directory
- Scripts are registered with Redis on `TaskQueue` initialization  
- **Entire script executes atomically** - Redis blocks other operations until complete
- **Memory bounded** - checks up to 1000 oldest runs (~50KB memory)
- **Early exit** optimization - stops immediately when match found
- Edge cases (missing runs, status mismatches) are handled within the script

**Why Lua?** Redis Lua scripts provide true atomicity - the entire script runs as one indivisible operation. This is superior to transactions (WATCH/MULTI/EXEC) which can fail and require retry logic.

### Best Practices for Distributed Setups

1. **Use the callable iterator** - `for run in queue():` atomically claims runs, no manual locking needed
2. **Call methods on TaskQueue instance** - Use `queue.set_status()`, not iterator methods
3. **Handle failures** - Set status to `FAILED` if processing fails, these runs can be retried
4. **Monitor stuck runs** - Implement timeouts for runs in RUNNING state too long
5. **Use path prefixes** - Partition work by path to reduce contention

**Example Worker Pattern:**
```python
queue = TaskQueue()
for run in queue(prefix="/experiments"):
    try:
        result = process(run)
        queue.set_data(run.path, result)
        queue.set_status(run.path, RunStatus.COMPLETED)
    except Exception as e:
        queue.set_data(run.path, {"error": str(e)})
        queue.set_status(run.path, RunStatus.FAILED)
```

### Non-Atomic Operations

**Note:** `set_data()` and `set_status()` are **not atomic** when called concurrently on the same run. They use a read-modify-write pattern that can result in lost updates under high contention. For status updates, last write wins. For data updates, concurrent modifications to different keys may be lost due to the merge operation.

Only the **iterator claiming operation** is atomic (powered by Lua scripts).
