"""Tests for :func:`swd.download.item.download_item`.

We never touch the real Steam network. The fake CDN/manifest classes live
in :mod:`tests.conftest` and are imported at module level so they're
picked up by both pytest and the test functions without needing an
``__init__.py`` in ``tests/``.
"""

from __future__ import annotations

# Add tests/ to sys.path so we can import the shared _fakes module.
import os as _os
import sys as _sys
from pathlib import Path

import pytest

from swd.download import download_item
from swd.ui import Log, Progress

_TESTS_DIR = _os.path.dirname(_os.path.abspath(__file__))
if _TESTS_DIR not in _sys.path:
    _sys.path.insert(0, _TESTS_DIR)

from _fakes import FakeCDN, FakeDir, FakeFile, FakeManifest  # noqa: E402


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def _progress(log: Log) -> Progress:
    return Progress(total_items=1, log=log, verbose=False)


def test_download_item_single_file_success(tmp_path: Path, log: Log) -> None:
    cdn = FakeCDN(
        manifests={
            1001: FakeManifest(name="Mod A", files=[FakeFile("a.txt", b"hello")]),
        }
    )
    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn,
            294100,
            1001,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=3,
        )

    assert stats is not None
    assert stats.ok == 1
    assert stats.fail == 0
    assert stats.bytes_done == 5
    assert (tmp_path / "1001" / "a.txt").read_bytes() == b"hello"


def test_download_item_retry_then_succeed(tmp_path: Path, log: Log) -> None:
    """A failing read should be retried and eventually succeed."""
    bad = FakeFile("a.txt", b"good")
    bad.read_side_effects = [RuntimeError("first try"), None]
    cdn = FakeCDN(manifests={1001: FakeManifest(name="Mod A", files=[bad])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn,
            294100,
            1001,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=3,
        )

    assert stats.ok == 1
    assert stats.fail == 0
    assert bad.read_count == 2
    assert (tmp_path / "1001" / "a.txt").read_bytes() == b"good"


def test_download_item_all_retries_fail(tmp_path: Path, log: Log) -> None:
    bad = FakeFile("a.txt", b"never seen")
    bad.read = lambda: (_ for _ in ()).throw(RuntimeError("hard fail"))
    cdn = FakeCDN(manifests={1001: FakeManifest(name="Mod A", files=[bad])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn,
            294100,
            1001,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=2,
        )

    assert stats.ok == 0
    assert stats.fail == 1
    assert stats.fully_failed
    assert not (tmp_path / "1001" / "a.txt").exists()


def test_download_item_retry_targets_same_file(tmp_path: Path, log: Log) -> None:
    """Regression: re-fetching the failed entry must point at the SAME file
    object, not at ``files[0]``. Two files, one retry, must not cross-talk.
    """
    a = FakeFile("a.txt", b"AAA")
    b = FakeFile("b.txt", b"BBB")
    b.read_side_effects = [RuntimeError("boom"), None]

    cdn = FakeCDN(manifests={1001: FakeManifest(name="Mod", files=[a, b])})
    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn,
            294100,
            1001,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=3,
        )

    assert stats.ok == 2
    assert stats.fail == 0
    assert (tmp_path / "1001" / "a.txt").read_bytes() == b"AAA"
    assert (tmp_path / "1001" / "b.txt").read_bytes() == b"BBB"


def test_download_item_creates_directory_for_non_file(
    tmp_path: Path,
    log: Log,
) -> None:
    cdn = FakeCDN(
        manifests={
            1001: FakeManifest(
                name="M",
                files=[
                    FakeDir("nested/"),
                ],
            )
        }
    )

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn,
            294100,
            1001,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=1,
        )
    assert stats.ok == 0
    assert stats.fail == 0
    assert (tmp_path / "1001" / "nested").is_dir()


