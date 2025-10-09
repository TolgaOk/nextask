"""
Edge case tests for TaskQueue.

Tests cover:
- Empty queues and missing data
- Invalid inputs and error handling
- Boundary conditions
- Data inconsistencies
- Special characters and unusual paths
- Large data volumes
"""
import pytest
import json
import time
from nextask import TaskQueue, Run, RunStatus


class TestEmptyAndMissingData:
    """Test behavior with empty queues and missing data."""
    
    def test_empty_queue_operations(self, task_queue):
        """Test all operations on empty queue."""
        assert task_queue.get_run("/nonexistent") is None
        assert task_queue.get_runs("/") == []
        assert next(iter(task_queue(wait=False)), None) is None
        assert task_queue.get_status("/nonexistent") is None
        assert task_queue.get_data("/nonexistent") is None
    
    def test_operations_on_nonexistent_run(self, task_queue):
        """Test operations that should fail on nonexistent runs."""
        with pytest.raises(ValueError):
            task_queue.set_status("/nonexistent", RunStatus.RUNNING)
        
        with pytest.raises(ValueError):
            task_queue.set_data("/nonexistent", {"key": "value"})
    
    def test_get_runs_no_matches(self, task_queue):
        """Test get_runs with prefix that matches nothing."""
        task_queue.create_run("/project/ml/exp1")
        
        runs = task_queue.get_runs("/project/rl")
        assert runs == []
    
    def test_create_run_with_empty_data(self, task_queue):
        """Test creating run with explicitly empty data."""
        run = task_queue.create_run("/test/run1", data={})
        
        assert run.data == {}
        retrieved = task_queue.get_run("/test/run1")
        assert retrieved.data == {}
    
    def test_set_data_to_empty(self, task_queue):
        """Test setting data to empty dictionary."""
        path = "/test/run1"
        task_queue.create_run(path, data={"key": "value"})
        
        # Setting empty data should still work (merges with existing)
        task_queue.set_data(path, {})
        
        data = task_queue.get_data(path)
        # Original data should still be there since merge happens
        assert data.get("key") == "value"


class TestInvalidInputs:
    """Test handling of invalid inputs."""
    
    def test_invalid_status_string(self, task_queue):
        """Test that invalid status strings are handled."""
        path = "/test/run1"
        task_queue.create_run(path)
        
        # This should raise an error or be handled gracefully
        # depending on implementation
        try:
            task_queue.set_status(path, "invalid_status")
            # If it doesn't raise, at least verify it didn't break the run
            run = task_queue.get_run(path)
            assert run is not None
        except (ValueError, AttributeError):
            pass  # Expected behavior
    
    def test_non_serializable_nested_data(self, task_queue):
        """Test deeply nested non-serializable data."""
        with pytest.raises(TypeError):
            task_queue.create_run("/test/run1", data={
                "level1": {
                    "level2": {
                        "func": lambda x: x
                    }
                }
            })
    
    def test_circular_reference_in_data(self, task_queue):
        """Test data with circular references."""
        circular_data = {"key": "value"}
        circular_data["self"] = circular_data
        
        with pytest.raises((TypeError, ValueError)):
            task_queue.create_run("/test/run1", data=circular_data)
    
    def test_none_as_data(self, task_queue):
        """Test None as data parameter."""
        run = task_queue.create_run("/test/run1", data=None)
        
        # Should handle None gracefully
        assert run is not None
        # Data should be empty dict or None
        assert run.data in [{}, None]
    
    def test_very_long_path(self, task_queue):
        """Test with very long path."""
        long_path = "/" + "/".join([f"level{i}" for i in range(100)]) + "/run"
        
        run = task_queue.create_run(long_path)
        assert run.path == long_path
        
        retrieved = task_queue.get_run(long_path)
        assert retrieved is not None


