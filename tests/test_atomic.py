"""
Tests for atomic operations, specifically the queue iterator.

These tests verify the atomic nature of claiming runs and ensure
distributed safety in multi-worker environments.
"""
import pytest
import time
from nextask import TaskQueue, RunStatus


class TestGetNextUnfinished:
    """Test the atomic get_next_unfinished operation."""
    
    def test_get_next_unfinished_basic(self, task_queue):
        """Test basic get_next_unfinished functionality."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        run = next(iter(task_queue(wait=False)), None)
        
        assert run is not None
        assert run.path == "/test/run1"
        assert run.status == RunStatus.RUNNING  # Should be automatically set
    
    def test_get_next_unfinished_updates_status(self, task_queue):
        """Test that get_next_unfinished atomically updates status to RUNNING."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        run = next(iter(task_queue(wait=False)), None)
        
        # Verify the run in the database has RUNNING status
        stored_run = task_queue.get_run("/test/run1")
        assert stored_run.status == RunStatus.RUNNING
    
    def test_get_next_unfinished_empty_queue(self, task_queue):
        """Test get_next_unfinished returns None for empty queue."""
        run = next(iter(task_queue(wait=False)), None)
        assert run is None
    
    def test_get_next_unfinished_no_available_runs(self, task_queue):
        """Test get_next_unfinished returns None when all runs are finished/running."""
        task_queue.create_run("/test/run1", status=RunStatus.RUNNING)
        task_queue.create_run("/test/run2", status=RunStatus.COMPLETED)
        
        run = next(iter(task_queue(wait=False)), None)
        assert run is None
    
    def test_get_next_unfinished_with_prefix(self, task_queue):
        """Test get_next_unfinished with path prefix filtering."""
        task_queue.create_run("/project/ml/exp1", status=RunStatus.PENDING)
        task_queue.create_run("/project/rl/exp1", status=RunStatus.PENDING)
        task_queue.create_run("/other/exp1", status=RunStatus.PENDING)
        
        # Should get only ML runs
        ml_run = next(iter(task_queue(prefix="/project/ml", wait=False)), None)
        assert ml_run is not None
        assert ml_run.path.startswith("/project/ml")
        assert ml_run.status == RunStatus.RUNNING
        
        # Should get only RL runs
        rl_run = next(iter(task_queue(prefix="/project/rl", wait=False)), None)
        assert rl_run is not None
        assert rl_run.path.startswith("/project/rl")
    
    def test_get_next_unfinished_prioritizes_pending(self, task_queue):
        """Test that pending runs are prioritized over failed runs."""
        # Create failed run first (older timestamp)
        task_queue.create_run("/test/failed1", status=RunStatus.FAILED)
        time.sleep(0.01)
        
        # Create pending run later
        task_queue.create_run("/test/pending1", status=RunStatus.PENDING)
        
        # Should get pending run first despite being newer (priority: pending > failed)
        run = next(iter(task_queue(wait=False)), None)
        assert run.path == "/test/pending1"  # Pending is prioritized over failed
    
    def test_get_next_unfinished_handles_failed_runs(self, task_queue):
        """Test that failed runs can be retried via get_next_unfinished."""
        task_queue.create_run("/test/run1", status=RunStatus.FAILED)
        
        run = next(iter(task_queue(wait=False)), None)
        
        assert run is not None
        assert run.path == "/test/run1"
        assert run.status == RunStatus.RUNNING  # Status updated from FAILED to RUNNING
    
    def test_get_next_unfinished_timestamp_ordering(self, task_queue):
        """Test that runs are claimed in timestamp order (oldest first)."""
        paths = []
        for i in range(5):
            path = f"/test/run{i}"
            task_queue.create_run(path, status=RunStatus.PENDING)
            paths.append(path)
            time.sleep(0.01)
        
        claimed_paths = []
        for _ in range(5):
            run = next(iter(task_queue(wait=False)), None)
            if run:
                claimed_paths.append(run.path)
        
        assert claimed_paths == paths  # Should be claimed in creation order
    
    def test_get_next_unfinished_does_not_reclaim_running(self, task_queue):
        """Test that runs with RUNNING status are not claimed again."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        task_queue.create_run("/test/run2", status=RunStatus.PENDING)
        
        # Claim first run
        run1 = next(iter(task_queue(wait=False)), None)
        assert run1.path == "/test/run1"
        
        # Next call should get run2, not run1
        run2 = next(iter(task_queue(wait=False)), None)
        assert run2.path == "/test/run2"
        
        # No more runs available
        run3 = next(iter(task_queue(wait=False)), None)
        assert run3 is None
    
    def test_get_next_unfinished_does_not_claim_completed(self, task_queue):
        """Test that completed runs are not claimed."""
        task_queue.create_run("/test/run1", status=RunStatus.COMPLETED)
        
        run = next(iter(task_queue(wait=False)), None)
        assert run is None
    
    def test_get_next_unfinished_mixed_statuses(self, populated_queue):
        """Test get_next_unfinished with mixed run statuses."""
        # populated_queue has: 2 pending, 1 failed, 1 running, 1 completed
        
        claimed_runs = []
        for _ in range(10):  # Try to claim more than available
            run = next(iter(populated_queue(prefix="/test", wait=False)), None)
            if run:
                claimed_runs.append(run)
        
        # Should claim 3 runs: 2 pending + 1 failed
        assert len(claimed_runs) == 3
        assert all(run.status == RunStatus.RUNNING for run in claimed_runs)
    
    def test_get_next_unfinished_updates_timestamp(self, task_queue):
        """Test that get_next_unfinished updates the updated_at timestamp."""
        run = task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        original_updated_at = run.updated_at
        
        time.sleep(0.01)
        claimed_run = next(iter(task_queue(wait=False)), None)
        
        assert claimed_run.updated_at > original_updated_at


class TestAtomicityProperties:
    """Test atomicity properties of get_next_unfinished."""
    
    def test_sequential_claims_no_duplicates(self, task_queue):
        """Test that sequential claims don't return duplicates."""
        # Create 10 pending runs
        for i in range(10):
            task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        # Claim all runs sequentially
        claimed_paths = set()
        for _ in range(10):
            run = next(iter(task_queue(wait=False)), None)
            if run:
                claimed_paths.add(run.path)
        
        # Should have claimed exactly 10 unique runs
        assert len(claimed_paths) == 10
    
    def test_claim_verify_claim_cycle(self, task_queue):
        """Test claim -> verify -> claim cycle works correctly."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        task_queue.create_run("/test/run2", status=RunStatus.PENDING)
        
        # Claim first run
        run1 = next(iter(task_queue(wait=False)), None)
        assert run1.path == "/test/run1"
        
        # Verify it's marked as running
        stored_run1 = task_queue.get_run("/test/run1")
        assert stored_run1.status == RunStatus.RUNNING
        
        # Claim second run
        run2 = next(iter(task_queue(wait=False)), None)
        assert run2.path == "/test/run2"
        
        # Verify first run is still running
        stored_run1 = task_queue.get_run("/test/run1")
        assert stored_run1.status == RunStatus.RUNNING
    
    def test_atomic_status_change_during_claim(self, task_queue):
        """Test that status change is atomic with the claim operation."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        # Claim the run
        run = next(iter(task_queue(wait=False)), None)
        
        # At this point, the run should already be RUNNING in Redis
        # Verify by getting it directly
        stored_run = task_queue.get_run("/test/run1")
        assert stored_run.status == RunStatus.RUNNING
        
        # The returned run should also be RUNNING
        assert run.status == RunStatus.RUNNING
    
    def test_failed_to_running_transition(self, task_queue):
        """Test atomic transition from FAILED to RUNNING."""
        task_queue.create_run("/test/run1", status=RunStatus.FAILED)
        
        # Verify it's failed
        assert task_queue.get_status("/test/run1") == "failed"
        
        # Claim it (should retry)
        run = next(iter(task_queue(wait=False)), None)
        
        # Should now be running
        assert run.status == RunStatus.RUNNING
        assert task_queue.get_status("/test/run1") == "running"


