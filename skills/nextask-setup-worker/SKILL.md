---
name: nextask-setup-worker
description: Set up a nextask worker to execute tasks. Covers local workers, containerized workers with dependency isolation, remote servers via SSH, and cloud GPU providers (RunPod, Vast.ai). Use when the user wants to add a worker, run tasks on a GPU machine, set up a container with project dependencies, or says "add a worker" or "I want to run jobs on my server."
---

Set up workers that claim and execute nextask tasks. Services (PostgreSQL, git remote) must already be running. If not, use the `nextask-setup-services` skill first.

On macOS, prefer `container` (Apple Container) over `docker` for running containers locally.

For SSH to remote servers, use `ssh -o ConnectTimeout=10 user@host "command"`. Do not use `-t`/`-tt` for non-interactive commands. Do not run SSH in background.

Before asking the user for DB URL or source remote, check if they already have a config:
```bash
cat ~/.config/nextask/global.toml 2>/dev/null
cat .nextask.toml 2>/dev/null
echo "NEXTASK_DB_URL=$NEXTASK_DB_URL"
echo "NEXTASK_SOURCE_REMOTE=$NEXTASK_SOURCE_REMOTE"
```
Use existing values if found.

## Options

- **Local**: run directly on this machine. Simplest, good for testing.
- **Container** (recommended): one image per project with all deps pre-installed. Tasks don't reinstall packages.
- **Remote server via SSH**: install nextask on an existing machine.
- **Cloud GPU** (RunPod, Vast.ai, Lambda): container worker on rented machines.

## Local worker

```bash
nextask worker
```

Foreground. Use `--daemon` to background. Use `--once` for a single task then exit.

**Verify:** `nextask worker list` shows the worker as "running".

## Container worker (recommended)

Build one image per project. Dependencies install once at build time, not per task.

```dockerfile
FROM python:3.12
# All project deps, installed once
RUN pip install torch numpy scipy matplotlib
# nextask binary
RUN curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash
```

```bash
docker build -t myproject-worker -f Dockerfile.worker .
```

Pass secrets as env vars. Never bake credentials into the image.

```bash
docker run --rm \
  -e NEXTASK_DB_URL="postgres://nextask:<password>@<host>:5432/nextask" \
  -e NEXTASK_SOURCE_REMOTE="<remote>" \
  myproject-worker nextask worker
```

For GPU: base on NVIDIA CUDA image, pass `--gpus all`:
```bash
docker run --rm --gpus all \
  -e NEXTASK_DB_URL -e NEXTASK_SOURCE_REMOTE \
  myproject-gpu-worker nextask worker --filter gpu=true
```

**Verify:** `nextask worker list`

## Remote server via SSH

1. Install nextask:
   ```bash
   ssh user@server "curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash"
   ```

2. Create a `.env` file on the remote with restricted permissions:
   ```bash
   ssh user@server 'install -m 600 /dev/null ~/.nextask.env && cat > ~/.nextask.env << EOF
   NEXTASK_DB_URL="postgres://nextask:<password>@<db-host>:5432/nextask"
   NEXTASK_SOURCE_REMOTE="<remote>"
   EOF'
   ```

3. Start worker (source the env file):
   ```bash
   ssh user@server "set -a && source ~/.nextask.env && set +a && nextask worker --daemon"
   ```

**Verify:** `nextask worker list`

## Cloud GPU (RunPod, Vast.ai, Lambda)

Build an image using the provider's base image, add project dependencies and nextask. Pass config as env vars. Use `--filter` to route tasks and `--exit-if-idle` to stop billing when idle.

Example Dockerfile for RunPod:
```dockerfile
FROM runpod/base:1.0.3-cuda1290-ubuntu2404
# Project dependencies
RUN pip install torch jax flax
# nextask
RUN curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash
```

Build and push to a registry (RunPod pulls from Docker Hub or GHCR):
```bash
docker build -t <user>/myproject-gpu:latest -f Dockerfile.gpu .
docker push <user>/myproject-gpu:latest
```

Create a RunPod template or pod with:
- Image: `<user>/myproject-gpu:latest`
- Env vars: `NEXTASK_DB_URL`, `NEXTASK_SOURCE_REMOTE`
- Start command: `nextask worker --filter gpu=a100 --exit-if-idle 5m`

`--exit-if-idle 5m` exits the worker process after 5 minutes with no tasks. The pod itself stays running (you need to stop/destroy it via the provider CLI or console to stop billing). `--filter gpu=a100` ensures it only claims tasks tagged for this GPU type.

Enqueue side:
```bash
nextask enqueue "python train.py" --snapshot --tag gpu=a100
```

For Vast.ai and Lambda, same pattern: provider base image + deps + nextask + env vars. Each provider has its own way to set the start command and env vars (CLI or web console).

## End-to-end test

From the local machine:

```bash
# Simple task
nextask enqueue "echo hello from nextask" --attach
# Expected: "hello from nextask", task completes

# Snapshot task (if using --snapshot)
nextask enqueue "ls -la" --snapshot --attach
# Expected: file listing, task completes

# Cleanup
nextask list --since 1h
nextask remove <id>
```

If the simple task works but snapshot fails, the git remote is misconfigured.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Tasks stuck `pending` | Check `nextask worker list`, ensure `--filter` tags match `--tag` |
| Tasks go `stale` | Worker crashed, check logs and restart |
| Worker can't reach DB | Verify URL, check firewall, test with `nextask list --db-url "..."` from worker host |
| `git clone` fails in worker | Wrong source remote or token expired, verify with `git ls-remote` |
| Container exits immediately | Missing nextask binary or bad entrypoint, test with `docker run --rm <image> nextask --version` |
