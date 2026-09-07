# 0.2.0-alpha TODO

Nextask runs commands. Built-in integrations prepare tasks and compose runtime wrappers. Standalone tools remain independent.

## Nextask

- [x] Confirm daemon readiness; bound startup failures and preserve literal filters.
- [x] Validate CLI options before DB access; reject conflicting output modes.
- [x] Move shared test setup into helpers; check reset errors and own cleanup.
- [x] Audit CLI consistency: arguments, flags, help, output, errors, and exit behavior.

- [x] Unify worker validation and daemon options; preserve `--rm` and `--exit-if-idle`.
- [x] Use typed DB errors; preserve causes and classify network failures correctly.
- [x] Route CLI output through command writers; report rendering failures.
- [x] Move enqueue SQL into a named DB operation; preserve transaction ordering.
- [x] Split worker commands and DB queries by responsibility; separate queries from migrations.

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

## Connection URLs

- [x] Share URL validation and environment-reference resolution; reject literal credentials in config.
- [ ] Accept DB URL templates and complete URLs from the environment.
- [ ] Authenticate Git with URL credentials resolved separately on submitters and workers.
- [ ] Read S3 credentials from its endpoint URL; remove fixed credential-variable requirements.
- [ ] Test missing variables, escaping, redaction, persisted tasks, and authenticated transfers; update focused docs.

## Worker failure and retry

- [ ] Keep `failed` for task failures; add `worker_failed` for worker interruptions, including intentional shutdown.
- [ ] Preserve `cancelled` for explicit task cancellation; never auto-requeue cancelled tasks.
- [ ] Update DB compatibility, CLI status filters, display, and wait behavior.
- [ ] Consider opt-in automatic requeue after worker failure, with retry limits and backoff.
- [ ] Keep task ID and original snapshot; preserve each attempt's logs and outcome.
- [ ] Treat stale heartbeats as uncertain loss; coordinate execution ownership and reject obsolete attempt updates.
- [ ] Test abrupt loss, intentional stops, DB outages, cancellation races, and retry exhaustion.

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