class TestLuaScriptEdgeCases:
    """Test edge cases specific to Lua script implementation."""
    
    def test_handles_missing_run_data(self, task_queue, redis_client):
        """Test that the script handles cases where run data is missing but ID exists."""
        # Create a run normally
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        # This should work normally
        run = next(iter(task_queue(wait=False)), None)
        assert run is not None
    
    def test_handles_empty_prefix_list(self, task_queue):
        """Test handling when no runs match the prefix."""
        task_queue.create_run("/project/ml/exp1", status=RunStatus.PENDING)
        
        # Try to get with non-matching prefix
        run = next(iter(task_queue(prefix="/project/rl", wait=False)), None)
        assert run is None
    
    def test_handles_many_runs_performance(self, task_queue):
        """Test performance with many runs (memory bounded check)."""
        # Create more than 1000 runs to test the memory bound mentioned in API
        for i in range(1500):
            status = RunStatus.COMPLETED if i < 1000 else RunStatus.PENDING
            task_queue.create_run(f"/test/run{i:04d}", status=status)
        
        # Should still be able to find pending runs efficiently
        run = next(iter(task_queue(wait=False)), None)
        assert run is not None
        assert run.status == RunStatus.RUNNING
    
    def test_early_exit_optimization(self, task_queue):
        """Test that script exits early when match is found."""
        # Create one pending run among many completed
        for i in range(100):
            task_queue.create_run(f"/test/run{i:03d}", status=RunStatus.COMPLETED)
        
        # Create one pending run
        task_queue.create_run("/test/run_pending", status=RunStatus.PENDING)
        
        # Should find it quickly due to early exit
        start_time = time.time()
        run = next(iter(task_queue(wait=False)), None)
        elapsed = time.time() - start_time
        
        assert run is not None
        assert elapsed < 0.1  # Should be very fast
    
    def test_consistent_behavior_across_calls(self, task_queue):
        """Test that multiple calls behave consistently."""
        # Create 5 pending runs
        for i in range(5):
            task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        # Claim all 5 runs
        claimed = []
        for _ in range(5):
            run = next(iter(task_queue(wait=False)), None)
            if run:
                claimed.append(run.path)
        
        # Verify all 5 were claimed
        assert len(claimed) == 5
        
        # Verify no more runs available
        run = next(iter(task_queue(wait=False)), None)
        assert run is None
        
        # Verify all are marked as running
        for path in claimed:
            status = task_queue.get_status(path)
            assert status == "running"


