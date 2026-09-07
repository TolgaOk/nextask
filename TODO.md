# 0.2.0 TODO

Nextask runs commands. Built-in integrations prepare tasks and compose runtime wrappers. Standalone tools remain independent.

## Nextask

- [x] Unify worker validation and daemon options; preserve `--rm` and `--exit-if-idle`.
- [x] Use typed DB errors; preserve causes and classify network failures correctly.
- [ ] Route CLI output through command writers; report rendering failures.
- [ ] Move enqueue SQL into a named DB operation; preserve transaction ordering.
- [ ] Split worker commands and DB queries by responsibility; separate queries from migrations.

- [x] Share Listener/Notifier recovery; test reconnects, channel quoting, and shutdown.
- [x] Serialize tag filters as JSON; preserve literal keys and values in list/count/claim.
- [x] Fix `wait --any`; test existing/live results, tags, stale tasks, and missed notifications.
- [x] Standardize CLI watching, cancellation contexts, retries, and bounded cleanup.
- [x] Isolate command construction, flags, and config; test concurrent command instances.

- [x] Test real workloads, outages, and hard kills; fix retry races, workdir reuse, and log loss.
- [x] Fix worker status filters: exclude stale from `running`; add `--status stale`.
- [x] Journal finished results; replay before new claims; test SIGKILL, reused IDs, and `--rm`.
- [x] Handle stop throughout the worker lifecycle; classify claim errors; clean up failed startup.
- [x] Recover completion/cancellation; report log-write failures; honor retry settings; close failed DB pools.

- [x] Add caller IDs, task environment, shared config, and redacted config diagnostics.
- [x] Require `NEXTASK_DB_URL`; reject secret config/URLs and name missing DB/S3 variables.
- [x] Add `--with`, `--set`, options-only config, and a common integration registry/prepare operation.
- [x] Keep execution generic, compose cleanup deadlines, and make external deletion explicit.
- [x] Test selection, config precedence, quoting, composition, cancellation, and failures.
- [x] Fix cancellation/channel handling; verify race tests and CLI workflows; add CI.

## Git integration

- [x] Snapshot/push during enqueue; restore the exact commit through the queued command.
- [x] Isolate Git writes from the local repository; keep remote branches `<project>/<TASK_ID>`.
- [x] Migrate legacy Git flags, config, and queued tasks.
- [x] Test repository preservation, restoration, failures, and cancellation.

## S3 integration

Agreed CLI, config, and defaults: [S3 design](doc/s3.md).

- [x] Add opt-in `--with s3` using `minio-go/v7` for S3-compatible providers, including Hetzner.
- [x] Support typed integration options and JSON-array `--set` overrides; config never enables integrations.
- [x] Read worker credentials from `S3_ACCESS_KEY` / `S3_SECRET_KEY`; require endpoint and destination.
- [x] Add explicit file filters, final-only inclusions, and uploads under `<remote>/<TASK_ID>/`.
- [x] Implement periodic uploads, final sync, transfer limits/retries, and configurable final-error handling.
- [x] Allow bounded final sync on cancellation; preserve task errors and stored objects.
- [x] Test filters, overrides, changed files, failures, cancellation, retention, and Git composition.
- [x] Verify live Hetzner uploads/readback, multipart, cancellation, and test-resource cleanup.

## Later

- [ ] Add observability integration; create separate tool CLIs only when independently useful.

Use focused branches from `dev/0.2.0`, targeted commits, short messages, and no attribution trailers.
