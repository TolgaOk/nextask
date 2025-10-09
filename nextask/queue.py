"""Core task queue implementation using Redis."""

import json
import time
from typing import Any, Optional, Union

import redis

from nextask.lua.loader import load_lua_script
from nextask.models import Run, RunStatus, validate_json_serializable


class TaskQueue:
    """Redis-based task queue with hierarchical path organization.

    Manages distributed ML experiment runs with status tracking and
    hierarchical path-based filtering. Uses atomic Lua scripts for
    distributed-safe operations.

    Attributes:
        redis: Redis client connection.
    """

    def __init__(
        self,
        host: str = "localhost",
        port: int = 6379,
        db: int = 0,
        password: Optional[str] = None,
    ):
        """Initialize connection to Redis server and load Lua scripts.

        Args:
            host: Redis server hostname.
            port: Redis server port.
            db: Redis database number.
            password: Optional Redis password.
        """
        self.redis = redis.Redis(
            host=host,
            port=port,
            db=db,
            password=password,
            decode_responses=True,
        )
        self._lua_scripts = {}
        self._load_lua_scripts()

    @property
    def _redis(self):
        """Alias for redis attribute for backward compatibility."""
        return self.redis

    def _load_lua_scripts(self) -> None:
        """Load and register all Lua scripts with Redis.

        Scripts are loaded from the lua/ directory and registered with Redis
        for efficient execution. Registered scripts are cached on the Redis
        server and can be called with minimal overhead.
        """
        try:
            claim_script = load_lua_script("claim_next.lua")
            self._lua_scripts["claim_next"] = self.redis.register_script(claim_script)
        except Exception as e:
            raise RuntimeError(f"Failed to load Lua scripts: {e}") from e

    def _path_to_key(self, path: str) -> str:
        """Convert user-facing path to Redis key.

        Args:
            path: User path like /runs/ppo/exp-001.

        Returns:
            Redis key like run:/runs/ppo/exp-001.
        """
        return f"run:{path}"

    def _key_to_path(self, key: str) -> str:
        """Convert Redis key to user-facing path.

        Args:
            key: Redis key like run:/runs/ppo/exp-001.

        Returns:
            User path like /runs/ppo/exp-001.
        """
        return key.removeprefix("run:")

    def create_run(
        self,
        path: str,
        data: Optional[dict[str, Any]] = None,
        status: Union[RunStatus, str] = RunStatus.PENDING,
    ) -> Run:
        """Create a new run at the specified path.

        Args:
            path: Hierarchical path for the run (e.g., /runs/ppo/2025-05-18/exp-001).
            data: Optional dictionary of run parameters and data (must be JSON serializable).
            status: Initial status (default: RunStatus.PENDING).

        Returns:
            Created Run object with path, status, data, and timestamps.

        Raises:
            TypeError: If data is not JSON serializable.
        """
        data = validate_json_serializable(data) if data is not None else {}
        status_value = status.value if isinstance(status, RunStatus) else status
        key = self._path_to_key(path)
        now = time.time()

        run_data = {
            "path": path,
            "status": status_value,
            "data": json.dumps(data),
            "created_at": str(now),
            "updated_at": str(now),
        }

        pipe = self.redis.pipeline()
        pipe.hset(key, mapping=run_data)
        pipe.sadd("runs:index", path)
        pipe.zadd(f"status:{status_value}", {path: now})
        pipe.execute()

        return self._deserialize_run(run_data)

    def get_run(self, path: str) -> Optional[Run]:
        """Get a single run by exact path.

        Args:
            path: Exact run path.

        Returns:
            Run object or None if not found.
        """
        key = self._path_to_key(path)
        run_data = self.redis.hgetall(key)

        if not run_data:
            return None

        return self._deserialize_run(run_data)

    def get_runs(self, prefix: str = "/") -> list[Run]:
        """Get all runs matching the path prefix.

        Args:
            prefix: Path prefix to filter runs (default: / for all runs).

        Returns:
            List of Run objects matching the prefix.
        """
        all_paths = self.redis.smembers("runs:index")
        matching_paths = [p for p in all_paths if p.startswith(prefix)]

        runs = []
        for path in matching_paths:
            run = self.get_run(path)
            if run:
                runs.append(run)

        return sorted(runs, key=lambda r: r.created_at)

    def set_status(self, path: str, status: Union[RunStatus, str]) -> None:
        """Update the status of a run.

        Args:
            path: Run path.
            status: New status (RunStatus enum or string: pending/running/completed/failed).

        Raises:
            ValueError: If run not found.
        """
        run = self.get_run(path)
        if not run:
            raise ValueError(f"Run not found: {path}")

        status_value = status.value if isinstance(status, RunStatus) else status
        old_status = run.status.value
        key = self._path_to_key(path)
        now = time.time()

        pipe = self.redis.pipeline()
        pipe.hset(key, "status", status_value)
        pipe.hset(key, "updated_at", str(now))
        pipe.zrem(f"status:{old_status}", path)
        pipe.zadd(f"status:{status_value}", {path: now})
        pipe.execute()

    def get_status(self, path: str) -> Optional[str]:
        """Get the status of a run.

        Args:
            path: Run path.

        Returns:
            Status string or None if run not found.
        """
        key = self._path_to_key(path)
        return self.redis.hget(key, "status")

    def set_data(self, path: str, data: dict[str, Any]) -> None:
        """Update run data (merges with existing data).

        Args:
            path: Run path.
            data: Dictionary of data to merge with existing run data (must be JSON serializable).

        Raises:
            ValueError: If run not found.
            TypeError: If data is not JSON serializable.
        """
        data = validate_json_serializable(data)

        run = self.get_run(path)
        if not run:
            raise ValueError(f"Run not found: {path}")

        existing_data = run.data
        merged_data = {**existing_data, **data}

        key = self._path_to_key(path)
        now = time.time()

        pipe = self.redis.pipeline()
        pipe.hset(key, "data", json.dumps(merged_data))
        pipe.hset(key, "updated_at", str(now))
        pipe.execute()

    def get_data(self, path: str) -> Optional[dict[str, Any]]:
        """Get run data.

        Args:
            path: Run path.

        Returns:
            Data dictionary or None if run not found.
        """
        key = self._path_to_key(path)
        data_str = self.redis.hget(key, "data")

        if data_str is None:
            return None

        return json.loads(data_str)

    def _deserialize_run(self, run_data: dict[str, str]) -> Run:
        """Convert Redis hash data to Run object.

        Args:
            run_data: Raw Redis hash data.

        Returns:
            Deserialized Run object with proper types.
        """
        return Run(
            path=run_data["path"],
            status=RunStatus(run_data["status"]),
            data=json.loads(run_data["data"]),
            created_at=float(run_data["created_at"]),
            updated_at=float(run_data["updated_at"]),
        )

    def __call__(
        self, prefix: str = "/", *, wait: bool = True, wait_interval: float = 5.0
    ) -> "TaskIterator":
        """Make TaskQueue callable to return an iterator (Pythonic API).

        Returns an iterator that atomically claims and yields runs one at a time.
        Each yielded run is already marked as RUNNING. Perfect for worker loops.

        Args:
            prefix: Path prefix to filter runs (default: / for all runs).
            wait: If True, wait for new runs when queue is empty (default: True).
            wait_interval: Seconds to wait between checks when queue is empty (default: 5.0).

        Returns:
            TaskIterator that yields Run objects.

        Example:
            # Infinite worker loop
            for run in queue(prefix="/runs/ppo"):
                process(run)
                queue.set_status(run.path, RunStatus.COMPLETED)

            # One-time batch processing (no waiting)
            for run in queue(wait=False):
                process(run)
        """
        return TaskIterator(self, prefix=prefix, wait=wait, wait_interval=wait_interval)


