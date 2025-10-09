"""
Basic unit tests for TaskQueue operations.

Tests cover:
- Queue initialization
- Basic CRUD operations (create_run, get_run, get_runs)
- Status management (set_status, get_status)
- Data management (set_data, get_data)
- Run dataclass properties
"""
import pytest
import time
from nextask import TaskQueue, Run, RunStatus


class TestTaskQueueInitialization:
    """Test TaskQueue initialization and connection."""
    
    def test_default_initialization(self):
        """Test TaskQueue can be initialized with default parameters."""
        queue = TaskQueue()
        assert queue is not None
        queue._redis.close()
    
    def test_custom_initialization(self):
        """Test TaskQueue can be initialized with custom parameters."""
        queue = TaskQueue(host="localhost", port=6379, db=15)
        assert queue is not None
        # Verify connection works
        queue._redis.ping()
        queue._redis.flushdb()
        queue._redis.close()
    
    def test_connection_ping(self, task_queue):
        """Test Redis connection is alive."""
        assert task_queue._redis.ping() is True


class TestRunCreation:
    """Test run creation functionality."""
    
    def test_create_run_basic(self, task_queue):
        """Test creating a basic run with minimal parameters."""
        run = task_queue.create_run("/test/run1")
        
        assert run is not None
        assert run.path == "/test/run1"
        assert run.status == RunStatus.PENDING
        assert run.data == {}
        assert run.created_at > 0
        assert run.updated_at > 0
        assert run.created_at == run.updated_at
    
    def test_create_run_with_data(self, task_queue):
        """Test creating a run with data."""
        data = {"learning_rate": 0.001, "batch_size": 32, "epochs": 100}
        run = task_queue.create_run("/test/run2", data=data)
        
        assert run.path == "/test/run2"
        assert run.data == data
        assert run.data["learning_rate"] == 0.001
    
    def test_create_run_with_status(self, task_queue):
        """Test creating a run with specific initial status."""
        run = task_queue.create_run("/test/run3", status=RunStatus.RUNNING)
        
        assert run.status == RunStatus.RUNNING
    
    def test_create_run_with_string_status(self, task_queue):
        """Test creating a run with status as string."""
        run = task_queue.create_run("/test/run4", status="completed")
        
        assert run.status == RunStatus.COMPLETED
    
    def test_create_run_non_json_serializable_data(self, task_queue):
        """Test that non-JSON serializable data raises TypeError."""
        with pytest.raises(TypeError):
            task_queue.create_run("/test/run5", data={"func": lambda x: x})
    
    def test_create_multiple_runs(self, task_queue):
        """Test creating multiple runs."""
        paths = [f"/test/run{i}" for i in range(10)]
        
        for path in paths:
            run = task_queue.create_run(path)
            assert run.path == path
        
        # Verify all runs exist
        runs = task_queue.get_runs("/test")
        assert len(runs) == 10
    
    def test_create_run_hierarchical_paths(self, task_queue):
        """Test creating runs with hierarchical paths."""
        paths = [
            "/projects/ml/ppo/2025-01-01/exp-001",
            "/projects/ml/ppo/2025-01-01/exp-002",
            "/projects/ml/dqn/2025-01-02/exp-001",
            "/projects/rl/a3c/2025-01-03/exp-001"
        ]
        
        for path in paths:
            run = task_queue.create_run(path, data={"path": path})
            assert run.path == path