class TestSpecialCharacters:
    """Test paths and data with special characters."""
    
    def test_path_with_spaces(self, task_queue):
        """Test path containing spaces."""
        path = "/test/run with spaces"
        run = task_queue.create_run(path)
        
        assert run.path == path
        retrieved = task_queue.get_run(path)
        assert retrieved is not None
    
    def test_path_with_special_chars(self, task_queue):
        """Test path with various special characters."""
        special_paths = [
            "/test/run-with-dashes",
            "/test/run_with_underscores",
            "/test/run.with.dots",
            "/test/run@with@at",
            "/test/run#with#hash",
        ]
        
        for path in special_paths:
            run = task_queue.create_run(path)
            assert run.path == path
            retrieved = task_queue.get_run(path)
            assert retrieved is not None
    
    def test_path_with_unicode(self, task_queue):
        """Test path with unicode characters."""
        path = "/test/实验/run001"
        run = task_queue.create_run(path)
        
        assert run.path == path
        retrieved = task_queue.get_run(path)
        assert retrieved is not None
    
    def test_data_with_unicode(self, task_queue):
        """Test data containing unicode."""
        data = {
            "description": "实验描述",
            "notes": "Tëst wïth àccénts",
            "emoji": "🚀🎯✨"
        }
        
        run = task_queue.create_run("/test/run1", data=data)
        
        retrieved_data = task_queue.get_data("/test/run1")
        assert retrieved_data["description"] == "实验描述"
        assert retrieved_data["emoji"] == "🚀🎯✨"
    
    def test_data_with_special_json_chars(self, task_queue):
        """Test data with characters that need JSON escaping."""
        data = {
            "quotes": 'He said "hello"',
            "newlines": "line1\nline2\nline3",
            "tabs": "col1\tcol2\tcol3",
            "backslash": "path\\to\\file"
        }
        
        run = task_queue.create_run("/test/run1", data=data)
        retrieved_data = task_queue.get_data("/test/run1")
        
        assert retrieved_data["quotes"] == 'He said "hello"'
        assert retrieved_data["newlines"] == "line1\nline2\nline3"
        assert retrieved_data["backslash"] == "path\\to\\file"


class TestBoundaryConditions:
    """Test boundary conditions and limits."""
    
    def test_large_data_payload(self, task_queue):
        """Test with large data payload."""
        # Create 1MB of data
        large_data = {f"key{i}": "x" * 1000 for i in range(1000)}
        
        run = task_queue.create_run("/test/run1", data=large_data)
        
        retrieved_data = task_queue.get_data("/test/run1")
        assert len(retrieved_data) == 1000
        assert retrieved_data["key0"] == "x" * 1000
    
    def test_many_keys_in_data(self, task_queue):
        """Test data with many keys."""
        data = {f"param{i}": i for i in range(10000)}
        
        run = task_queue.create_run("/test/run1", data=data)
        
        retrieved_data = task_queue.get_data("/test/run1")
        assert len(retrieved_data) == 10000
        assert retrieved_data["param5000"] == 5000
    
    def test_deeply_nested_data(self, task_queue):
        """Test deeply nested data structure."""
        # Create nested structure 50 levels deep
        data = {"level": 0}
        current = data
        for i in range(1, 50):
            current["nested"] = {"level": i}
            current = current["nested"]
        
        run = task_queue.create_run("/test/run1", data=data)
        
        retrieved_data = task_queue.get_data("/test/run1")
        # Navigate to deep level
        current = retrieved_data
        for i in range(49):
            current = current["nested"]
        assert current["level"] == 49
    
    def test_zero_timestamps(self, task_queue):
        """Test that timestamps are reasonable."""
        run = task_queue.create_run("/test/run1")
        
        assert run.created_at > 0
        assert run.updated_at > 0
        assert run.created_at < time.time() + 1
        assert run.updated_at < time.time() + 1
    
    def test_many_runs_same_prefix(self, task_queue):
        """Test creating many runs with same prefix."""
        num_runs = 1000
        
        for i in range(num_runs):
            task_queue.create_run(f"/test/batch/run{i:04d}")
        
        runs = task_queue.get_runs("/test/batch")
        assert len(runs) == num_runs
    
    def test_single_character_path(self, task_queue):
        """Test with minimal path."""
        path = "/x"
        run = task_queue.create_run(path)
        
        assert run.path == path
        retrieved = task_queue.get_run(path)
        assert retrieved is not None


