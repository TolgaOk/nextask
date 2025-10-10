"""Command-line interface for nextask."""

import json
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from typing import Optional

import click
import redis as redis_lib
from rich.console import Console
from rich.table import Table
from rich.panel import Panel
from rich.json import JSON
from rich import box

from nextask import RecordStatus, TaskQueue

console = Console()


@click.group()
@click.option("--host", default="localhost", help="Redis host")
@click.option("--port", default=6379, type=int, help="Redis port")
@click.option("--db", default=0, type=int, help="Redis database number")
@click.pass_context
def cli(ctx, host, port, db):
    """Nextask - Pythonic distributed task queue for ML experiments."""
    ctx.ensure_object(dict)
    ctx.obj["host"] = host
    ctx.obj["port"] = port
    ctx.obj["db"] = db


def get_queue(ctx) -> TaskQueue:
    """Get TaskQueue instance from context."""
    return TaskQueue(
        host=ctx.obj["host"],
        port=ctx.obj["port"],
        db=ctx.obj["db"],
    )


# ============================================================================
# TASK MANAGEMENT COMMANDS
# ============================================================================


@cli.command()
@click.argument("path")
@click.option("--data", default="{}", help="JSON data for the record")
@click.option(
    "--status",
    type=click.Choice(["pending", "running", "completed", "failed"]),
    default="pending",
    help="Initial status",
)
@click.pass_context
def add(ctx, path, data, status):
    """Add a new record to the queue.
    
    Example:
        nextask add /experiments/exp001 --data '{"lr": 0.001, "epochs": 100}'
    """
    queue = get_queue(ctx)
    
    try:
        data_dict = json.loads(data)
    except json.JSONDecodeError as e:
        console.print(f"[red]✗ Invalid JSON data:[/red] {e}")
        sys.exit(1)
    
    try:
        record = queue.create_record(path, data=data_dict, status=status)
        
        info = f"[cyan]Path:[/cyan] {record.path}\n"
        info += f"[cyan]Status:[/cyan] {_status_style(record.status.value)}"
        
        console.print(Panel(info, title="✓ Record Created", border_style="green"))
        
        if record.data:
            console.print("\n[cyan]Data:[/cyan]")
            console.print(JSON.from_data(record.data))
    except Exception as e:
        console.print(f"[red]✗ Error creating record:[/red] {e}")
        sys.exit(1)


@cli.command()
@click.option("--prefix", default="/", help="Path prefix filter")
@click.option(
    "--status",
    type=click.Choice(["pending", "running", "completed", "failed"]),
    help="Filter by status",
)
@click.option("--limit", type=int, help="Limit number of results")
@click.option("--json", "output_json", is_flag=True, help="Output as JSON")
@click.pass_context
def list(ctx, prefix, status, limit, output_json):
    """List records with optional filters.
    
    Example:
        nextask list --prefix /experiments --status pending --limit 10
    """
    queue = get_queue(ctx)
    
    try:
        records = queue.list_records(prefix)
        
        if status:
            records = [r for r in records if r.status.value == status]
        
        if limit:
            records = records[:limit]
        
        if not records:
            console.print("[yellow]No records found.[/yellow]")
            return
        
        if output_json:
            output = [
                {
                    "path": r.path,
                    "status": r.status.value,
                    "data": r.data,
                    "created_at": r.created_at,
                    "updated_at": r.updated_at,
                }
                for r in records
            ]
            console.print(json.dumps(output, indent=2))
        else:
            table = Table(title=f"Records (found {len(records)})", box=box.ROUNDED)
            table.add_column("Status", style="cyan", width=12)
            table.add_column("Path", style="white")
            table.add_column("Created", style="dim")
            table.add_column("Data", style="dim")
            
            for record in records:
                status_display = _status_style(record.status.value)
                data_display = json.dumps(record.data)[:50] + "..." if len(json.dumps(record.data)) > 50 else json.dumps(record.data)
                
                table.add_row(
                    status_display,
                    record.path,
                    _format_timestamp(record.created_at),
                    data_display if record.data else "",
                )
            
            console.print(table)
    except Exception as e:
        console.print(f"[red]✗ Error listing records:[/red] {e}")
        sys.exit(1)


