"""Steam Workshop Downloader — pure-Python Workshop item fetcher.

Top-level package. Importing :mod:`swd` is cheap (no Steam or DLL I/O).
The expensive imports (steam[client], pysocks) are deferred to where
they're actually used so that lightweight test runs can avoid them.
"""

from __future__ import annotations

from swd.constants import VERSION

__version__ = VERSION
__all__ = ["__version__", "VERSION"]
