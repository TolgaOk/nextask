# CLI Reference

Command-line interface for managing tasks and Redis instances.

## Global Options

All commands support these global options:

```bash
--host TEXT     Redis host (default: localhost)
--port INTEGER  Redis port (default: 6379)
--db INTEGER    Redis database number (default: 0)
```

**Example:**
```bash
nextask --host redis.example.com --port 6380 list
```

---

## Task Management Commands

### add

```bash
nextask add PATH [OPTIONS]
```

Create a new record in the queue.

**Arguments:**
- `PATH`: Hierarchical path for the record (e.g., `/experiments/run001`)

**Options:**
- `--data TEXT`: JSON data for the record (default: `{}`)
- `--status [pending|running|completed|failed]`: Initial status (default: `pending`)

**Example:**
```bash
nextask add /experiments/exp001 --data '{"lr": 0.001, "epochs": 100}'
```

**Output:**
```
╭────────────────────────────── ✓ Record Created ──────────────────────────────╮
│ Path: /experiments/exp001                                                    │
│ Status: pending                                                              │
│ Data: {                                                                      │
│   "lr": 0.001,                                                               │
│   "epochs": 100                                                              │
│ }                                                                            │
╰──────────────────────────────────────────────────────────────────────────────╯
```

---

### list

```bash
nextask list [OPTIONS]
```

List records with optional filters.

**Options:**
- `--prefix TEXT`: Path prefix filter (default: `/`)
- `--status [pending|running|completed|failed]`: Filter by status
- `--limit INTEGER`: Limit number of results
- `--json`: Output as JSON

**Example:**
```bash
nextask list --prefix /experiments --status pending --limit 10
```

**Output:**
```
                               Records (found 2)                                
╭──────────────┬────────────────────┬─────────────────────┬──────────────────╮
│ Status       │ Path               │ Created             │ Data             │
├──────────────┼────────────────────┼─────────────────────┼──────────────────┤
│ pending      │ /experiments/run1  │ 2025-10-10 15:43:05 │ {"lr": 0.001}    │
│ pending      │ /experiments/run2  │ 2025-10-10 15:43:44 │ {"lr": 0.01}     │
╰──────────────┴────────────────────┴─────────────────────┴──────────────────╯
```

---

### show

```bash
nextask show PATH
```

Show detailed information about a specific record.

**Arguments:**
- `PATH`: Exact record path

**Example:**
```bash
nextask show /experiments/exp001
```

**Output:**
```
                          Record                          
╭─────────────┬──────────────────────────────────────────╮
│ Path        │ /experiments/exp001                      │
│ Status      │ pending                                  │
│ Created     │ 2025-10-10 15:43:05                      │
│ Updated     │ 2025-10-10 15:43:05                      │
│ Duration    │ 0.00s                                    │
│ Age         │ 125.43s                                  │
│ Data        │ {                                        │
│             │   "lr": 0.001,                           │
│             │   "epochs": 100                          │
│             │ }                                        │
╰─────────────┴──────────────────────────────────────────╯
```

---

### update

```bash
nextask update PATH [OPTIONS]
```

Update a record's status or data.

**Arguments:**
- `PATH`: Record path to update

**Options:**
- `--status [pending|running|completed|failed]`: Update status
- `--data TEXT`: Update data (JSON, will merge with existing)

**Example:**
```bash
nextask update /experiments/exp001 --status completed
nextask update /experiments/exp001 --data '{"accuracy": 0.95}'
```

**Output:**
```
✓ Updated status to: completed
```

---

### stats

```bash
nextask stats [OPTIONS]
```

Show queue statistics.

**Options:**
- `--prefix TEXT`: Show stats for prefix (default: `/`)

**Example:**
```bash
nextask stats --prefix /experiments
```

**Output:**
```
Queue Statistics (prefix: /)            
╭───────────────┬───────╮
│ Metric        │ Value │
├───────────────┼───────┤
│ Total records │     2 │
│ Pending       │     2 │
│ Running       │     0 │
│ Completed     │     0 │
│ Failed        │     0 │
│               │       │
│ Avg duration  │ 0.00s │
╰───────────────┴───────╯
```

---

### clear

```bash
nextask clear [OPTIONS]
```

Clear records from the queue with confirmation.

**Options:**
- `--prefix TEXT`: Clear records with prefix
- `--status [pending|running|completed|failed]`: Clear records with status
- `--all`: Clear all records
- `--yes`: Skip confirmation

**Example:**
```bash
nextask clear --status failed
nextask clear --prefix /old --yes
```

**Output:**
```
⚠  About to delete 5 records with status 'failed'
Continue? [y/N]: y
✓ Deleted 5 records
```

---

## Redis Management Commands

### redis status

```bash
nextask redis status
```

Show Redis connection status and server information.

**Example:**
```bash
nextask redis status
```

**Output:**
```
       Redis Connection Status       
╭─────────────┬────────────────────╮
│ Property    │ Value              │
├─────────────┼────────────────────┤
│ Host        │ localhost:6379     │
│ Database    │ 0                  │
│ Version     │ 7.2.0              │
│ Uptime      │ 3600s              │
│ Memory      │ 1.2M               │
│ Connections │ 42                 │
│ Commands    │ 1234               │
│ Keys in DB  │ 10                 │
╰─────────────┴────────────────────╯

✓ Connected
```

---

### redis start

```bash
nextask redis start [OPTIONS]
```

Start a local Redis server.

**Options:**
- `--port INTEGER`: Port to run on (default: `6379`)
- `--name TEXT`: Named instance for easier management
- `--db-path PATH`: Data directory path
- `--daemonize`: Run in background

**Example:**
```bash
nextask redis start --port 6380 --name dev --daemonize
```

**Output:**
```
✓ Redis server started on port 6380
  Name: dev
  Data: /Users/username/.nextask/redis/dev
  PID: 12345
```

---

### redis stop

```bash
nextask redis stop [OPTIONS]
```

Stop managed Redis instances.

**Options:**
- `--name TEXT`: Stop named instance
- `--port INTEGER`: Stop instance on port
- `--all`: Stop all managed instances

**Example:**
```bash
nextask redis stop --name dev
```

**Output:**
```
✓ Stopped Redis instance: dev (port 6380)
```

---

### redis list

```bash
nextask redis list
```

List all nextask-managed Redis servers.

**Example:**
```bash
nextask redis list
```

**Output:**
```
        Managed Redis Instances        
╭─────────┬──────┬──────────┬─────────╮
│ Name    │ Port │ Status   │ PID     │
├─────────┼──────┼──────────┼─────────┤
│ dev     │ 6380 │ running  │ 12345   │
│ test    │ 6381 │ stopped  │ -       │
╰─────────┴──────┴──────────┴─────────╯
```

---

## Tips

### Scripting with JSON Output

Use `--json` flag for machine-readable output:

```bash
nextask list --json | jq '.[] | select(.status == "failed")'
```

### Batch Operations

Process multiple records in a bash loop:

```bash
for path in $(nextask list --status failed --json | jq -r '.[].path'); do
  nextask update "$path" --status pending
done
```

### Monitoring

Watch queue stats in real-time:

```bash
watch -n 1 "nextask stats"
```