@cli.command()
@click.argument("path")
@click.pass_context
def show(ctx, path):
    """Show detailed information about a specific record.
    
    Example:
        nextask show /experiments/exp001
    """
    queue = get_queue(ctx)
    
    try:
        record = queue.get_record(path)
        if not record:
            console.print(f"[red]✗ Record not found:[/red] {path}")
            sys.exit(1)
        
        table = Table(title="Record", box=box.ROUNDED, show_header=False)
        table.add_column("Property", style="cyan", width=15, vertical="top")
        table.add_column("Value", style="white")
        
        table.add_row("Path", record.path)
        table.add_row("Status", _status_style(record.status.value))
        table.add_row("Created", _format_timestamp(record.created_at))
        table.add_row("Updated", _format_timestamp(record.updated_at))
        table.add_row("Duration", f"{record.duration:.2f}s")
        table.add_row("Age", f"{record.age:.2f}s")
        
        if record.data:
            json_str = json.dumps(record.data, indent=2)
            table.add_row("[bold]Data[/bold]", json_str)
        
        console.print(table)
    except Exception as e:
        console.print(f"[red]✗ Error showing record:[/red] {e}")
        sys.exit(1)


@cli.command()
@click.argument("path")
@click.option(
    "--status",
    type=click.Choice(["pending", "running", "completed", "failed"]),
    help="Update status",
)
@click.option("--data", help="Update data (JSON, will merge)")
@click.pass_context
def update(ctx, path, status, data):
    """Update a record's status or data.
    
    Example:
        nextask update /experiments/exp001 --status completed
        nextask update /experiments/exp001 --data '{"accuracy": 0.95}'
    """
    queue = get_queue(ctx)
    
    if not status and not data:
        console.print("[red]✗ Must specify --status or --data[/red]")
        sys.exit(1)
    
    try:
        record = queue.get_record(path)
        if not record:
            console.print(f"[red]✗ Record not found:[/red] {path}")
            sys.exit(1)
        
        if status:
            queue.set_status(path, status)
            console.print(f"[green]✓[/green] Updated status to: {_status_style(status)}")
        
        if data:
            try:
                data_dict = json.loads(data)
                queue.update_data(path, data_dict)
                console.print(f"[green]✓[/green] Updated data:")
                console.print(JSON.from_data(data_dict, indent=2))
            except json.JSONDecodeError as e:
                console.print(f"[red]✗ Invalid JSON data:[/red] {e}")
                sys.exit(1)
    except Exception as e:
        console.print(f"[red]✗ Error updating record:[/red] {e}")
        sys.exit(1)


@cli.command()
@click.option("--prefix", default="/", help="Show stats for prefix")
@click.pass_context
def stats(ctx, prefix):
    """Show queue statistics.
    
    Example:
        nextask stats --prefix /experiments
    """
    queue = get_queue(ctx)
    
    try:
        records = queue.list_records(prefix)
        
        if not records:
            console.print(f"[yellow]No records found for prefix: {prefix}[/yellow]")
            return
        
        status_counts = {"pending": 0, "running": 0, "completed": 0, "failed": 0}
        total_duration = 0
        
        for record in records:
            status_counts[record.status.value] = status_counts.get(record.status.value, 0) + 1
            total_duration += record.duration
        
        avg_duration = total_duration / len(records) if records else 0
        
        table = Table(title=f"Queue Statistics (prefix: {prefix})", box=box.ROUNDED)
        table.add_column("Metric", style="cyan")
        table.add_column("Value", style="white", justify="right")
        
        table.add_row("Total records", str(len(records)))
        table.add_row("Pending", f"[yellow]{status_counts['pending']}[/yellow]")
        table.add_row("Running", f"[blue]{status_counts['running']}[/blue]")
        table.add_row("Completed", f"[green]{status_counts['completed']}[/green]")
        table.add_row("Failed", f"[red]{status_counts['failed']}[/red]")
        table.add_row("", "")
        table.add_row("Avg duration", f"{avg_duration:.2f}s")
        
        finished = status_counts['completed'] + status_counts['failed']
        if finished > 0:
            success_rate = (status_counts['completed'] / finished) * 100
            color = "green" if success_rate >= 80 else "yellow" if success_rate >= 50 else "red"
            table.add_row("Success rate", f"[{color}]{success_rate:.1f}%[/{color}]")
        
        console.print(table)
    except Exception as e:
        console.print(f"[red]✗ Error calculating stats:[/red] {e}")
        sys.exit(1)


