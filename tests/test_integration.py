"""
Integration tests for queue iterator pattern and end-to-end workflows.

Tests cover:
- Queue iterator (for loop) pattern
- Complete worker workflows
- Real-world usage scenarios
- wait and wait_interval parameters
"""
import pytest
import time
import threading
from concurrent.futures import ThreadPoolExecutor
from nextask import TaskQueue, RunStatus


class TestQueueIterator:
    """Test the queue iterator pattern."""
    
    def test_queue_iterator_basic(self, task_queue):
        """Test basic queue iterator usage without waiting."""
        # Create some tasks
        for i in range(5):
            task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        # Process all tasks
        processed = []
        for run in task_queue(wait=False):
            processed.append(run.path)
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        assert len(processed) == 5
        assert len(set(processed)) == 5  # All unique
    
    def test_queue_iterator_empty_queue(self, task_queue):
        """Test queue iterator on empty queue without waiting."""
        processed = []
        for run in task_queue(wait=False):
            processed.append(run.path)
        
        assert len(processed) == 0
    
    def test_queue_iterator_with_prefix(self, task_queue):
        """Test queue iterator with prefix filtering."""
        # Create tasks in different prefixes
        for i in range(3):
            task_queue.create_run(f"/ml/run{i}", status=RunStatus.PENDING)
            task_queue.create_run(f"/rl/run{i}", status=RunStatus.PENDING)
        
        # Process only ML tasks
        ml_processed = []
        for run in task_queue(prefix="/ml", wait=False):
            ml_processed.append(run.path)
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        assert len(ml_processed) == 3
        assert all(p.startswith("/ml") for p in ml_processed)
    
    def test_queue_iterator_auto_status_update(self, task_queue):
        """Test that iterator automatically sets status to RUNNING."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        for run in task_queue(wait=False):
            # Run should already be RUNNING when yielded
            assert run.status == RunStatus.RUNNING
            
            # Verify in database
            stored_run = task_queue.get_run(run.path)
            assert stored_run.status == RunStatus.RUNNING
            break
    
    def test_queue_iterator_with_failures(self, task_queue):
        """Test iterator handles failed runs correctly."""
        # Create pending and failed runs
        task_queue.create_run("/test/pending1", status=RunStatus.PENDING)
        task_queue.create_run("/test/failed1", status=RunStatus.FAILED)
        task_queue.create_run("/test/pending2", status=RunStatus.PENDING)
        
        processed = []
        for run in task_queue(wait=False):
            processed.append(run.path)
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        # Should process all unfinished runs
        assert len(processed) == 3
    
    def test_queue_iterator_break_early(self, task_queue):
        """Test breaking out of iterator early."""
        for i in range(10):
            task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        processed = []
        for run in task_queue(wait=False):
            processed.append(run.path)
            task_queue.set_status(run.path, RunStatus.COMPLETED)
            if len(processed) >= 3:
                break
        
        assert len(processed) == 3
        
        # Remaining runs should still be available
        remaining = task_queue.get_runs("/test")
        pending_count = sum(1 for r in remaining if r.status == RunStatus.PENDING)
        assert pending_count == 7


class TestQueueIteratorWithWait:
    """Test queue iterator with wait functionality."""
    
    def test_queue_iterator_wait_timeout(self, task_queue):
        """Test that iterator waits and eventually times out."""
        # Empty queue - would wait forever with wait=True
        # So we test with a timeout in a separate thread
        
        processed = []
        
        def consumer():
            # Wait with very short interval
            for run in task_queue(wait=True, wait_interval=0.1):
                processed.append(run.path)
                task_queue.set_status(run.path, RunStatus.COMPLETED)
                if len(processed) >= 2:
                    break
        
        consumer_thread = threading.Thread(target=consumer)
        consumer_thread.start()
        
        # Give consumer time to start waiting
        time.sleep(0.05)
        
        # Add tasks while consumer is waiting
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        time.sleep(0.15)  # Wait for consumer to pick it up
        
        task_queue.create_run("/test/run2", status=RunStatus.PENDING)
        
        # Wait for consumer to finish
        consumer_thread.join(timeout=2.0)
        
        assert len(processed) == 2
    
    def test_queue_iterator_producer_consumer(self, task_queue):
        """Test producer-consumer pattern with wait."""
        processed = []
        lock = threading.Lock()
        
        def consumer():
            for run in task_queue(wait=True, wait_interval=0.05):
                with lock:
                    processed.append(run.path)
                task_queue.set_status(run.path, RunStatus.COMPLETED)
                if len(processed) >= 5:
                    break
        
        def producer():
            for i in range(5):
                time.sleep(0.1)  # Slow producer
                task_queue.create_run(f"/test/run{i}", status=RunStatus.PENDING)
        
        consumer_thread = threading.Thread(target=consumer)
        producer_thread = threading.Thread(target=producer)
        
        consumer_thread.start()
        time.sleep(0.05)  # Let consumer start first
        producer_thread.start()
        
        producer_thread.join(timeout=2.0)
        consumer_thread.join(timeout=3.0)
        
        assert len(processed) == 5


class TestEndToEndWorkflows:
    """Test complete end-to-end workflows."""
    
    def test_simple_ml_experiment_workflow(self, task_queue):
        """Test a simple ML experiment workflow."""
        # Setup: Create experiment configurations
        experiments = [
            {"lr": 0.001, "batch_size": 32},
            {"lr": 0.01, "batch_size": 64},
            {"lr": 0.0001, "batch_size": 128},
        ]
        
        # Create runs
        for i, config in enumerate(experiments):
            task_queue.create_run(
                f"/experiments/ppo/2025-01-01/exp{i:03d}",
                data=config,
                status=RunStatus.PENDING
            )
        
        # Worker processes experiments
        results = {}
        for run in task_queue(prefix="/experiments", wait=False):
            # Simulate training
            lr = run.data["lr"]
            batch_size = run.data["batch_size"]
            
            # Fake result based on hyperparameters
            reward = lr * 100 + batch_size * 0.1
            
            # Store result
            task_queue.set_data(run.path, {"reward": reward})
            task_queue.set_status(run.path, RunStatus.COMPLETED)
            
            results[run.path] = reward
        
        # Verify all completed
        runs = task_queue.get_runs("/experiments")
        assert all(r.status == RunStatus.COMPLETED for r in runs)
        assert len(results) == 3
        
        # Verify results stored
        for run in runs:
            assert "reward" in run.data
            assert run.data["reward"] == results[run.path]
    
    def test_workflow_with_retries(self, task_queue):
        """Test workflow where some runs fail and are retried."""
        # Create runs
        for i in range(5):
            task_queue.create_run(f"/test/run{i}", 
                                 data={"attempts": 0},
                                 status=RunStatus.PENDING)
        
        def simulate_worker_first_attempt():
            """Worker that fails on first attempt."""
            processed = 0
            for run in task_queue(wait=False):
                attempts = run.data.get("attempts", 0)
                
                if attempts == 0:
                    # First attempt fails
                    task_queue.set_data(run.path, {"attempts": attempts + 1})
                    task_queue.set_status(run.path, RunStatus.FAILED)
                    processed += 1
                    if processed >= 5:
                        break  # Only process 5 runs in this pass
        
        def simulate_worker_retry():
            """Worker that retries failed runs."""
            for run in task_queue(wait=False):
                attempts = run.data.get("attempts", 0)
                # Retry failed runs
                task_queue.set_data(run.path, {"attempts": attempts + 1})
                task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        # First pass - all fail
        simulate_worker_first_attempt()
        
        runs = task_queue.get_runs("/test")
        assert all(r.status == RunStatus.FAILED for r in runs)
        assert all(r.data["attempts"] == 1 for r in runs)
        
        # Second pass - all succeed
        simulate_worker_retry()
        
        runs = task_queue.get_runs("/test")
        assert all(r.status == RunStatus.COMPLETED for r in runs)
        assert all(r.data["attempts"] == 2 for r in runs)
    
    def test_multi_stage_pipeline(self, task_queue):
        """Test multi-stage pipeline workflow."""
        # Stage 1: Data preparation
        for i in range(3):
            task_queue.create_run(f"/pipeline/data_prep/job{i}",
                                 data={"stage": "prep"},
                                 status=RunStatus.PENDING)
        
        # Process stage 1
        for run in task_queue(prefix="/pipeline/data_prep", wait=False):
            task_queue.set_data(run.path, {"stage": "prep", "output": f"data_{run.path}"})
            task_queue.set_status(run.path, RunStatus.COMPLETED)
            
            # Create stage 2 job
            job_id = run.path.split("/")[-1]
            task_queue.create_run(f"/pipeline/training/{job_id}",
                                 data={"stage": "train", "input": f"data_{run.path}"},
                                 status=RunStatus.PENDING)
        
        # Process stage 2
        for run in task_queue(prefix="/pipeline/training", wait=False):
            task_queue.set_data(run.path, {"stage": "train", "model": f"model_{run.path}"})
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        # Verify pipeline completed
        prep_runs = task_queue.get_runs("/pipeline/data_prep")
        train_runs = task_queue.get_runs("/pipeline/training")
        
        assert len(prep_runs) == 3
        assert len(train_runs) == 3
        assert all(r.status == RunStatus.COMPLETED for r in prep_runs)
        assert all(r.status == RunStatus.COMPLETED for r in train_runs)
    
    def test_distributed_workers_workflow(self, task_queue):
        """Test realistic distributed workers scenario."""
        # Create a batch of 50 tasks
        num_tasks = 50
        for i in range(num_tasks):
            task_queue.create_run(f"/batch/task{i:03d}",
                                 data={"value": i},
                                 status=RunStatus.PENDING)
        
        results = []
        lock = threading.Lock()
        
        def worker(worker_id):
            """Worker that processes tasks."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_results = []
            
            for run in worker_queue(prefix="/batch", wait=False):
                # Simulate processing
                result = run.data["value"] ** 2
                
                # Store result
                worker_queue.set_data(run.path, {"result": result})
                worker_queue.set_status(run.path, RunStatus.COMPLETED)
                
                local_results.append((run.path, result))
                time.sleep(0.001)  # Simulate work
            
            worker_queue._redis.close()
            return local_results
        
        # Run 5 workers concurrently
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = [executor.submit(worker, i) for i in range(5)]
            for future in futures:
                worker_results = future.result()
                with lock:
                    results.extend(worker_results)
        
        # Verify all tasks completed
        assert len(results) == num_tasks
        assert len(set(r[0] for r in results)) == num_tasks  # All unique
        
        # Verify all results stored correctly
        runs = task_queue.get_runs("/batch")
        assert all(r.status == RunStatus.COMPLETED for r in runs)
        assert all("result" in r.data for r in runs)