class TestRunRetrieval:
    """Test run retrieval functionality."""
    
    def test_get_run_existing(self, task_queue):
        """Test retrieving an existing run."""
        created = task_queue.create_run("/test/run1", data={"key": "value"})
        retrieved = task_queue.get_run("/test/run1")
        
        assert retrieved is not None
        assert retrieved.path == created.path
        assert retrieved.status == created.status
        assert retrieved.data == created.data
    
    def test_get_run_non_existing(self, task_queue):
        """Test retrieving a non-existent run returns None."""
        run = task_queue.get_run("/non/existent/path")
        assert run is None
    
    def test_get_runs_all(self, task_queue):
        """Test retrieving all runs."""
        for i in range(5):
            task_queue.create_run(f"/test/run{i}")
        
        runs = task_queue.get_runs("/")
        assert len(runs) == 5
    
    def test_get_runs_with_prefix(self, task_queue):
        """Test retrieving runs with specific prefix."""
        task_queue.create_run("/project/ml/exp1")
        task_queue.create_run("/project/ml/exp2")
        task_queue.create_run("/project/rl/exp1")
        task_queue.create_run("/other/exp1")
        
        ml_runs = task_queue.get_runs("/project/ml")
        assert len(ml_runs) == 2
        assert all(run.path.startswith("/project/ml") for run in ml_runs)
        
        project_runs = task_queue.get_runs("/project")
        assert len(project_runs) == 3
    
    def test_get_runs_empty_queue(self, task_queue):
        """Test retrieving runs from empty queue."""
        runs = task_queue.get_runs("/")
        assert runs == []
    
    def test_get_runs_sorted_by_creation(self, task_queue):
        """Test that get_runs returns runs sorted by creation time."""
        paths = []
        for i in range(5):
            path = f"/test/run{i}"
            task_queue.create_run(path)
            paths.append(path)
            time.sleep(0.01)  # Small delay to ensure different timestamps
        
        runs = task_queue.get_runs("/test")
        retrieved_paths = [run.path for run in runs]
        
        assert retrieved_paths == paths  # Should be in creation order


