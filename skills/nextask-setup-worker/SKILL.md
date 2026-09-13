---
name: nextask-setup-worker
description: Set up or reconfigure a Nextask worker on a local machine, remote server, or container.
---

Use the chosen machine and existing configuration; ask only for missing deployment details.
Check `nextask --version` and `nextask config show --sources` before changing the setup.

## Critical points

- The worker needs a reachable PostgreSQL database and the software required by task commands.
  Start it in the intended virtual environment or container so tasks inherit that environment.
  Git tasks also require Git and access to the snapshot remote.
- Supply DB credentials and any referenced Git/S3 variables on the worker itself.
  Queued tasks carry connection templates, not the submitter's secrets.
  Nextask does not load `.env` files automatically; export variables or load them through the service manager.
- Tasks start in fresh directories; starting a worker in a project folder does not copy that project into tasks.
- Each worker runs one task at a time.
  Use `worker --tag KEY=VALUE` to restrict which tasks it claims; unfiltered workers may claim any task.
- Use a persistent `--workdir` for saved results to survive reboot, and a separate workdir per database.
  `--rm` removes finished task directories, including local logs.
  Restarting with the same workdir can restore saved completion records, but does not resume interrupted commands.
- Stopping a worker interrupts its current task.
  `--exit-if-idle` stops the worker process, not its cloud instance or billing.

Verify with `nextask worker list` and a harmless task such as `nextask enqueue 'hostname' --attach`.
Match the worker's tag filter when submitting that check.
If Git is enabled, also verify a snapshot task can restore its files.

See [installation](https://github.com/TolgaOk/nextask#install), [configuration](https://github.com/TolgaOk/nextask/blob/main/doc/configuration.md), and [worker options](https://github.com/TolgaOk/nextask/blob/main/doc/cli.md#workers) as needed.
