"""Utility for loading Lua scripts from files."""

from pathlib import Path


def load_lua_script(script_name: str) -> str:
    """Load a Lua script from the lua directory.

    Args:
        script_name: Name of the Lua script file.

    Returns:
        Contents of the Lua script as a string.

    Raises:
        FileNotFoundError: If the script file does not exist.
    """
    script_path = Path(__file__).parent / script_name

    if not script_path.exists():
        raise FileNotFoundError(f"Lua script not found: {script_path}")

    return script_path.read_text(encoding="utf-8")
