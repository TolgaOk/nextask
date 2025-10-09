"""Data models for nextask."""

import json
from dataclasses import dataclass
from enum import Enum
from typing import Any


class RunStatus(str, Enum):
    """Run execution status."""

    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"


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
class Run:
    """Represents a single ML experiment run.

    Attributes:
        path: Hierarchical path identifying the run.
        status: Current execution status.
        data: Run parameters and results (must be JSON serializable).
        created_at: Unix timestamp of creation.
        updated_at: Unix timestamp of last update.
    """

    path: str
    status: RunStatus
    data: dict[str, Any]
    created_at: float
    updated_at: float

    @property
    def is_pending(self) -> bool:
        """Check if run is pending."""
        return self.status == RunStatus.PENDING

    @property
    def is_running(self) -> bool:
        """Check if run is currently running."""
        return self.status == RunStatus.RUNNING

    @property
    def is_completed(self) -> bool:
        """Check if run completed successfully."""
        return self.status == RunStatus.COMPLETED

    @property
    def is_failed(self) -> bool:
        """Check if run failed."""
        return self.status == RunStatus.FAILED

    @property
    def is_finished(self) -> bool:
        """Check if run is finished (completed successfully)."""
        return self.status == RunStatus.COMPLETED

    @property
    def is_unfinished(self) -> bool:
        """Check if run is unfinished (pending or failed, needs processing)."""
        return self.status in (RunStatus.PENDING, RunStatus.FAILED)

    @property
    def duration(self) -> float:
        """Get duration between creation and last update in seconds."""
        return self.updated_at - self.created_at

    @property
    def age(self) -> float:
        """Get age of the run from creation to now in seconds."""
        import time

        return time.time() - self.created_at
