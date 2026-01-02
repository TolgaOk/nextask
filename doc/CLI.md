# CLI Reference

## Global Options

| Option | Description |
|--------|-------------|
| `--db-url` | PostgreSQL connection URL (required) |

---

## init db

Create database tables. Safe to run multiple times.

```bash
nextask init db --db-url "postgres://localhost/nextask"
```

---

## init source

Create a local bare git repository for source snapshots.

```bash
nextask init source
nextask init source --path "/custom/path/source.git"
```

**Options:**

| Option | Description |
|--------|-------------|
| `--path` | Path for bare repo (default: `~/.nextask/source.git`) |

**Output:**

```
Source repository initialized: /Users/you/.nextask/source.git
```

---

## enqueue

Add a task to the queue.

```bash
nextask enqueue "python train.py" --db-url "..."
```

**Options:**

| Option | Description |
|--------|-------------|
| `--tag key=value ...` | Tags for metadata/filtering (multiple values allowed) |

**Examples:**

```bash
# Basic
nextask enqueue "python train.py"

# With tags
nextask enqueue "python train.py" --tag device=gpu lr=0.001 experiment=baseline
```

**Output:**

```
Task enqueued: k8f2m9xa
```

---

## list

List tasks with optional filters.

```bash
nextask list --db-url "..."
```

**Options:**

| Option | Description |
|--------|-------------|
| `--status` | Filter by status (comma-separated, OR logic) |
| `--tag` | Filter by tag key=value (repeatable, AND logic) |
| `--command` | Substring match in command (repeatable, AND logic, case-insensitive) |
| `--since` | Tasks created after (1h, 24h, 7d) |
| `--limit` | Max results (default: 50) |

**Examples:**

```bash
# All tasks
nextask list

# Filter by status
nextask list --status pending
nextask list --status running,failed

# Filter by tag
nextask list --tag device=gpu
nextask list --tag device=gpu --tag project=rl

# Substring match in command
nextask list --command "train.py"
nextask list --command "train.py" --command "lr=0.95"

# Recent tasks
nextask list --since 24h

# Combine filters
nextask list --status pending --tag device=gpu --command "train" --since 1h --limit 10
```

**Output:**

```
ID        STATUS     COMMAND              TAGS                 CREATED
k8f2m9xa  pending    python train.py      device=gpu lr=0.001  2025-01-03 10:30
a2b3c4d5  running    python eval.py       device=cpu           2025-01-03 10:25
```
