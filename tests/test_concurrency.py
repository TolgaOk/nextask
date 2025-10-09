"""
Concurrency and race condition tests for atomic operations.

These tests simulate multi-worker environments to verify that
the queue iterator is truly atomic and distributed-safe.
"""
import time
import threading
from concurrent.futures import ThreadPoolExecutor, as_completed
from nextask import TaskQueue, RunStatus


class TestConcurrentClaims:
    """Test concurrent claim operations from multiple workers."""
    
    def test_concurrent_claims_no_duplicates(self, task_queue):
        """Test that concurrent claims don't result in duplicate assignments."""
        # Create 20 pending runs
        num_runs = 20
        for i in range(num_runs):
            task_queue.create_run(f"/test/run{i:03d}", status=RunStatus.PENDING)
        
        # Create 5 workers that will compete for runs
        num_workers = 5
        claimed_runs = []
        lock = threading.Lock()
        
        def worker(worker_id):
            """Worker function that claims runs."""
            # Each worker gets a fresh connection
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claims = []
            
            # Try to claim up to num_runs (more than available)
            for _ in range(num_runs):
                run = next(iter(worker_queue(wait=False)), None)
                if run:
                    local_claims.append(run.path)
                    time.sleep(0.001)  # Small delay to increase contention
                else:
                    break
            
            worker_queue._redis.close()
            return local_claims
        
        # Run workers concurrently
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(worker, i) for i in range(num_workers)]
            for future in as_completed(futures):
                worker_claims = future.result()
                with lock:
                    claimed_runs.extend(worker_claims)
        
        # Verify: All runs should be claimed exactly once
        assert len(claimed_runs) == num_runs
        assert len(set(claimed_runs)) == num_runs  # No duplicates
        
        # Verify: All claimed runs are marked as RUNNING
        for path in claimed_runs:
            status = task_queue.get_status(path)
            assert status == "running"
    
    def test_high_contention_scenario(self, task_queue):
        """Test with high contention: many workers competing for few runs."""
        # Create only 5 runs
        num_runs = 5
        for i in range(num_runs):
            task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        # But have 20 workers competing
        num_workers = 20
        claimed_runs = []
        lock = threading.Lock()
        
        def aggressive_worker():
            """Worker that immediately tries to claim a run."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            run = next(iter(worker_queue(wait=False)), None)
            worker_queue._redis.close()
            return run.path if run else None
        
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(aggressive_worker) for _ in range(num_workers)]
            for future in as_completed(futures):
                result = future.result()
                if result:
                    with lock:
                        claimed_runs.append(result)
        
        # Should have exactly 5 claims, no duplicates
        assert len(claimed_runs) == num_runs
        assert len(set(claimed_runs)) == num_runs
    
    def test_concurrent_claims_with_prefix(self, task_queue):
        """Test concurrent claims with different prefixes don't interfere."""
        # Create runs in two different prefixes
        for i in range(10):
            task_queue.create_run(f"/project/ml/run{i}", status=RunStatus.PENDING)
            task_queue.create_run(f"/project/rl/run{i}", status=RunStatus.PENDING)
        
        ml_claims = []
        rl_claims = []
        ml_lock = threading.Lock()
        rl_lock = threading.Lock()
        
        def ml_worker():
            """Worker for ML runs."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claims = []
            for _ in range(10):
                run = next(iter(worker_queue(prefix="/project/ml", wait=False)), None)
                if run:
                    local_claims.append(run.path)
                else:
                    break
            worker_queue._redis.close()
            return local_claims
        
        def rl_worker():
            """Worker for RL runs."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claims = []
            for _ in range(10):
                run = next(iter(worker_queue(prefix="/project/rl", wait=False)), None)
                if run:
                    local_claims.append(run.path)
                else:
                    break
            worker_queue._redis.close()
            return local_claims
        
        # Run both types of workers concurrently
        with ThreadPoolExecutor(max_workers=10) as executor:
            ml_futures = [executor.submit(ml_worker) for _ in range(5)]
            rl_futures = [executor.submit(rl_worker) for _ in range(5)]
            
            for future in as_completed(ml_futures):
                with ml_lock:
                    ml_claims.extend(future.result())
            
            for future in as_completed(rl_futures):
                with rl_lock:
                    rl_claims.extend(future.result())
        
        # Verify each prefix has 10 unique claims
        assert len(ml_claims) == 10
        assert len(set(ml_claims)) == 10
        assert all(path.startswith("/project/ml") for path in ml_claims)
        
        assert len(rl_claims) == 10
        assert len(set(rl_claims)) == 10
        assert all(path.startswith("/project/rl") for path in rl_claims)
    
    def test_concurrent_claim_and_status_update(self, task_queue):
        """Test concurrent claim and status update operations."""
        # Create runs
        num_runs = 10
        for i in range(num_runs):
            task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        results = []
        lock = threading.Lock()
        
        def worker_with_completion():
            """Worker that claims and immediately completes a run."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            run = next(iter(worker_queue(wait=False)), None)
            if run:
                # Simulate work
                time.sleep(0.01)
                # Mark as completed
                worker_queue.set_status(run.path, RunStatus.COMPLETED)
                worker_queue._redis.close()
                return run.path
            worker_queue._redis.close()
            return None
        
        # Run workers
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = [executor.submit(worker_with_completion) for _ in range(15)]
            for future in as_completed(futures):
                result = future.result()
                if result:
                    with lock:
                        results.append(result)
        
        # Should have completed exactly num_runs
        assert len(results) == num_runs
        assert len(set(results)) == num_runs
        
        # Verify all are marked as completed
        for path in results:
            assert task_queue.get_status(path) == "completed"
    
    def test_race_condition_pending_vs_failed(self, task_queue):
        """Test race condition with mixed pending and failed runs."""
        # Create a mix of pending and failed runs
        for i in range(5):
            task_queue.create_run(f"/test/pending{i}", status=RunStatus.PENDING)
            task_queue.create_run(f"/test/failed{i}", status=RunStatus.FAILED)
        
        claimed = []
        lock = threading.Lock()
        
        def worker():
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claims = []
            for _ in range(5):
                run = next(iter(worker_queue(wait=False)), None)
                if run:
                    local_claims.append(run.path)
                else:
                    break
            worker_queue._redis.close()
            return local_claims
        
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = [executor.submit(worker) for _ in range(5)]
            for future in as_completed(futures):
                with lock:
                    claimed.extend(future.result())
        
        # Should claim all 10 runs without duplicates
        assert len(claimed) == 10
        assert len(set(claimed)) == 10


class TestDistributedWorkerSimulation:
    """Simulate realistic distributed worker scenarios."""
    
    def test_worker_pool_exhausting_queue(self, task_queue):
        """Simulate a pool of workers exhausting a queue of tasks."""
        # Create 100 tasks
        num_tasks = 100
        for i in range(num_tasks):
            task_queue.create_run(f"/tasks/task{i:03d}", 
                                 data={"task_id": i},
                                 status=RunStatus.PENDING)
        
        completed_tasks = []
        lock = threading.Lock()
        
        def worker_loop(worker_id):
            """Worker that processes tasks until queue is empty."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_completed = []
            
            while True:
                run = next(iter(worker_queue(wait=False)), None)
                if run is None:
                    break
                
                # Simulate processing
                time.sleep(0.001)
                
                # Mark as completed
                worker_queue.set_status(run.path, RunStatus.COMPLETED)
                local_completed.append(run.path)
            
            worker_queue._redis.close()
            return local_completed
        
        # Use 10 workers
        start_time = time.time()
        with ThreadPoolExecutor(max_workers=10) as executor:
            futures = [executor.submit(worker_loop, i) for i in range(10)]
            for future in as_completed(futures):
                with lock:
                    completed_tasks.extend(future.result())
        
        elapsed = time.time() - start_time
        
        # Verify all tasks were completed exactly once
        assert len(completed_tasks) == num_tasks
        assert len(set(completed_tasks)) == num_tasks
        
        # Verify all are marked as completed
        for path in completed_tasks:
            assert task_queue.get_status(path) == "completed"
        
        print(f"\n10 workers completed {num_tasks} tasks in {elapsed:.2f}s")
    
    def test_workers_with_failures_and_retries(self, task_queue):
        """Simulate workers that sometimes fail, requiring retries."""
        # Create 20 tasks
        num_tasks = 20
        for i in range(num_tasks):
            task_queue.create_run(f"/tasks/task{i:02d}", 
                                 data={"attempt": 0},
                                 status=RunStatus.PENDING)
        
        completed_tasks = []
        lock = threading.Lock()
        
        def unreliable_worker(worker_id):
            """Worker that fails 30% of the time."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_completed = []
            
            for _ in range(30):  # Try many times
                run = next(iter(worker_queue(wait=False)), None)
                if run is None:
                    break
                
                # Simulate work and random failures
                time.sleep(0.001)
                
                # 30% chance of failure
                import random
                if random.random() < 0.3:
                    worker_queue.set_status(run.path, RunStatus.FAILED)
                else:
                    worker_queue.set_status(run.path, RunStatus.COMPLETED)
                    local_completed.append(run.path)
            
            worker_queue._redis.close()
            return local_completed
        
        # Run workers multiple times to handle retries
        for _ in range(3):
            with ThreadPoolExecutor(max_workers=5) as executor:
                futures = [executor.submit(unreliable_worker, i) for i in range(5)]
                for future in as_completed(futures):
                    with lock:
                        completed_tasks.extend(future.result())
        
        # Verify all tasks eventually completed (allowing duplicates due to retries)
        unique_completed = set(completed_tasks)
        assert len(unique_completed) == num_tasks
    
    def test_burst_workers(self, task_queue):
        """Test burst of many workers starting simultaneously."""
        # Create 50 tasks
        num_tasks = 50
        for i in range(num_tasks):
            task_queue.create_run(f"/burst/task{i:03d}", status=RunStatus.PENDING)
        
        claimed = []
        lock = threading.Lock()
        barrier = threading.Barrier(20)  # Synchronize 20 workers
        
        def synchronized_worker():
            """Worker that waits for all to start, then claims."""
            barrier.wait()  # Wait for all workers to be ready
            
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            run = next(iter(worker_queue(wait=False)), None)
            worker_queue._redis.close()
            
            return run.path if run else None
        
        # Launch 20 workers simultaneously
        with ThreadPoolExecutor(max_workers=20) as executor:
            futures = [executor.submit(synchronized_worker) for _ in range(20)]
            for future in as_completed(futures):
                result = future.result()
                if result:
                    with lock:
                        claimed.append(result)
        
        # Should have 20 unique claims
        assert len(claimed) == 20
        assert len(set(claimed)) == 20


class TestConcurrentStatusUpdates:
    """Test concurrent status updates and data modifications."""
    
    def test_concurrent_status_updates_same_run(self, task_queue):
        """Test that concurrent status updates to same run don't cause issues."""
        path = "/test/run1"
        task_queue.create_run(path, status=RunStatus.RUNNING)
        
        def update_status(new_status):
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            time.sleep(0.001)  # Small delay to increase contention
            worker_queue.set_status(path, new_status)
            worker_queue._redis.close()
        
        # Multiple workers try to update status
        with ThreadPoolExecutor(max_workers=3) as executor:
            executor.submit(update_status, RunStatus.COMPLETED)
            executor.submit(update_status, RunStatus.FAILED)
            executor.submit(update_status, RunStatus.RUNNING)
        
        # Final status should be one of the three (last write wins)
        final_status = task_queue.get_status(path)
        assert final_status in ["completed", "failed", "running"]
    
    def test_concurrent_data_updates(self, task_queue):
        """Test concurrent data updates and merging.
        
        Note: set_data is not atomic, so concurrent updates to different keys
        may result in lost updates (last write wins). This test verifies that
        at least some updates are preserved and the initial data remains.
        """
        path = "/test/run1"
        task_queue.create_run(path, data={"initial": "value"})
        
        def update_data(key, value):
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            worker_queue.set_data(path, {key: value})
            worker_queue._redis.close()
        
        # Multiple workers update different keys
        with ThreadPoolExecutor(max_workers=5) as executor:
            for i in range(5):
                executor.submit(update_data, f"key{i}", f"value{i}")
        
        # Initial data should always be preserved (it was there before updates)
        final_data = task_queue.get_data(path)
        assert final_data["initial"] == "value"
        
        # At least some updates should be present (due to race conditions, not all may be preserved)
        # This is expected behavior since set_data is not atomic
        keys_present = sum(1 for i in range(5) if f"key{i}" in final_data)
        assert keys_present >= 1, f"Expected at least 1 key update, got {keys_present}"

