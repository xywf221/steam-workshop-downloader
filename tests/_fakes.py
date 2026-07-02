"""Shared fake Steam types used across tests.

Lives in its own module so test files can import without needing
``tests/`` to be a package (no ``__init__.py``).
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field


@dataclass
class FakeFile:
    """Drop-in stand-in for ``CDNDepotManifest.FileEntry`` (regular file)."""

    filename: str
    data: bytes = b""
    is_file: bool = True
    read_count: int = 0
    read_side_effects: list[Callable[[], object]] = field(default_factory=list)

    def read(self) -> bytes:
        self.read_count += 1
        if self.read_side_effects:
            eff = self.read_side_effects.pop(0)
            if callable(eff):
                result = eff()
            elif isinstance(eff, Exception):
                raise eff
            else:
                result = eff
            if isinstance(result, Exception):
                raise result
            if result is not None:
                return result  # type: ignore[return-value]
        return self.data


@dataclass
class FakeDir:
    """Drop-in stand-in for a directory entry (``is_file == False``)."""

    filename: str
    is_file: bool = False


@dataclass
class FakeManifest:
    name: str
    files: list  # list[FakeFile | FakeDir]

    def iter_files(self):
        return iter(self.files)


@dataclass
class FakeCDN:
    manifests: dict

    def get_manifest_for_workshop_item(self, wid: int) -> FakeManifest:
        if wid not in self.manifests:
            raise KeyError(wid)
        return self.manifests[wid]