@cli.command()
@click.option("--prefix", help="Clear records with prefix")
@click.option(
    "--status",
    type=click.Choice(["pending", "running", "completed", "failed"]),
    help="Clear records with status",
)
@click.option("--all", "clear_all", is_flag=True, help="Clear all records")
@click.option("--yes", is_flag=True, help="Skip confirmation")
@click.pass_context
def clear(ctx, prefix, status, clear_all, yes):
    """Clear records from the queue (with confirmation).
    
    Example:
        nextask clear --status failed
        nextask clear --prefix /old --yes
    """
    queue = get_queue(ctx)
    
    try:
        if clear_all:
            records = queue.list_records("/")
            msg = "all records"
        elif prefix:
            records = queue.list_records(prefix)
            msg = f"records with prefix '{prefix}'"
        elif status:
            records = queue.list_records("/")
            records = [r for r in records if r.status.value == status]
            msg = f"records with status '{status}'"
        else:
            console.print("[red]✗ Must specify --prefix, --status, or --all[/red]")
            sys.exit(1)
        
        if not records:
            console.print("[yellow]No records to clear.[/yellow]")
            return
        
        if not yes:
            console.print(f"[yellow]⚠  About to delete {len(records)} {msg}[/yellow]")
            if not click.confirm("Continue?"):
                console.print("[dim]Cancelled.[/dim]")
                return
        
        for record in records:
            key = f"record:{record.path}"
            queue.redis.delete(key)
            queue.redis.srem("records:index", record.path)
            queue.redis.zrem(f"status:{record.status.value}", record.path)
        
        console.print(f"[green]✓ Cleared {len(records)} record(s)[/green]")
    except Exception as e:
        console.print(f"[red]✗ Error clearing records:[/red] {e}")
        sys.exit(1)


# ============================================================================
# REDIS MANAGEMENT COMMANDS
# ============================================================================


@cli.group()
def redis():
    """Manage local Redis instances."""
    pass


@redis.command("status")
@click.pass_context
def redis_status(ctx):
    """Show Redis connection status."""
    try:
        queue = get_queue(ctx)
        info = queue.redis.info("server")
        memory_info = queue.redis.info("memory")
        stats_info = queue.redis.info("stats")
        dbsize = queue.redis.dbsize()
        
        # Create a nice table
        table = Table(title="Redis Connection Status", box=box.ROUNDED)
        table.add_column("Property", style="cyan")
        table.add_column("Value", style="white")
        
        table.add_row("Host", f"{ctx.obj['host']}:{ctx.obj['port']}")
        table.add_row("Database", str(ctx.obj['db']))
        table.add_row("Version", info['redis_version'])
        table.add_row("Uptime", f"{info['uptime_in_seconds']}s")
        table.add_row("Memory", memory_info['used_memory_human'])
        table.add_row("Connections", str(stats_info['total_connections_received']))
        table.add_row("Commands", str(stats_info['total_commands_processed']))
        table.add_row("Keys in DB", str(dbsize))
        
        console.print(table)
        console.print("\n[green]✓ Connected[/green]\n")
    except redis_lib.ConnectionError:
        console.print(f"[red]✗ Cannot connect to Redis at {ctx.obj['host']}:{ctx.obj['port']}[/red]")
        sys.exit(1)
    except Exception as e:
        console.print(f"[red]✗ Error:[/red] {e}")
        sys.exit(1)


