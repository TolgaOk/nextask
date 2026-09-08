# Logs and waiting

```sh
nextask log TASK_ID --tail 20 --attach  # recent lines, then live output
nextask wait task-a task-b             # wait for both
nextask wait task-a task-b --any       # return when either finishes
nextask wait --tag batch=export        # wait for matching tasks
nextask wait task-a --timeout 30s      # stop waiting after 30 seconds
```

- `wait` returns the first failure code it sees, after all selected tasks finish.
- `wait --any` returns the first finished task's code, including tasks already finished. Other tasks keep running.
- Waiting by tag includes matching tasks added while waiting. It ends when all selected tasks finish, or one with `--any`.
- A timeout returns code `124`. Missing tasks and workers that stop reporting also cause an error.
- `log --attach` shows output without returning the task's exit code. `enqueue --attach` returns that code.

## Ctrl+C

- With `wait` or `log --attach`, it stops watching. The task keeps running.
- With `enqueue --attach`, it requests cancellation and waits for the result. Press again to exit.
- After `cancel` or `worker stop` sends its request, Ctrl+C stops waiting for confirmation. The request remains in effect.
