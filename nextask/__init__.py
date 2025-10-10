"""nextask: Redis-based task distribution for ML experiments."""

from nextask.models import Record, RecordStatus
from nextask.queue import TaskQueue

# Backward compatibility aliases
Run = Record
RunStatus = RecordStatus

__all__ = ["TaskQueue", "Record", "RecordStatus", "Run", "RunStatus"]
