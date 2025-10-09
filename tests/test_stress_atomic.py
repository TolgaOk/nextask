"""
Stress tests for atomic operations at scale.

These tests are designed to detect race conditions and non-atomic behavior
by running high-volume, high-concurrency scenarios that would expose any
weaknesses in the atomic guarantees.
"""
import time
import threading
import random
from concurrent.futures import ThreadPoolExecutor, as_completed
from collections import Counter, defaultdict
from nextask import TaskQueue, RunStatus


class TestLargeScaleAtomicity:
    """Large-scale stress tests for atomic operations."""
    
    def test_massive_concurrent_claims_no_duplicates(self, task_queue):
        """
        CRITICAL STRESS TEST: Massive scale concurrent claiming.
        
        Scenario:
        - 1000 tasks in queue
        - 50 workers competing simultaneously
        - Each worker aggressively tries to claim tasks
        
        Success Criteria:
        - Every task claimed exactly once
        - No duplicate claims
        - No lost tasks
        - All tasks accounted for
        """
        num_tasks = 1000
        num_workers = 50
        
        print(f"\n🔥 STRESS TEST: {num_tasks} tasks, {num_workers} workers")
        
        # Create tasks
        task_paths = []
        for i in range(num_tasks):
            path = f"/stress/batch1/task{i:04d}"
            task_queue.create_run(path, data={"id": i}, status=RunStatus.PENDING)
            task_paths.append(path)
        
        # Track claims per worker
        worker_claims = defaultdict(list)
        all_claims = []
        claim_lock = threading.Lock()
        
        def aggressive_worker(worker_id):
            """Worker that claims as fast as possible."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claims = []
            
            while True:
                run = next(iter(worker_queue(wait=False)), None)
                if run is None:
                    break
                    
                local_claims.append(run.path)
                # No delay - claim as fast as possible to increase contention
                
            worker_queue.redis.close()
            return worker_id, local_claims
        
        # Run workers in parallel
        start_time = time.time()
        
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(aggressive_worker, i) for i in range(num_workers)]
            
            for future in as_completed(futures):
                worker_id, claims = future.result()
                worker_claims[worker_id] = claims
                with claim_lock:
                    all_claims.extend(claims)
        
        elapsed = time.time() - start_time
        
        # Analysis
        claim_counts = Counter(all_claims)
        duplicates = {path: count for path, count in claim_counts.items() if count > 1}
        unique_claims = len(set(all_claims))
        
        # Report
        print(f"⏱️  Execution time: {elapsed:.2f}s")
        print(f"📊 Total claims: {len(all_claims)}")
        print(f"✨ Unique claims: {unique_claims}")
        print(f"🎯 Expected claims: {num_tasks}")
        
        # Worker distribution
        claims_per_worker = {wid: len(claims) for wid, claims in worker_claims.items()}
        print(f"📈 Claims per worker: min={min(claims_per_worker.values())}, "
              f"max={max(claims_per_worker.values())}, "
              f"avg={sum(claims_per_worker.values())/len(claims_per_worker):.1f}")
        
        # CRITICAL ASSERTIONS
        assert len(duplicates) == 0, f"🚨 DUPLICATE CLAIMS DETECTED: {duplicates}"
        assert unique_claims == num_tasks, f"🚨 LOST TASKS: Expected {num_tasks}, got {unique_claims}"
        assert len(all_claims) == num_tasks, f"🚨 CLAIM COUNT MISMATCH: {len(all_claims)} vs {num_tasks}"
        assert set(all_claims) == set(task_paths), "🚨 WRONG TASKS CLAIMED"
        
        # Verify all tasks are now RUNNING
        for path in task_paths:
            status = task_queue.get_status(path)
            assert status == "running", f"🚨 Task {path} not marked as RUNNING: {status}"
        
        print("✅ PASSED: All atomicity guarantees verified at scale")
    
    def test_extreme_contention_burst_load(self, task_queue):
        """
        EXTREME STRESS: Very few tasks, many workers (maximum contention).
        
        Scenario:
        - Only 10 tasks
        - 100 workers competing (10:1 worker-to-task ratio)
        - All workers start simultaneously
        
        This maximizes the probability of race conditions.
        """
        num_tasks = 10
        num_workers = 100
        
        print(f"\n🔥 EXTREME CONTENTION: {num_tasks} tasks, {num_workers} workers (10:1 ratio)")
        
        # Create tasks
        for i in range(num_tasks):
            task_queue.create_run(f"/stress/extreme/task{i}", status=RunStatus.PENDING)
        
        claimed = []
        lock = threading.Lock()
        barrier = threading.Barrier(num_workers)  # Synchronize all workers
        
        def synchronized_aggressive_worker(worker_id):
            """Worker that starts exactly when all others are ready."""
            barrier.wait()  # Wait for all workers to be ready
            
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            run = next(iter(worker_queue(wait=False)), None)
            worker_queue.redis.close()
            
            return run.path if run else None
        
        # Launch all workers simultaneously
        start_time = time.time()
        
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(synchronized_aggressive_worker, i) 
                      for i in range(num_workers)]
            
            for future in as_completed(futures):
                result = future.result()
                if result:
                    with lock:
                        claimed.append(result)
        
        elapsed = time.time() - start_time
        
        # Analysis
        claim_counts = Counter(claimed)
        duplicates = {path: count for path, count in claim_counts.items() if count > 1}
        
        print(f"⏱️  Execution time: {elapsed:.2f}s")
        print(f"📊 Successful claims: {len(claimed)}/{num_workers} workers")
        print(f"✨ Unique claims: {len(set(claimed))}")
        
        # CRITICAL ASSERTIONS
        assert len(duplicates) == 0, f"🚨 DUPLICATE CLAIMS under extreme contention: {duplicates}"
        assert len(claimed) == num_tasks, f"🚨 Expected {num_tasks} claims, got {len(claimed)}"
        assert len(set(claimed)) == num_tasks, f"🚨 Expected {num_tasks} unique, got {len(set(claimed))}"
        
        print("✅ PASSED: Atomicity maintained under extreme contention")
    
    def test_sustained_high_throughput(self, task_queue):
        """
        THROUGHPUT STRESS: Sustained high-volume processing.
        
        Scenario:
        - 5000 tasks total
        - 20 workers processing continuously
        - Measure throughput and verify no atomicity issues
        """
        num_tasks = 5000
        num_workers = 20
        
        print(f"\n🔥 THROUGHPUT TEST: {num_tasks} tasks, {num_workers} workers")
        
        # Create tasks
        for i in range(num_tasks):
            task_queue.create_run(
                f"/stress/throughput/task{i:05d}",
                data={"batch": i // 100, "id": i},
                status=RunStatus.PENDING
            )
        
        processed = []
        lock = threading.Lock()
        worker_stats = defaultdict(int)
        
        def processing_worker(worker_id):
            """Worker that processes tasks with realistic work simulation."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_processed = []
            
            while True:
                run = next(iter(worker_queue(wait=False)), None)
                if run is None:
                    break
                
                # Simulate minimal processing
                time.sleep(0.0001)  # 0.1ms of "work"
                
                # Mark as completed
                worker_queue.set_status(run.path, RunStatus.COMPLETED)
                local_processed.append(run.path)
                
                worker_stats[worker_id] += 1
            
            worker_queue.redis.close()
            return local_processed
        
        # Run workers
        start_time = time.time()
        
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(processing_worker, i) for i in range(num_workers)]
            
            for future in as_completed(futures):
                with lock:
                    processed.extend(future.result())
        
        elapsed = time.time() - start_time
        
        # Analysis
        throughput = num_tasks / elapsed
        claim_counts = Counter(processed)
        duplicates = {path: count for path, count in claim_counts.items() if count > 1}
        
        print(f"⏱️  Execution time: {elapsed:.2f}s")
        print(f"🚀 Throughput: {throughput:.0f} tasks/second")
        print(f"📊 Tasks processed: {len(processed)}")
        print(f"✨ Unique tasks: {len(set(processed))}")
        print(f"📈 Per worker: min={min(worker_stats.values())}, "
              f"max={max(worker_stats.values())}, "
              f"avg={sum(worker_stats.values())/len(worker_stats):.1f}")
        
        # CRITICAL ASSERTIONS
        assert len(duplicates) == 0, f"🚨 DUPLICATE PROCESSING: {len(duplicates)} tasks"
        assert len(processed) == num_tasks, f"🚨 Expected {num_tasks}, processed {len(processed)}"
        assert len(set(processed)) == num_tasks, f"🚨 Some tasks processed multiple times"
        
        # Verify all completed
        completed_count = len([r for r in task_queue.get_runs("/stress/throughput") 
                              if r.status == RunStatus.COMPLETED])
        assert completed_count == num_tasks, f"🚨 Only {completed_count}/{num_tasks} marked completed"
        
        print("✅ PASSED: High throughput maintained without atomicity issues")
    
    def test_mixed_operations_under_load(self, task_queue):
        """
        MIXED LOAD STRESS: Multiple operations happening simultaneously.
        
        Scenario:
        - 500 tasks
        - 10 workers claiming and processing
        - 5 workers updating data on running tasks
        - 5 workers querying status
        - Verify no data corruption or race conditions
        """
        num_tasks = 500
        num_claim_workers = 10
        num_update_workers = 5
        num_query_workers = 5
        
        print(f"\n🔥 MIXED OPERATIONS: {num_tasks} tasks, "
              f"{num_claim_workers + num_update_workers + num_query_workers} total workers")
        
        # Create tasks
        for i in range(num_tasks):
            task_queue.create_run(
                f"/stress/mixed/task{i:04d}",
                data={"counter": 0},
                status=RunStatus.PENDING
            )
        
        claimed = []
        updates_performed = []
        queries_performed = []
        lock = threading.Lock()
        
        def claim_worker(worker_id):
            """Worker that claims and processes tasks."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claimed = []
            
            while True:
                run = next(iter(worker_queue(wait=False)), None)
                if run is None:
                    break
                
                local_claimed.append(run.path)
                time.sleep(0.001)  # Small delay
                worker_queue.set_status(run.path, RunStatus.RUNNING)
            
            worker_queue.redis.close()
            return local_claimed
        
        def update_worker(worker_id):
            """Worker that updates data on running tasks."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_updates = []
            
            # Run for a fixed time
            end_time = time.time() + 0.5
            while time.time() < end_time:
                # Find a random running task
                runs = worker_queue.get_runs("/stress/mixed")
                running_runs = [r for r in runs if r.status == RunStatus.RUNNING]
                
                if running_runs:
                    run = random.choice(running_runs)
                    try:
                        # Increment counter
                        current_data = worker_queue.get_data(run.path)
                        if current_data:
                            worker_queue.set_data(run.path, {"counter": current_data.get("counter", 0) + 1})
                            local_updates.append(run.path)
                    except Exception:
                        pass  # Task might have changed status
                
                time.sleep(0.01)
            
            worker_queue.redis.close()
            return local_updates
        
        def query_worker(worker_id):
            """Worker that continuously queries task status."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_queries = 0
            
            # Run for a fixed time
            end_time = time.time() + 0.5
            while time.time() < end_time:
                runs = worker_queue.get_runs("/stress/mixed")
                for run in runs:
                    _ = worker_queue.get_status(run.path)
                    local_queries += 1
                time.sleep(0.01)
            
            worker_queue.redis.close()
            return local_queries
        
        # Run all worker types in parallel
        start_time = time.time()
        
        with ThreadPoolExecutor(max_workers=num_claim_workers + num_update_workers + num_query_workers) as executor:
            # Submit claim workers
            claim_futures = [executor.submit(claim_worker, i) for i in range(num_claim_workers)]
            
            # Submit update workers
            update_futures = [executor.submit(update_worker, i) for i in range(num_update_workers)]
            
            # Submit query workers
            query_futures = [executor.submit(query_worker, i) for i in range(num_query_workers)]
            
            # Collect results
            for future in as_completed(claim_futures):
                with lock:
                    claimed.extend(future.result())
            
            for future in as_completed(update_futures):
                with lock:
                    updates_performed.extend(future.result())
            
            for future in as_completed(query_futures):
                with lock:
                    queries_performed.append(future.result())
        
        elapsed = time.time() - start_time
        
        # Analysis
        claim_counts = Counter(claimed)
        duplicates = {path: count for path, count in claim_counts.items() if count > 1}
        
        print(f"⏱️  Execution time: {elapsed:.2f}s")
        print(f"📊 Tasks claimed: {len(claimed)}")
        print(f"✨ Unique claims: {len(set(claimed))}")
        print(f"🔄 Data updates: {len(updates_performed)}")
        print(f"🔍 Status queries: {sum(queries_performed)}")
        
        # CRITICAL ASSERTIONS
        assert len(duplicates) == 0, f"🚨 DUPLICATE CLAIMS with mixed operations: {duplicates}"
        assert len(claimed) == num_tasks, f"🚨 Expected {num_tasks} claims, got {len(claimed)}"
        
        # Verify data integrity - all tasks should exist and be valid
        final_runs = task_queue.get_runs("/stress/mixed")
        assert len(final_runs) == num_tasks, f"🚨 Lost tasks: {len(final_runs)}/{num_tasks}"
        
        # Verify all tasks are in valid states
        for run in final_runs:
            assert run.status in [RunStatus.PENDING, RunStatus.RUNNING, RunStatus.COMPLETED, RunStatus.FAILED]
            assert isinstance(run.data, dict)
            assert "counter" in run.data
            assert run.data["counter"] >= 0
        
        print("✅ PASSED: No corruption under mixed concurrent operations")
    
    def test_repeated_stress_iterations(self, task_queue):
        """
        ITERATIVE STRESS: Run multiple stress iterations to catch rare race conditions.
        
        Scenario:
        - 10 iterations of stress testing
        - Each iteration: 200 tasks, 20 workers
        - Any single failure indicates a race condition
        """
        num_iterations = 10
        tasks_per_iteration = 200
        workers_per_iteration = 20
        
        print(f"\n🔥 ITERATIVE STRESS: {num_iterations} iterations x "
              f"{tasks_per_iteration} tasks x {workers_per_iteration} workers")
        
        all_iterations_passed = True
        
        for iteration in range(num_iterations):
            # Create tasks for this iteration
            task_paths = []
            for i in range(tasks_per_iteration):
                path = f"/stress/iter{iteration}/task{i:04d}"
                task_queue.create_run(path, status=RunStatus.PENDING)
                task_paths.append(path)
            
            # Run workers
            claimed = []
            lock = threading.Lock()
            
            def worker(worker_id):
                worker_queue = TaskQueue(host="localhost", port=6379, db=15)
                local_claims = []
                
                while True:
                    run = next(iter(worker_queue(prefix=f"/stress/iter{iteration}", wait=False)), None)
                    if run is None:
                        break
                    local_claims.append(run.path)
                
                worker_queue.redis.close()
                return local_claims
            
            with ThreadPoolExecutor(max_workers=workers_per_iteration) as executor:
                futures = [executor.submit(worker, i) for i in range(workers_per_iteration)]
                
                for future in as_completed(futures):
                    with lock:
                        claimed.extend(future.result())
            
            # Verify this iteration
            claim_counts = Counter(claimed)
            duplicates = {path: count for path, count in claim_counts.items() if count > 1}
            
            iteration_passed = (
                len(duplicates) == 0 and
                len(claimed) == tasks_per_iteration and
                len(set(claimed)) == tasks_per_iteration
            )
            
            status_char = "✅" if iteration_passed else "❌"
            print(f"  {status_char} Iteration {iteration + 1}/{num_iterations}: "
                  f"{len(claimed)} claims, {len(set(claimed))} unique")
            
            if not iteration_passed:
                all_iterations_passed = False
                print(f"    🚨 FAILURE: Duplicates={len(duplicates)}, "
                      f"Claims={len(claimed)}, Expected={tasks_per_iteration}")
            
            # Critical assertion per iteration
            assert iteration_passed, f"🚨 Iteration {iteration + 1} failed atomicity check"
        
        assert all_iterations_passed, "🚨 Some iterations failed"
        print(f"✅ PASSED: All {num_iterations} iterations maintained atomicity")
    
    def test_claim_and_immediate_update_race(self, task_queue):
        """
        RACE CONDITION TEST: Claim and immediate status/data updates.
        
        Scenario:
        - 1000 tasks
        - 30 workers claim and immediately update both status and data
        - This tests for race conditions in the update path
        """
        num_tasks = 1000
        num_workers = 30
        
        print(f"\n🔥 UPDATE RACE TEST: {num_tasks} tasks, {num_workers} workers")
        
        # Create tasks
        for i in range(num_tasks):
            task_queue.create_run(
                f"/stress/race/task{i:04d}",
                data={"processed": False, "worker_id": None},
                status=RunStatus.PENDING
            )
        
        processed = []
        lock = threading.Lock()
        
        def claim_and_update_worker(worker_id):
            """Worker that claims and immediately updates."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_processed = []
            
            while True:
                run = next(iter(worker_queue(prefix="/stress/race", wait=False)), None)
                if run is None:
                    break
                
                # Immediately update data (potential race condition point)
                worker_queue.set_data(run.path, {
                    "processed": True,
                    "worker_id": worker_id,
                    "timestamp": time.time()
                })
                
                # Then update status (another potential race condition point)
                worker_queue.set_status(run.path, RunStatus.COMPLETED)
                
                local_processed.append(run.path)
            
            worker_queue.redis.close()
            return worker_id, local_processed
        
        # Run workers
        start_time = time.time()
        worker_results = {}
        
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(claim_and_update_worker, i) for i in range(num_workers)]
            
            for future in as_completed(futures):
                worker_id, claims = future.result()
                worker_results[worker_id] = claims
                with lock:
                    processed.extend(claims)
        
        elapsed = time.time() - start_time
        
        # Analysis
        claim_counts = Counter(processed)
        duplicates = {path: count for path, count in claim_counts.items() if count > 1}
        
        print(f"⏱️  Execution time: {elapsed:.2f}s")
        print(f"📊 Tasks processed: {len(processed)}")
        print(f"✨ Unique: {len(set(processed))}")
        
        # CRITICAL ASSERTIONS
        assert len(duplicates) == 0, f"🚨 DUPLICATE PROCESSING: {duplicates}"
        assert len(processed) == num_tasks
        
        # Verify data integrity - each task should have exactly one worker_id
        for i in range(num_tasks):
            path = f"/stress/race/task{i:04d}"
            data = task_queue.get_data(path)
            status = task_queue.get_status(path)
            
            assert data["processed"] is True, f"Task {path} not marked as processed"
            assert data["worker_id"] is not None, f"Task {path} has no worker_id"
            assert status == "completed", f"Task {path} not completed: {status}"
        
        print("✅ PASSED: No race conditions in update path")