class TestStatusManagement:
    """Test status management functionality."""
    
    def test_set_status(self, task_queue):
        """Test setting run status."""
        run = task_queue.create_run("/test/run1")
        original_updated_at = run.updated_at
        
        time.sleep(0.01)
        task_queue.set_status("/test/run1", RunStatus.RUNNING)
        
        updated_run = task_queue.get_run("/test/run1")
        assert updated_run.status == RunStatus.RUNNING
        assert updated_run.updated_at > original_updated_at
    
    def test_set_status_string(self, task_queue):
        """Test setting status with string value."""
        task_queue.create_run("/test/run1")
        task_queue.set_status("/test/run1", "completed")
        
        run = task_queue.get_run("/test/run1")
        assert run.status == RunStatus.COMPLETED
    
    def test_set_status_non_existing(self, task_queue):
        """Test setting status on non-existent run raises ValueError."""
        with pytest.raises(ValueError):
            task_queue.set_status("/non/existent", RunStatus.RUNNING)
    
    def test_get_status(self, task_queue):
        """Test getting run status."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        status = task_queue.get_status("/test/run1")
        assert status == "pending"
    
    def test_get_status_non_existing(self, task_queue):
        """Test getting status of non-existent run returns None."""
        status = task_queue.get_status("/non/existent")
        assert status is None
    
    def test_status_transitions(self, task_queue):
        """Test typical status transition workflow."""
        path = "/test/run1"
        task_queue.create_run(path, status=RunStatus.PENDING)
        
        # Pending -> Running
        task_queue.set_status(path, RunStatus.RUNNING)
        assert task_queue.get_status(path) == "running"
        
        # Running -> Completed
        task_queue.set_status(path, RunStatus.COMPLETED)
        assert task_queue.get_status(path) == "completed"
    
    def test_status_failed_transition(self, task_queue):
        """Test transition to failed status."""
        path = "/test/run1"
        task_queue.create_run(path)
        task_queue.set_status(path, RunStatus.RUNNING)
        task_queue.set_status(path, RunStatus.FAILED)
        
        assert task_queue.get_status(path) == "failed"


class TestDataManagement:
    """Test data management functionality."""
    
    def test_set_data(self, task_queue):
        """Test setting run data."""
        path = "/test/run1"
        task_queue.create_run(path, data={"initial": "value"})
        
        new_data = {"updated": "data", "count": 42}
        task_queue.set_data(path, new_data)
        
        run = task_queue.get_run(path)
        assert run.data["updated"] == "data"
        assert run.data["count"] == 42
    
    def test_set_data_merge(self, task_queue):
        """Test that set_data merges with existing data."""
        path = "/test/run1"
        task_queue.create_run(path, data={"key1": "value1", "key2": "value2"})
        
        task_queue.set_data(path, {"key2": "updated", "key3": "value3"})
        
        run = task_queue.get_run(path)
        assert run.data["key1"] == "value1"  # Original key preserved
        assert run.data["key2"] == "updated"  # Updated key
        assert run.data["key3"] == "value3"  # New key added
    
    def test_set_data_updates_timestamp(self, task_queue):
        """Test that set_data updates the updated_at timestamp."""
        path = "/test/run1"
        run = task_queue.create_run(path)
        original_updated_at = run.updated_at
        
        time.sleep(0.01)
        task_queue.set_data(path, {"new": "data"})
        
        updated_run = task_queue.get_run(path)
        assert updated_run.updated_at > original_updated_at
    
    def test_set_data_non_existing(self, task_queue):
        """Test setting data on non-existent run raises ValueError."""
        with pytest.raises(ValueError):
            task_queue.set_data("/non/existent", {"data": "value"})
    
    def test_set_data_non_json_serializable(self, task_queue):
        """Test that non-JSON serializable data raises TypeError."""
        path = "/test/run1"
        task_queue.create_run(path)
        
        with pytest.raises(TypeError):
            task_queue.set_data(path, {"func": lambda x: x})
    
    def test_get_data(self, task_queue):
        """Test getting run data."""
        path = "/test/run1"
        data = {"param1": "value1", "param2": 42}
        task_queue.create_run(path, data=data)
        
        retrieved_data = task_queue.get_data(path)
        assert retrieved_data == data
    
    def test_get_data_non_existing(self, task_queue):
        """Test getting data of non-existent run returns None."""
        data = task_queue.get_data("/non/existent")
        assert data is None
    
    def test_get_data_empty(self, task_queue):
        """Test getting data when no data was set."""
        path = "/test/run1"
        task_queue.create_run(path)
        
        data = task_queue.get_data(path)
        assert data == {}


class TestRunDataclass:
    """Test Run dataclass properties and methods."""
    
    def test_run_properties_pending(self, task_queue):
        """Test Run properties for pending status."""
        run = task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        assert run.is_pending is True
        assert run.is_running is False
        assert run.is_completed is False
        assert run.is_failed is False
        assert run.is_finished is False
        assert run.is_unfinished is True
    
    def test_run_properties_running(self, task_queue):
        """Test Run properties for running status."""
        run = task_queue.create_run("/test/run1", status=RunStatus.RUNNING)
        
        assert run.is_pending is False
        assert run.is_running is True
        assert run.is_completed is False
        assert run.is_failed is False
        assert run.is_finished is False
        assert run.is_unfinished is False
    
    def test_run_properties_completed(self, task_queue):
        """Test Run properties for completed status."""
        run = task_queue.create_run("/test/run1", status=RunStatus.COMPLETED)
        
        assert run.is_pending is False
        assert run.is_running is False
        assert run.is_completed is True
        assert run.is_failed is False
        assert run.is_finished is True
        assert run.is_unfinished is False
    
    def test_run_properties_failed(self, task_queue):
        """Test Run properties for failed status."""
        run = task_queue.create_run("/test/run1", status=RunStatus.FAILED)
        
        assert run.is_pending is False
        assert run.is_running is False
        assert run.is_completed is False
        assert run.is_failed is True
        assert run.is_finished is False  # Failed runs are not finished (can be retried)
        assert run.is_unfinished is True  # Failed runs are unfinished (need retry)
    
    def test_run_duration(self, task_queue):
        """Test Run duration calculation."""
        path = "/test/run1"
        run = task_queue.create_run(path)
        initial_duration = run.duration
        
        time.sleep(0.1)
        task_queue.set_status(path, RunStatus.RUNNING)
        
        updated_run = task_queue.get_run(path)
        assert updated_run.duration > initial_duration
        assert updated_run.duration >= 0.1
    
    def test_run_age(self, task_queue):
        """Test Run age calculation."""
        run = task_queue.create_run("/test/run1")
        
        time.sleep(0.1)
        
        # Age should be at least 0.1 seconds
        assert run.age >= 0.1
    
    def test_run_timestamps(self, task_queue):
        """Test that created_at and updated_at are Unix timestamps."""
        run = task_queue.create_run("/test/run1")
        
        current_time = time.time()
        assert abs(run.created_at - current_time) < 1  # Within 1 second
        assert abs(run.updated_at - current_time) < 1