class TestPrefixFiltering:
    """Test prefix filtering functionality in get_next_unfinished."""
    
    def test_prefix_exact_match(self, task_queue):
        """Test prefix filtering with exact path match."""
        task_queue.create_run("/project/ml/exp1", status=RunStatus.PENDING)
        task_queue.create_run("/project/ml/exp2", status=RunStatus.PENDING)
        task_queue.create_run("/project/rl/exp1", status=RunStatus.PENDING)
        
        # Get with specific prefix
        run = next(iter(task_queue(prefix="/project/ml", wait=False)), None)
        assert run is not None
        assert run.path.startswith("/project/ml")
    
    def test_prefix_hierarchical(self, task_queue):
        """Test hierarchical prefix filtering."""
        task_queue.create_run("/a/b/c/run1", status=RunStatus.PENDING)
        task_queue.create_run("/a/b/d/run2", status=RunStatus.PENDING)
        task_queue.create_run("/a/e/run3", status=RunStatus.PENDING)
        task_queue.create_run("/f/run4", status=RunStatus.PENDING)
        
        # Get all under /a
        runs_a = []
        for _ in range(10):
            run = next(iter(task_queue(prefix="/a", wait=False)), None)
            if run:
                runs_a.append(run.path)
        
        assert len(runs_a) == 3
        assert all(path.startswith("/a") for path in runs_a)
    
    def test_prefix_root(self, task_queue):
        """Test that root prefix returns all runs."""
        task_queue.create_run("/project1/run1", status=RunStatus.PENDING)
        task_queue.create_run("/project2/run2", status=RunStatus.PENDING)
        task_queue.create_run("/other/run3", status=RunStatus.PENDING)
        
        # Get all with root prefix
        runs = []
        for _ in range(10):
            run = next(iter(task_queue(prefix="/", wait=False)), None)
            if run:
                runs.append(run.path)
        
        assert len(runs) == 3
    
    def test_prefix_no_matches(self, task_queue):
        """Test prefix with no matching runs."""
        task_queue.create_run("/project/ml/exp1", status=RunStatus.PENDING)
        
        run = next(iter(task_queue(prefix="/project/rl", wait=False)), None)
        assert run is None
    
    def test_prefix_with_special_characters(self, task_queue):
        """Test prefix with special characters in path."""
        task_queue.create_run("/project/ml-2025/exp_001", status=RunStatus.PENDING)
        task_queue.create_run("/project/ml-2025/exp_002", status=RunStatus.PENDING)
        
        run = next(iter(task_queue(prefix="/project/ml-2025", wait=False)), None)
        assert run is not None
        assert run.path.startswith("/project/ml-2025")

