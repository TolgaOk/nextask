---
name: nextask-setup-worker
description: Set up or reconfigure a Nextask worker on a local machine, remote server, or container.
---

Use the chosen machine and existing configuration; ask only for missing deployment details.
Check `nextask --version` and `nextask config show --sources` before changing the setup.

## Guide the user

Prompt at each unresolved setup decision, one question at a time, with brief choices and a recommendation when useful.
Reuse answers already given; do not repeat the whole questionnaire.

- Where should the worker run: this machine, an existing remote host, or a cloud instance?
- How should project dependencies be provided: an existing environment or a container?
- How many workers should run, and should they stay running or exit after work?
- Where should task files be kept, and should they be removed after completion?

Ask how to supply any missing credentials; have the user set them through their environment or secret store rather than paste them into chat.
Wait for answers before taking dependent actions; continue read-only checks meanwhile.
Summarize the chosen setup before installing or starting anything, and confirm choices not already approved.

## Prepare and start

Reuse the user's chosen virtual environment, container, or service manager.
If Nextask is missing, use the linked installation instructions and check that its version supports the required integrations.
Run `nextask list` on the worker host to verify database access before starting it.

Choose the appropriate mode:

```sh
nextask worker                                  # foreground
nextask worker --daemon --workdir ~/nextask-work  # background
nextask worker --once --rm                       # at most one task, then clean up
nextask worker --tag gpu=a100 --exit-if-idle 5m   # only matching tasks
```

For a virtual environment, activate it before starting the worker.
For example, `. /opt/project/.venv/bin/activate && nextask worker` makes those dependencies available to task commands.
For SSH, run the setup on the remote host and load its own environment before `nextask worker --daemon`.
For containers, include Nextask and project dependencies in the image, pass credentials at startup, and mount persistent storage at the configured workdir.
Do not bake credentials into the image.

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

## Verify and diagnose

From the submitting machine:

```sh
nextask worker list --status running
nextask enqueue 'hostname' --tag gpu=a100 --attach
```

Use the configured worker tag in the check, or omit it for an unfiltered worker.
An unfiltered worker could also pick up that task; use `nextask show TASK_ID` to confirm which worker ran it.
If Git is enabled, also verify a snapshot task can restore its files.
If S3 is enabled, produce a small selected artifact and confirm it can be fetched.

- Worker startup fails: check config and DB access from the actual host or container; its `localhost` is not another machine.
- Tasks stay pending: compare task tags with the worker's `--tag` filter.
- Commands cannot find dependencies: check the worker's environment and task directory, not the submitting machine's shell.

Report the worker ID, how it is started, and where its workdir and configuration live.

See [installation](https://github.com/TolgaOk/nextask#install), [configuration](https://github.com/TolgaOk/nextask/blob/main/doc/configuration.md), and [worker options](https://github.com/TolgaOk/nextask/blob/main/doc/cli.md#workers) as needed.