@redis.command("start")
@click.option("--port", default=6379, type=int, help="Port to run on")
@click.option("--name", help="Named instance")
@click.option("--db-path", type=click.Path(), help="Data directory path")
@click.option("--daemonize", is_flag=True, help="Run in background")
def redis_start(port, name, db_path, daemonize):
    """Start a local Redis server.
    
    Example:
        nextask redis start --port 6380 --name dev --daemonize
    """
    # Check if redis-server is available
    try:
        subprocess.run(["redis-server", "--version"], capture_output=True, check=True)
    except (subprocess.CalledProcessError, FileNotFoundError):
        console.print("[red]✗ redis-server not found. Please install Redis first:[/red]")
        console.print("   [dim]brew install redis[/dim]  (macOS)")
        console.print("   [dim]apt-get install redis-server[/dim]  (Ubuntu)")
        sys.exit(1)
    
    # Check if port is already in use
    try:
        test_conn = redis_lib.Redis(host="localhost", port=port, socket_connect_timeout=1)
        test_conn.ping()
        console.print(f"[yellow]⚠  Redis is already running on port {port}[/yellow]")
        sys.exit(1)
    except redis_lib.ConnectionError:
        pass  # Port is free
    
    # Create data directory
    if db_path:
        db_path_obj = Path(db_path)
    else:
        db_path_obj = Path.home() / ".nextask" / "redis" / (name or f"port-{port}")
    
    db_path_obj.mkdir(parents=True, exist_ok=True)
    
    # Create config file
    config_file = db_path_obj / "redis.conf"
    pid_file = db_path_obj / "redis.pid"
    log_file = db_path_obj / "redis.log"
    
    config_content = f"""
port {port}
dir {db_path_obj}
pidfile {pid_file}
logfile {log_file}
daemonize {"yes" if daemonize else "no"}
"""
    
    # Mark it as nextask-managed
    if name:
        config_content += f"\n# nextask-name: {name}\n"
    
    config_file.write_text(config_content)
    
    # Start Redis
    try:
        cmd = ["redis-server", str(config_file)]
        if daemonize:
            subprocess.run(cmd, check=True, capture_output=True)
            
            info_table = Table(title="✓ Redis Started", box=box.ROUNDED)
            info_table.add_column("Property", style="cyan")
            info_table.add_column("Value", style="white")
            
            info_table.add_row("Port", str(port))
            if name:
                info_table.add_row("Name", name)
            info_table.add_row("Data", str(db_path_obj))
            info_table.add_row("PID file", str(pid_file))
            info_table.add_row("Log file", str(log_file))
            
            console.print(info_table)
        else:
            console.print(f"[cyan]Starting Redis on port {port}...[/cyan]")
            console.print(f"[dim]Data directory: {db_path_obj}[/dim]")
            console.print(f"[dim]Press Ctrl+C to stop[/dim]\n")
            subprocess.run(cmd)
    except subprocess.CalledProcessError as e:
        console.print(f"[red]✗ Error starting Redis:[/red] {e}")
        sys.exit(1)
    except KeyboardInterrupt:
        console.print("\n[yellow]👋 Redis stopped[/yellow]")