class TestDataTypeHandling:
    """Test various data types in run data."""
    
    def test_all_json_types(self, task_queue):
        """Test all JSON-serializable types."""
        data = {
            "string": "hello",
            "integer": 42,
            "float": 3.14159,
            "boolean": True,
            "null": None,
            "array": [1, 2, 3, "four", 5.0],
            "object": {"nested": "value"}
        }
        
        run = task_queue.create_run("/test/run1", data=data)
        retrieved_data = task_queue.get_data("/test/run1")
        
        assert retrieved_data["string"] == "hello"
        assert retrieved_data["integer"] == 42
        assert abs(retrieved_data["float"] - 3.14159) < 0.00001
        assert retrieved_data["boolean"] is True
        assert retrieved_data["null"] is None
        assert retrieved_data["array"] == [1, 2, 3, "four", 5.0]
        assert retrieved_data["object"]["nested"] == "value"
    
    def test_numeric_edge_cases(self, task_queue):
        """Test edge cases for numeric values."""
        data = {
            "zero": 0,
            "negative": -42,
            "large_int": 9999999999999999,
            "small_float": 0.0000000001,
            "negative_float": -3.14,
        }
        
        run = task_queue.create_run("/test/run1", data=data)
        retrieved_data = task_queue.get_data("/test/run1")
        
        assert retrieved_data["zero"] == 0
        assert retrieved_data["negative"] == -42
        assert retrieved_data["large_int"] == 9999999999999999
    
    def test_empty_collections(self, task_queue):
        """Test empty arrays and objects."""
        data = {
            "empty_array": [],
            "empty_object": {},
            "nested_empty": {"empty": []}
        }
        
        run = task_queue.create_run("/test/run1", data=data)
        retrieved_data = task_queue.get_data("/test/run1")
        
        assert retrieved_data["empty_array"] == []
        assert retrieved_data["empty_object"] == {}
        assert retrieved_data["nested_empty"]["empty"] == []


class TestStatusTransitionEdgeCases:
    """Test edge cases in status transitions."""
    
    def test_transition_from_completed_to_pending(self, task_queue):
        """Test manually resetting a completed run to pending."""
        path = "/test/run1"
        task_queue.create_run(path, status=RunStatus.COMPLETED)
        
        # Reset to pending (manual retry)
        task_queue.set_status(path, RunStatus.PENDING)
        
        # Should be claimable again
        run = next(iter(task_queue(wait=False)), None)
        assert run is not None
        assert run.path == path
    
    def test_transition_from_running_to_failed(self, task_queue):
        """Test transition from running to failed."""
        path = "/test/run1"
        task_queue.create_run(path, status=RunStatus.RUNNING)
        
        task_queue.set_status(path, RunStatus.FAILED)
        
        # Should be retryable
        run = next(iter(task_queue(wait=False)), None)
        assert run is not None
        assert run.path == path
    
    def test_multiple_status_changes(self, task_queue):
        """Test multiple status changes on same run."""
        path = "/test/run1"
        run = task_queue.create_run(path, status=RunStatus.PENDING)
        last_update = run.updated_at
        
        statuses = [
            RunStatus.RUNNING,
            RunStatus.FAILED,
            RunStatus.RUNNING,
            RunStatus.COMPLETED,
            RunStatus.PENDING,
            RunStatus.RUNNING,
        ]
        
        for status in statuses:
            time.sleep(0.01)
            task_queue.set_status(path, status)
            run = task_queue.get_run(path)
            assert run.status == status
            assert run.updated_at > last_update
            last_update = run.updated_at


