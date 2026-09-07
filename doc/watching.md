# Watching tasks

```sh
nextask wait task-a task-b              # wait for both
nextask wait task-a task-b --any        # return after one finishes
nextask wait --tag batch=export --any
nextask wait task-a --timeout 30s       # exit 124 on timeout
```

Default `wait` waits for every selected task and retains the first observed nonzero
exit code. `--any` returns the first observed terminal result, including a task
already finished successfully. Other tasks keep running. Repeated IDs print once.
Missing tasks and stale workers produce nonzero results.

Tag selection includes tasks discovered while waiting. Waiting ends once its
completion condition holds; it does not wait for future tasks after that point.

`wait`, `log --attach`, `enqueue --attach`, `cancel`, and `worker stop` use database
state to confirm completion. Notifications prompt immediate rechecks; a one-second
poll covers missed notifications. Temporary read failures retry using configured
retry intervals. Permanent read errors are reported. Listener cleanup has its own
five-second deadline.

Ctrl+C detaches from `wait` and `log --attach`. For `enqueue --attach`, it requests
task cancellation and waits for a running task's final result. A second interrupt
exits. Viewing logs does not propagate the task's exit code; attached enqueue does.
Interrupting `cancel` leaves the cancellation request in place.