def test_download_item_partial_file_cleared_before_retry(
    tmp_path: Path,
    log: Log,
) -> None:
    """Stale local content (hash mismatch) is unlinked and re-downloaded."""
    target = tmp_path / "1001" / "a.txt"
    target.parent.mkdir(parents=True)
    target.write_bytes(b"STALE")

    f = FakeFile("a.txt", b"NEW")
    cdn = FakeCDN(manifests={1001: FakeManifest(name="M", files=[f])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        download_item(cdn, 294100, 1001, tmp_path, prog, log, verbose=False, retries=1)
    assert target.read_bytes() == b"NEW"
    assert f.read_count == 1


def test_download_item_skips_when_sha_matches(tmp_path: Path, log: Log) -> None:
    """Existing file with matching size + SHA-1 is not re-fetched from CDN."""
    payload = b"already good"
    target = tmp_path / "1001" / "a.txt"
    target.parent.mkdir(parents=True)
    target.write_bytes(payload)

    f = FakeFile("a.txt", payload)
    cdn = FakeCDN(manifests={1001: FakeManifest(name="M", files=[f])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn, 294100, 1001, tmp_path, prog, log, verbose=False, retries=1
        )

    assert stats is not None
    assert stats.skipped == 1
    assert stats.ok == 0
    assert stats.fail == 0
    assert stats.bytes_done == 0
    assert f.read_count == 0
    assert target.read_bytes() == payload


def test_download_item_redownloads_when_size_mismatches(tmp_path: Path, log: Log) -> None:
    payload = b"correct-content"
    target = tmp_path / "1001" / "a.txt"
    target.parent.mkdir(parents=True)
    target.write_bytes(b"short")  # wrong size + wrong hash

    f = FakeFile("a.txt", payload)
    cdn = FakeCDN(manifests={1001: FakeManifest(name="M", files=[f])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn, 294100, 1001, tmp_path, prog, log, verbose=False, retries=1
        )

    assert stats.ok == 1
    assert stats.skipped == 0
    assert f.read_count == 1
    assert target.read_bytes() == payload


def test_download_item_no_sha_always_downloads(tmp_path: Path, log: Log) -> None:
    """Without a usable SHA we cannot verify — re-download (safe default)."""
    payload = b"same-bytes"
    target = tmp_path / "1001" / "a.txt"
    target.parent.mkdir(parents=True)
    target.write_bytes(payload)

    f = FakeFile("a.txt", payload, sha_content=b"")  # empty → treated as missing
    cdn = FakeCDN(manifests={1001: FakeManifest(name="M", files=[f])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn, 294100, 1001, tmp_path, prog, log, verbose=False, retries=1
        )

    assert stats.ok == 1
    assert stats.skipped == 0
    assert f.read_count == 1


def test_download_item_manifest_failure_is_soft(tmp_path: Path, log: Log) -> None:
    """A SteamError/timeout from get_manifest must not abort the whole process.

    Returns a fully_failed ItemStats so the CLI can continue with the next id.
    """
    cdn = FakeCDN(manifests={})  # KeyError on lookup

    prog = _progress(log)
    with prog:
        prog.start_item(1, "9999")
        stats = download_item(
            cdn,
            294100,
            9999,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=1,
        )

    assert stats is not None
    assert stats.fully_failed
    assert stats.ok == 0
    assert stats.fail == 1


def test_download_item_parallel_jobs(tmp_path: Path, log: Log) -> None:
    """jobs>1 downloads multiple files and writes them all correctly."""
    files = [FakeFile(f"f{i}.bin", f"data-{i}".encode()) for i in range(6)]
    cdn = FakeCDN(manifests={1001: FakeManifest(name="Big", files=files)})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn,
            294100,
            1001,
            tmp_path,
            prog,
            log,
            verbose=False,
            retries=2,
            jobs=4,
        )

    assert stats is not None
    assert stats.ok == 6
    assert stats.fail == 0
    for i in range(6):
        assert (tmp_path / "1001" / f"f{i}.bin").read_bytes() == f"data-{i}".encode()


def test_download_item_auto_jobs_single_file(tmp_path: Path, log: Log) -> None:
    """jobs=0 (auto) with one file still works (1 worker)."""
    cdn = FakeCDN(
        manifests={1001: FakeManifest(name="One", files=[FakeFile("a.txt", b"x")])}
    )
    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn, 294100, 1001, tmp_path, prog, log, retries=1, jobs=0
        )
    assert stats is not None
    assert stats.ok == 1
    assert (tmp_path / "1001" / "a.txt").read_bytes() == b"x"


def test_download_item_parallel_with_retries(tmp_path: Path, log: Log) -> None:
    """A flaky file under jobs>1 still retries and succeeds."""
    flaky = FakeFile("a.txt", b"good")
    flaky.read_side_effects = [RuntimeError("transient"), None]
    other = FakeFile("b.txt", b"BBB")
    cdn = FakeCDN(manifests={1001: FakeManifest(name="M", files=[flaky, other])})

    prog = _progress(log)
    with prog:
        prog.start_item(1, "1001")
        stats = download_item(
            cdn, 294100, 1001, tmp_path, prog, log, retries=3, jobs=2
        )

    assert stats.ok == 2
    assert stats.fail == 0
    assert (tmp_path / "1001" / "a.txt").read_bytes() == b"good"
    assert (tmp_path / "1001" / "b.txt").read_bytes() == b"BBB"