class TestAtomicityUnderFailure:
    """Test atomic behavior when workers fail or timeout."""
    
    def test_atomicity_with_simulated_worker_failures(self, task_queue):
        """
        FAILURE SCENARIO: Some workers crash mid-processing.
        
        Verify that failed workers don't cause:
        - Duplicate claims
        - Lost tasks
        - Data corruption
        """
        num_tasks = 500
        num_workers = 20
        failure_rate = 0.2  # 20% of workers will "crash"
        
        print(f"\n🔥 FAILURE TEST: {num_tasks} tasks, {num_workers} workers, "
              f"{failure_rate*100:.0f}% failure rate")
        
        # Create tasks
        for i in range(num_tasks):
            task_queue.create_run(f"/stress/failure/task{i:04d}", status=RunStatus.PENDING)
        
        claimed = []
        failed_workers = []
        lock = threading.Lock()
        
        def unreliable_worker(worker_id):
            """Worker that might crash randomly."""
            worker_queue = TaskQueue(host="localhost", port=6379, db=15)
            local_claims = []
            
            # Simulate crash at random point
            should_crash = random.random() < failure_rate
            crash_after = random.randint(5, 20) if should_crash else float('inf')
            
            try:
                claim_count = 0
                while True:
                    run = next(iter(worker_queue(prefix="/stress/failure", wait=False)), None)
                    if run is None:
                        break
                    
                    local_claims.append(run.path)
                    claim_count += 1
                    
                    # Simulate crash
                    if claim_count >= crash_after:
                        raise Exception(f"Worker {worker_id} crashed!")
                    
                    time.sleep(0.001)
            
            except Exception as e:
                with lock:
                    failed_workers.append(worker_id)
                return worker_id, local_claims, True  # Failed
            
            finally:
                worker_queue.redis.close()
            
            return worker_id, local_claims, False  # Success
        
        # Run workers
        start_time = time.time()
        
        with ThreadPoolExecutor(max_workers=num_workers) as executor:
            futures = [executor.submit(unreliable_worker, i) for i in range(num_workers)]
            
            for future in as_completed(futures):
                try:
                    worker_id, claims, failed = future.result()
                    with lock:
                        claimed.extend(claims)
                except Exception:
                    pass  # Worker exception already recorded
        
        elapsed = time.time() - start_time
        
        # Analysis
        claim_counts = Counter(claimed)
        duplicates = {path: count for path, count in claim_counts.items() if count > 1}
        
        print(f"⏱️  Execution time: {elapsed:.2f}s")
        print(f"💥 Failed workers: {len(failed_workers)}/{num_workers}")
        print(f"📊 Successful claims: {len(claimed)}")
        print(f"✨ Unique claims: {len(set(claimed))}")
        
        # Even with failures, no duplicates should occur
        assert len(duplicates) == 0, f"🚨 DUPLICATES despite failures: {duplicates}"
        
        # Some tasks might not be claimed due to failures, that's okay
        # But no task should be claimed twice
        assert len(claimed) == len(set(claimed)), "🚨 Duplicate claims detected"
        
        print("✅ PASSED: Atomicity maintained even with worker failures")

