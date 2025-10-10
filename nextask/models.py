"""Data models for nextask."""

import json
from dataclasses import dataclass
from enum import Enum
from typing import Any


class RecordStatus(str, Enum):
    """Record execution status."""

    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"


DEFAULT_PRIMITIVE_TYPES = (int, float, str, bool, type(None))


def validate_json_serializable(data: dict[str, Any]) -> dict[str, Any]:
    """Validate that data is JSON serializable.

    Args:
        data: Dictionary to validate.

    Returns:
        The same dictionary if valid.

    Raises:
        TypeError: If data contains non-JSON-serializable values.
    """
    try:
        json.dumps(data)
        return data
    except (TypeError, ValueError) as e:
        raise TypeError(f"Data must be JSON serializable: {e}") from e


@dataclass
class Record:
    """Represents a single record in the task queue.

    Attributes:
        path: Hierarchical path identifying the record.
        status: Current execution status.
        data: Record parameters and results (must be JSON serializable).
        created_at: Unix timestamp of creation.
        updated_at: Unix timestamp of last update.
    """

    path: str
    status: RecordStatus
    data: dict[str, Any]
    created_at: float
    updated_at: float

    @property
    def is_pending(self) -> bool:
        """Check if record is pending."""
        return self.status == RecordStatus.PENDING

    @property
    def is_running(self) -> bool:
        """Check if record is currently running."""
        return self.status == RecordStatus.RUNNING

    @property
    def is_completed(self) -> bool:
        """Check if record completed successfully."""
        return self.status == RecordStatus.COMPLETED

    @property
    def is_failed(self) -> bool:
        """Check if record failed."""
        return self.status == RecordStatus.FAILED

    @property
    def is_finished(self) -> bool:
        """Check if record is finished (completed successfully)."""
        return self.status == RecordStatus.COMPLETED

    @property
    def is_unfinished(self) -> bool:
        """Check if record is unfinished (pending or failed, needs processing)."""
        return self.status in (RecordStatus.PENDING, RecordStatus.FAILED)

    @property
    def duration(self) -> float:
        """Get duration between creation and last update in seconds."""
        return self.updated_at - self.created_at

    @property
    def age(self) -> float:
        """Get age of the record from creation to now in seconds."""
        import time

        return time.time() - self.created_at