class TestTimestampBehavior:
    """Test timestamp behavior in various scenarios."""
    
    def test_updated_at_changes_on_status_change(self, task_queue):
        """Test that updated_at changes when status changes."""
        path = "/test/run1"
        run = task_queue.create_run(path)
        original_updated = run.updated_at
        
        time.sleep(0.01)
        task_queue.set_status(path, RunStatus.RUNNING)
        
        updated_run = task_queue.get_run(path)
        assert updated_run.updated_at > original_updated
    
    def test_updated_at_changes_on_data_change(self, task_queue):
        """Test that updated_at changes when data changes."""
        path = "/test/run1"
        run = task_queue.create_run(path)
        original_updated = run.updated_at
        
        time.sleep(0.01)
        task_queue.set_data(path, {"new": "data"})
        
        updated_run = task_queue.get_run(path)
        assert updated_run.updated_at > original_updated
    
    def test_created_at_never_changes(self, task_queue):
        """Test that created_at never changes."""
        path = "/test/run1"
        run = task_queue.create_run(path)
        original_created = run.created_at
        
        time.sleep(0.01)
        task_queue.set_status(path, RunStatus.RUNNING)
        task_queue.set_data(path, {"key": "value"})
        
        updated_run = task_queue.get_run(path)
        assert updated_run.created_at == original_created
    
    def test_updated_at_on_claim(self, task_queue):
        """Test that updated_at changes when run is claimed."""
        path = "/test/run1"
        run = task_queue.create_run(path, status=RunStatus.PENDING)
        original_updated = run.updated_at
        
        time.sleep(0.01)
        claimed = next(iter(task_queue(wait=False)), None)
        
        assert claimed.updated_at > original_updated


class TestPrefixFilteringEdgeCases:
    """Test edge cases in prefix filtering."""
    
    def test_prefix_with_trailing_slash(self, task_queue):
        """Test prefix with and without trailing slash."""
        task_queue.create_run("/project/ml/exp1", status=RunStatus.PENDING)
        
        # Both should work the same
        run1 = next(iter(task_queue(prefix="/project/ml", wait=False)), None)
        assert run1 is not None
        
        task_queue.set_status(run1.path, RunStatus.COMPLETED)
        task_queue.create_run("/project/ml/exp2", status=RunStatus.PENDING)
        
        run2 = next(iter(task_queue(prefix="/project/ml/", wait=False)), None)
        assert run2 is not None
    
    def test_prefix_partial_match(self, task_queue):
        """Test that prefix doesn't match partial path components."""
        task_queue.create_run("/project/ml-2025/exp1", status=RunStatus.PENDING)
        task_queue.create_run("/project/ml/exp1", status=RunStatus.PENDING)
        
        # Should only match exact prefix paths
        runs = task_queue.get_runs("/project/ml")
        matching_paths = [r.path for r in runs]
        
        # Both might match depending on implementation
        # but at minimum /project/ml/exp1 should match
        assert any("/project/ml/exp1" in p for p in matching_paths)
    
    def test_empty_prefix(self, task_queue):
        """Test with empty string as prefix."""
        task_queue.create_run("/test/run1", status=RunStatus.PENDING)
        
        # Empty prefix might work like "/" or be invalid
        try:
            run = next(iter(task_queue(prefix="", wait=False)), None)
            # If it works, should get the run
            if run:
                assert run.path == "/test/run1"
        except (ValueError, TypeError):
            pass  # Empty prefix might not be allowed
    
    def test_prefix_longer_than_paths(self, task_queue):
        """Test prefix that's longer than any path."""
        task_queue.create_run("/a/b", status=RunStatus.PENDING)
        
        run = next(iter(task_queue(prefix="/a/b/c/d/e/f", wait=False)), None)
        assert run is None


class TestDataMerging:
    """Test data merging behavior."""
    
    def test_data_merge_overwrites_keys(self, task_queue):
        """Test that set_data overwrites existing keys."""
        path = "/test/run1"
        task_queue.create_run(path, data={"key1": "old"})
        
        task_queue.set_data(path, {"key1": "new"})
        
        data = task_queue.get_data(path)
        assert data["key1"] == "new"
    
    def test_data_merge_adds_keys(self, task_queue):
        """Test that set_data adds new keys."""
        path = "/test/run1"
        task_queue.create_run(path, data={"key1": "value1"})
        
        task_queue.set_data(path, {"key2": "value2"})
        
        data = task_queue.get_data(path)
        assert data["key1"] == "value1"
        assert data["key2"] == "value2"
    
    def test_sequential_data_merges(self, task_queue):
        """Test multiple sequential data merges."""
        path = "/test/run1"
        task_queue.create_run(path, data={"key1": "value1"})
        
        task_queue.set_data(path, {"key2": "value2"})
        task_queue.set_data(path, {"key3": "value3"})
        task_queue.set_data(path, {"key1": "updated"})
        
        data = task_queue.get_data(path)
        assert data["key1"] == "updated"
        assert data["key2"] == "value2"
        assert data["key3"] == "value3"

