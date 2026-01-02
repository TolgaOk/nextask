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

**Examples:**

```bash
# Local development
nextask init db --db-url "postgres://localhost/nextask"

# With credentials
nextask init db --db-url "postgres://user:pass@localhost:5432/nextask"
```
