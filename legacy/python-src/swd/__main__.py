"""``python -m swd ...`` entry point."""

from swd.cli import cmd_swd

if __name__ == "__main__":  # pragma: no cover
    import sys

    sys.exit(cmd_swd())