class TestRealWorldScenarios:
    """Test real-world usage scenarios."""
    
    def test_hyperparameter_search(self, task_queue):
        """Test hyperparameter search scenario."""
        # Create grid search
        learning_rates = [0.001, 0.01, 0.1]
        batch_sizes = [32, 64, 128]
        
        for lr in learning_rates:
            for bs in batch_sizes:
                path = f"/search/lr{lr}_bs{bs}"
                task_queue.create_run(path, 
                                     data={"lr": lr, "batch_size": bs},
                                     status=RunStatus.PENDING)
        
        # Workers process experiments
        for run in task_queue(prefix="/search", wait=False):
            # Simulate training and evaluation
            reward = run.data["lr"] * 10 + run.data["batch_size"] * 0.1
            
            task_queue.set_data(run.path, {"reward": reward})
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        # Find best configuration
        runs = task_queue.get_runs("/search")
        best_run = max(runs, key=lambda r: r.data["reward"])
        
        assert all(r.status == RunStatus.COMPLETED for r in runs)
        assert len(runs) == 9
        assert "reward" in best_run.data
    
    def test_checkpoint_resume_scenario(self, task_queue):
        """Test scenario where worker crashes and another resumes."""
        # Create long-running task
        path = "/training/long_run"
        task_queue.create_run(path, 
                             data={"checkpoint": 0, "max_steps": 10},
                             status=RunStatus.PENDING)
        
        # Worker 1 starts but crashes after 3 steps
        run = next(iter(task_queue(wait=False)), None)
        for step in range(3):
            task_queue.set_data(run.path, {"checkpoint": step + 1})
        # Worker crashes (doesn't set status to completed)
        
        # Manually mark as failed (timeout system would do this)
        task_queue.set_status(path, RunStatus.FAILED)
        
        # Worker 2 resumes from checkpoint
        run = next(iter(task_queue(wait=False)), None)
        checkpoint = run.data["checkpoint"]
        assert checkpoint == 3
        
        # Continue from checkpoint
        max_steps = run.data["max_steps"]
        for step in range(checkpoint, max_steps):
            task_queue.set_data(run.path, {"checkpoint": step + 1})
        
        task_queue.set_status(path, RunStatus.COMPLETED)
        
        final_run = task_queue.get_run(path)
        assert final_run.status == RunStatus.COMPLETED
        assert final_run.data["checkpoint"] == 10
    
    def test_priority_based_processing(self, task_queue):
        """Test processing high-priority tasks first."""
        # Create tasks with different priorities
        for i in range(5):
            # Low priority (created first, older timestamps)
            task_queue.create_run(f"/tasks/low_priority_{i}",
                                 data={"priority": "low"},
                                 status=RunStatus.PENDING)
        
        # Create high priority tasks (they should be processed first due to timestamp)
        # But we use prefix to separate them
        for i in range(3):
            task_queue.create_run(f"/priority/high_{i}",
                                 data={"priority": "high"},
                                 status=RunStatus.PENDING)
        
        # Process high priority first
        high_priority = []
        for run in task_queue(prefix="/priority", wait=False):
            high_priority.append(run.path)
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        assert len(high_priority) == 3
        
        # Then process low priority
        low_priority = []
        for run in task_queue(prefix="/tasks", wait=False):
            low_priority.append(run.path)
            task_queue.set_status(run.path, RunStatus.COMPLETED)
        
        assert len(low_priority) == 5