@redis.command("stop")
@click.option("--name", help="Named instance to stop")
@click.option("--port", type=int, help="Port number to stop")
@click.option("--all", "stop_all", is_flag=True, help="Stop all nextask-managed instances")
def redis_stop(name, port, stop_all):
    """Stop Redis server(s).
    
    Example:
        nextask redis stop --name dev
        nextask redis stop --port 6380
        nextask redis stop --all
    """
    nextask_dir = Path.home() / ".nextask" / "redis"
    
    if not nextask_dir.exists():
        console.print("[yellow]No nextask-managed Redis instances found.[/yellow]")
        return
    
    stopped = 0
    
    for instance_dir in nextask_dir.iterdir():
        if not instance_dir.is_dir():
            continue
        
        pid_file = instance_dir / "redis.pid"
        config_file = instance_dir / "redis.conf"
        
        if not pid_file.exists() or not config_file.exists():
            continue
        
        # Check if this matches the filter
        if name:
            config_text = config_file.read_text()
            if f"# nextask-name: {name}" not in config_text:
                continue
        elif port:
            config_text = config_file.read_text()
            if f"port {port}" not in config_text:
                continue
        elif not stop_all:
            continue
        
        # Stop this instance
        try:
            pid = int(pid_file.read_text().strip())
            subprocess.run(["kill", str(pid)], check=True)
            console.print(f"[green]✓[/green] Stopped Redis instance: {instance_dir.name}")
            pid_file.unlink()
            stopped += 1
        except (ValueError, subprocess.CalledProcessError, FileNotFoundError):
            console.print(f"[yellow]⚠[/yellow]  Could not stop: {instance_dir.name}")
    
    if stopped == 0:
        console.print("[yellow]No matching Redis instances found to stop.[/yellow]")
    else:
        console.print(f"\n[green]Stopped {stopped} instance(s)[/green]")


@redis.command("list")
def redis_list():
    """List all nextask-managed Redis instances."""
    nextask_dir = Path.home() / ".nextask" / "redis"
    
    if not nextask_dir.exists():
        console.print("[yellow]No nextask-managed Redis instances found.[/yellow]")
        return
    
    instances = []
    
    for instance_dir in nextask_dir.iterdir():
        if not instance_dir.is_dir():
            continue
        
        pid_file = instance_dir / "redis.pid"
        config_file = instance_dir / "redis.conf"
        
        if not config_file.exists():
            continue
        
        # Parse config
        config_text = config_file.read_text()
        port = None
        name = None
        
        for line in config_text.split("\n"):
            if line.startswith("port "):
                port = line.split()[1]
            elif line.startswith("# nextask-name: "):
                name = line.split(":", 1)[1].strip()
        
        # Check if running
        running = False
        if pid_file.exists():
            try:
                pid = int(pid_file.read_text().strip())
                # Check if process exists
                subprocess.run(["kill", "-0", str(pid)], check=True, capture_output=True)
                running = True
            except (ValueError, subprocess.CalledProcessError):
                pass
        
        instances.append({
            "name": name or instance_dir.name,
            "port": port,
            "running": running,
            "path": instance_dir,
        })
    
    if not instances:
        console.print("[yellow]No nextask-managed Redis instances found.[/yellow]")
        return
    
    # Create a nice table
    table = Table(title="Nextask-managed Redis Instances", box=box.ROUNDED)
    table.add_column("Status", style="cyan", width=12)
    table.add_column("Port", style="white", width=8)
    table.add_column("Name", style="white")
    table.add_column("Path", style="dim")
    
    for inst in instances:
        status = "[green]Running[/green]" if inst["running"] else "[dim]Stopped[/dim]"
        name = inst['name'] if inst['name'] != inst['path'].name else ""
        
        table.add_row(
            status,
            inst['port'],
            name,
            str(inst['path']),
        )
    
    console.print(table)


# ============================================================================
# HELPER FUNCTIONS
# ============================================================================


def _format_timestamp(ts: float) -> str:
    """Format Unix timestamp to readable string."""
    return datetime.fromtimestamp(ts).strftime("%Y-%m-%d %H:%M:%S")


def _status_style(status: str) -> str:
    """Return styled status string."""
    styles = {
        "pending": "[yellow]pending[/yellow]",
        "running": "[blue]running[/blue]",
        "completed": "[green]completed[/green]",
        "failed": "[red]failed[/red]",
    }
    return styles.get(status, status)


def _bool_style(value: bool) -> str:
    """Return styled boolean value."""
    return "[green]True[/green]" if value else "[dim]False[/dim]"


if __name__ == "__main__":
    cli(obj={})

