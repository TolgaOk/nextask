---
name: nextask-setup-worker
description: Set up a nextask worker to execute tasks. Covers local workers, containerized workers with dependency isolation, remote servers via SSH, and cloud GPU providers (RunPod, Vast.ai). Use when the user wants to add a worker, run tasks on a GPU machine, set up a container with project dependencies, or says "add a worker" or "I want to run jobs on my server."
---

Related skills: `nextask` (enqueue, monitor, manage tasks), `nextask-setup-services` (deploy PostgreSQL, git server).

Set up workers that claim and execute nextask tasks. Services (PostgreSQL, git remote) must already be running. If not, use the `nextask-setup-services` skill first.

**Installing nextask:** `curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash`

For SSH to remote servers, use `ssh -o ConnectTimeout=10 user@host "command"`. Do not use `-t`/`-tt` for non-interactive commands. Do not run SSH in background.

## Agent guidance

If `AskUserQuestion` is available, use it to present choices as structured options. Otherwise, ask in plain text.

**Quick reference:**
```
0. Check nextask (silent install if missing)
1. Where? → local / remote / cloud
   cloud → 1b. Template or SSH? → template: 3, SSH: 2
2. Deps? (local & remote) → no / container / venv
3. Cloud container setup (registry, Dockerfile, push)
4. Verify
```

Before asking the user for DB URL or source remote, check if they already have a config:
```bash
cat ~/.config/nextask/global.toml 2>/dev/null
cat .nextask.toml 2>/dev/null
echo "NEXTASK_DB_URL=$NEXTASK_DB_URL"
echo "NEXTASK_SOURCE_REMOTE=$NEXTASK_SOURCE_REMOTE"
```
Use existing values if found.

### 0. Check nextask

Check `nextask --version`. If not installed, install with:
```bash
curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash
```

### 1. "Where will the worker run?"

- **Local** → this machine. Continue to step 2.
- **Remote server** → existing machine with SSH access. Continue to step 2.
- **Cloud provider** (RunPod, Vast.ai, Lambda, etc.) → continue to step 1b.

### 1b. "Container template or remote access?"

Only if cloud provider was chosen:

- **Container template** → provider runs a per-project image. Jump to step 3.
- **Remote access** (SSH into the cloud machine) → treat as remote server. Continue to step 2.

### 2. "Does the worker need project dependencies (Python packages, CUDA, etc.)?"

For local and remote:

- **Container** (recommend) → build a per-project Docker image with deps + nextask. Reproducible and isolated. Jump to step 4.
- **Virtual env** (venv/conda) → start the worker from within the activated environment so child processes inherit it. E.g., `conda activate myproject && nextask worker`. Alternatively, bake activation into the enqueued command: `nextask enqueue "source .venv/bin/activate && python train.py"`. Jump to step 4.
- **No deps needed** → run nextask directly without isolation. Jump to step 4.

### 3. Cloud provider container setup

Build a per-project image with the project's dependencies and nextask, push it to a registry, and configure as the provider's template/pod.

- "Which container registry?" → Docker Hub, GHCR, or provider-specific.
- Help build the Dockerfile, push to registry, and configure on the provider.

Continue to step 4.

### 4. Verify

After starting the worker, run `nextask worker list` to confirm it appears as "running". Then run the end-to-end test at the bottom.

## Local worker

```bash
nextask worker
```

Foreground. Use `--daemon` to background. Use `--once` for a single task then exit.

**With container** (for dependency isolation):

```dockerfile
FROM python:3.12
RUN pip install torch numpy scipy matplotlib
RUN curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash
```

```bash
docker build -t myproject-worker -f Dockerfile.worker .
docker run --rm \
  -e NEXTASK_DB_URL="postgres://nextask:<password>@<host>:5432/nextask" \
  -e NEXTASK_SOURCE_REMOTE="<remote>" \
  myproject-worker nextask worker
```

Pass secrets as env vars. Never bake credentials into the image.

**With GPU** (local NVIDIA GPU):
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

2. Create config with restricted permissions:
   ```bash
   ssh user@server 'install -m 600 /dev/null ~/.nextask.env && cat > ~/.nextask.env << EOF
   NEXTASK_DB_URL="postgres://nextask:<password>@<db-host>:5432/nextask"
   NEXTASK_SOURCE_REMOTE="<remote>"
   EOF'
   ```

3. Start worker:
   ```bash
   ssh user@server "set -a && source ~/.nextask.env && set +a && nextask worker --daemon"
   ```

**With container** (same as local, but run on the remote):
```bash
ssh user@server "docker run -d --rm \
  -e NEXTASK_DB_URL='...' -e NEXTASK_SOURCE_REMOTE='...' \
  myproject-worker nextask worker"
```

**Verify:** `nextask worker list`

## Cloud GPU (RunPod, Vast.ai, Lambda)

Build an image with the provider's base image + project deps + nextask. Pass config as env vars. Use `--filter` to route tasks and `--exit-if-idle` to stop billing when idle.

Example Dockerfile for RunPod:
```dockerfile
FROM runpod/base:1.0.3-cuda1290-ubuntu2404
RUN pip install torch jax flax
RUN curl -fsSL https://raw.githubusercontent.com/TolgaOk/nextask/main/scripts/install.sh | bash
```

Build and push to a registry:
```bash
docker build -t <user>/myproject-gpu:latest -f Dockerfile.gpu .
docker push <user>/myproject-gpu:latest
```

Create a pod/template with:
- Image: `<user>/myproject-gpu:latest`
- Env vars: `NEXTASK_DB_URL`, `NEXTASK_SOURCE_REMOTE`
- Start command: `nextask worker --filter gpu=a100 --exit-if-idle 5m`

`--exit-if-idle 5m` exits after 5 minutes with no tasks. The pod stays running. Stop it via the provider to stop billing.

Enqueue side:
```bash
nextask enqueue "python train.py" --snapshot --tag gpu=a100
```

For Vast.ai and Lambda, same pattern: provider base image + deps + nextask + env vars.

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
| Container exits immediately | Missing nextask binary or bad entrypoint, test with `docker run --rm <image> nextask --version` |
| Container can't reach DB | Host network differs from container network. Use host IP, not `localhost`. On Docker Desktop, `host.docker.internal` works |
| SSH worker won't start | Check env file sourced correctly: `ssh user@host "set -a && source ~/.nextask.env && set +a && nextask --version"` |
| `nextask worker` hangs on start | DB URL wrong or unreachable from worker host. Test with `nextask list --db-url "..."` |
| Cloud template fails | Verify image is pushed and accessible: `docker pull <image>`. Check provider env vars are set |