class TaskIterator:
    """Iterator for atomically claiming and processing runs.

    Implements the iterator protocol for TaskQueue, enabling Pythonic
    for-loop iteration over unfinished runs.

    Attributes:
        queue: TaskQueue instance to claim runs from.
        prefix: Path prefix to filter runs.
        wait: Whether to wait for new runs when queue is empty.
        wait_interval: Seconds to wait between checks.
    """

    def __init__(
        self,
        queue: TaskQueue,
        prefix: str = "/",
        wait: bool = True,
        wait_interval: float = 5.0,
    ):
        """Initialize the task iterator.

        Args:
            queue: TaskQueue instance.
            prefix: Path prefix to filter runs.
            wait: If True, wait for new runs when empty.
            wait_interval: Seconds to wait between checks.
        """
        self.queue = queue
        self.prefix = prefix
        self.wait = wait
        self.wait_interval = wait_interval

    def __iter__(self) -> "TaskIterator":
        """Return self as iterator."""
        return self

    def __next__(self) -> Run:
        """Atomically claim and return the next unfinished run.

        This operation is atomic and safe for distributed environments.
        The run is automatically marked as RUNNING when claimed, preventing
        race conditions where multiple workers might pick the same run.

        Prioritizes pending runs first, then failed runs, ordered by timestamp.

        Returns:
            Next available Run object with status set to RUNNING.

        Raises:
            StopIteration: If no runs available and wait=False.
            RuntimeError: If the Lua script execution fails.
        """
        while True:
            now = time.time()

            try:
                claimed_path = self.queue._lua_scripts["claim_next"](
                    keys=[],
                    args=[self.prefix, str(now), RunStatus.RUNNING.value],
                )
            except redis.RedisError as e:
                raise RuntimeError(f"Failed to claim run: {e}") from e

            if claimed_path:
                run = self.queue.get_run(claimed_path)
                if not run:
                    raise RuntimeError(
                        f"Claimed run {claimed_path} not found - data inconsistency detected"
                    )
                return run

            if not self.wait:
                raise StopIteration

            time.sleep(self.wait_interval)
