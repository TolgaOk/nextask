"""Core task queue implementation using Redis."""

import json
import time
from typing import Any, Optional, Union

import redis

from nextask.lua.loader import load_lua_script
from nextask.models import (
    DEFAULT_PRIMITIVE_TYPES,
    Record,
    RecordStatus,
    validate_json_serializable,
)


class TaskQueue:
    """Redis-based task queue with hierarchical path organization.

    Manages distributed records with status tracking and hierarchical
    path-based filtering. Uses atomic Lua scripts for distributed-safe
    operations.

    Attributes:
        redis: Redis client connection.
        primitive_types: Tuple of types allowed in append_data. Can be customized via subclassing.
    """

    primitive_types = DEFAULT_PRIMITIVE_TYPES

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
            path: User path like /experiments/exp-001.

        Returns:
            Redis key like record:/experiments/exp-001.
        """
        return f"record:{path}"

    def _key_to_path(self, key: str) -> str:
        """Convert Redis key to user-facing path.

        Args:
            key: Redis key like record:/experiments/exp-001.

        Returns:
            User path like /experiments/exp-001.
        """
        return key.removeprefix("record:")

    def create_record(
        self,
        path: str,
        data: Optional[dict[str, Any]] = None,
        status: Union[RecordStatus, str] = RecordStatus.PENDING,
    ) -> Record:
        """Create a new record at the specified path.

        Args:
            path: Hierarchical path for the record (e.g., /experiments/2025-05-18/exp-001).
            data: Optional dictionary of record parameters and data (must be JSON serializable).
            status: Initial status (default: RecordStatus.PENDING).

        Returns:
            Created Record object with path, status, data, and timestamps.

        Raises:
            TypeError: If data is not JSON serializable.
        """
        data = validate_json_serializable(data) if data is not None else {}
        status_value = status.value if isinstance(status, RecordStatus) else status
        key = self._path_to_key(path)
        now = time.time()

        record_data = {
            "path": path,
            "status": status_value,
            "data": json.dumps(data),
            "created_at": str(now),
            "updated_at": str(now),
        }

        pipe = self.redis.pipeline()
        pipe.hset(key, mapping=record_data)
        pipe.sadd("records:index", path)
        pipe.zadd(f"status:{status_value}", {path: now})
        pipe.execute()

        return self._deserialize_record(record_data)

    def get_record(self, path: str) -> Optional[Record]:
        """Get a single record by exact path.

        Args:
            path: Exact record path.

        Returns:
            Record object or None if not found.
        """
        key = self._path_to_key(path)
        record_data = self.redis.hgetall(key)

        if not record_data:
            return None

        return self._deserialize_record(record_data)

    def list_records(self, prefix: str = "/") -> list[Record]:
        """Get all records matching the path prefix.

        Args:
            prefix: Path prefix to filter records (default: / for all records).

        Returns:
            List of Record objects matching the prefix.
        """
        all_paths = self.redis.smembers("records:index")
        matching_paths = [p for p in all_paths if p.startswith(prefix)]

        records = []
        for path in matching_paths:
            record = self.get_record(path)
            if record:
                records.append(record)

        return sorted(records, key=lambda r: r.created_at)

    def set_status(self, path: str, status: Union[RecordStatus, str]) -> None:
        """Update the status of a record.

        Args:
            path: Record path.
            status: New status (RecordStatus enum or string: pending/running/completed/failed).

        Raises:
            ValueError: If record not found.
        """
        record = self.get_record(path)
        if not record:
            raise ValueError(f"Record not found: {path}")

        status_value = status.value if isinstance(status, RecordStatus) else status
        old_status = record.status.value
        key = self._path_to_key(path)
        now = time.time()

        pipe = self.redis.pipeline()
        pipe.hset(key, "status", status_value)
        pipe.hset(key, "updated_at", str(now))
        pipe.zrem(f"status:{old_status}", path)
        pipe.zadd(f"status:{status_value}", {path: now})
        pipe.execute()

    def get_status(self, path: str) -> Optional[str]:
        """Get the status of a record.

        Args:
            path: Record path.

        Returns:
            Status string or None if record not found.
        """
        key = self._path_to_key(path)
        return self.redis.hget(key, "status")

    def update_data(self, path: str, data: dict[str, Any]) -> None:
        """Update record data (merges with existing data).

        Args:
            path: Record path.
            data: Dictionary of data to merge with existing record data (must be JSON serializable).

        Raises:
            ValueError: If record not found.
            TypeError: If data is not JSON serializable.
        """
        data = validate_json_serializable(data)

        record = self.get_record(path)
        if not record:
            raise ValueError(f"Record not found: {path}")

        existing_data = record.data
        merged_data = {**existing_data, **data}

        key = self._path_to_key(path)
        now = time.time()

        pipe = self.redis.pipeline()
        pipe.hset(key, "data", json.dumps(merged_data))
        pipe.hset(key, "updated_at", str(now))
        pipe.execute()

    def get_data(self, path: str) -> Optional[dict[str, Any]]:
        """Get record data, including any appended list data.

        Args:
            path: Record path.

        Returns:
            Data dictionary or None if record not found.
        """
        key = self._path_to_key(path)
        data_str = self.redis.hget(key, "data")

        if data_str is None:
            return None

        data = json.loads(data_str)

        list_keys = self.redis.smembers(f"{key}:lists")
        for list_key in list_keys:
            list_values = self.redis.lrange(f"{key}:list:{list_key}", 0, -1)
            data[list_key] = [json.loads(v) for v in list_values]

        return data

    def append_data(self, path: str, key: str, value: Any) -> None:
        """Append a primitive value to a list at the specified key.

        Optimized for high-frequency calls with O(1) append operations using Redis Lists.
        Values must be primitive types. First value establishes the type for the list.

        Args:
            path: Record path.
            key: Data key to append to.
            value: Primitive value to append (must match primitive_types).

        Raises:
            ValueError: If record not found.
            TypeError: If value is not primitive or type mismatch with existing list.
        """
        if not isinstance(value, self.primitive_types):
            raise TypeError(
                f"Value must be primitive type, got {type(value).__name__}"
            )

        value_str = json.dumps(value)
        value_type = type(value).__name__
        now = time.time()
        record_key = self._path_to_key(path)

        pipe = self.redis.pipeline()
        pipe.exists(record_key)
        pipe.get(f"{record_key}:list:{key}:type")
        exists, existing_type = pipe.execute()

        if not exists:
            raise ValueError(f"Record not found: {path}")

        if existing_type and existing_type != value_type:
            raise TypeError(f"Type mismatch: expected {existing_type}, got {value_type}")

        pipe = self.redis.pipeline()
        pipe.rpush(f"{record_key}:list:{key}", value_str)
        pipe.hset(record_key, "updated_at", str(now))
        if not existing_type:
            pipe.set(f"{record_key}:list:{key}:type", value_type)
            pipe.sadd(f"{record_key}:lists", key)
        pipe.execute()

    def _deserialize_record(self, record_data: dict[str, str]) -> Record:
        """Convert Redis hash data to Record object.

        Args:
            record_data: Raw Redis hash data.

        Returns:
            Deserialized Record object with proper types.
        """
        return Record(
            path=record_data["path"],
            status=RecordStatus(record_data["status"]),
            data=json.loads(record_data["data"]),
            created_at=float(record_data["created_at"]),
            updated_at=float(record_data["updated_at"]),
        )

    def __call__(
        self, prefix: str = "/", *, wait: bool = True, wait_interval: float = 5.0
    ) -> "TaskIterator":
        """Make TaskQueue callable to return an iterator (Pythonic API).

        Returns an iterator that atomically claims and yields records one at a time.
        Each yielded record is already marked as RUNNING. Perfect for worker loops.

        Args:
            prefix: Path prefix to filter records (default: / for all records).
            wait: If True, wait for new records when queue is empty (default: True).
            wait_interval: Seconds to wait between checks when queue is empty (default: 5.0).

        Returns:
            TaskIterator that yields Record objects.

        Example:
            for record in queue(prefix="/experiments"):
                process(record)
                queue.set_status(record.path, RecordStatus.COMPLETED)
        """
        return TaskIterator(self, prefix=prefix, wait=wait, wait_interval=wait_interval)


class TaskIterator:
    """Iterator for atomically claiming and processing records.

    Implements the iterator protocol for TaskQueue, enabling Pythonic
    for-loop iteration over unfinished records.

    Attributes:
        queue: TaskQueue instance to claim records from.
        prefix: Path prefix to filter records.
        wait: Whether to wait for new records when queue is empty.
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
            prefix: Path prefix to filter records.
            wait: If True, wait for new records when empty.
            wait_interval: Seconds to wait between checks.
        """
        self.queue = queue
        self.prefix = prefix
        self.wait = wait
        self.wait_interval = wait_interval

    def __iter__(self) -> "TaskIterator":
        """Return self as iterator."""
        return self

    def __next__(self) -> Record:
        """Atomically claim and return the next unfinished record.

        This operation is atomic and safe for distributed environments.
        The record is automatically marked as RUNNING when claimed, preventing
        race conditions where multiple workers might pick the same record.

        Prioritizes pending records first, then failed records, ordered by timestamp.

        Returns:
            Next available Record object with status set to RUNNING.

        Raises:
            StopIteration: If no records available and wait=False.
            RuntimeError: If the Lua script execution fails.
        """
        while True:
            now = time.time()

            try:
                claimed_path = self.queue._lua_scripts["claim_next"](
                    keys=[],
                    args=[self.prefix, str(now), RecordStatus.RUNNING.value],
                )
            except redis.RedisError as e:
                raise RuntimeError(f"Failed to claim record: {e}") from e

            if claimed_path:
                record = self.queue.get_record(claimed_path)
                if not record:
                    raise RuntimeError(
                        f"Claimed record {claimed_path} not found - data inconsistency detected"
                    )
                return record

            if not self.wait:
                raise StopIteration

            time.sleep(self.wait_interval)
