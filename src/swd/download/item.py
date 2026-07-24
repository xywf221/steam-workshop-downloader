"""Per-item download loop with retry + progress reporting.

Within a single workshop item, regular files are fetched concurrently via
a :class:`ThreadPoolExecutor`. Worker count is either explicit (``jobs>0``)
or auto via :func:`swd.utils.suggest_jobs` based on how many files the item
has. CDN I/O is serialised inside the get_chunk patch so the shared
``steam`` session stays safe across workers.

Local files whose size and SHA-1 (``sha_content`` from the depot manifest)
already match the server are skipped — no CDN fetch, no rewrite.
"""

from __future__ import annotations

import contextlib
import hashlib
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import Any, Literal

from swd.constants import DEFAULT_JOBS, DEFAULT_RETRIES
from swd.ui import ItemStats, Log, Progress
from swd.utils import compute_backoff, suggest_jobs

FileStatus = Literal["downloaded", "skipped", "failed"]


def expected_sha1(entry: Any) -> bytes | None:
    """Return the 20-byte content SHA-1 from a depot file entry, if present.

    ValvePython exposes this as ``file_mapping.sha_content`` (raw bytes). We
    also accept a top-level ``sha_content`` / ``sha`` attribute for fakes and
    alternate wrappers. Hex strings of length 40 are accepted and decoded.
    """
    raw: Any = getattr(entry, "sha_content", None)
    if raw is None:
        raw = getattr(entry, "sha", None)
    if raw is None:
        mapping = getattr(entry, "file_mapping", None)
        if mapping is not None:
            raw = getattr(mapping, "sha_content", None)

    if raw is None:
        return None
    if isinstance(raw, (bytes, bytearray, memoryview)):
        b = bytes(raw)
        return b if len(b) == 20 else None
    if isinstance(raw, str):
        s = raw.strip().lower()
        if len(s) == 40:
            try:
                return bytes.fromhex(s)
            except ValueError:
                return None
    return None


def expected_size(entry: Any) -> int | None:
    """Return the declared file size from the entry, if known."""
    size = getattr(entry, "size", None)
    if size is None:
        mapping = getattr(entry, "file_mapping", None)
        if mapping is not None:
            size = getattr(mapping, "size", None)
    if size is None:
        return None
    try:
        return int(size)
    except (TypeError, ValueError):
        return None


def local_matches_remote(path: Path, entry: Any) -> bool:
    """True when ``path`` exists and matches the manifest size + SHA-1.

    If the entry has no usable SHA-1 we cannot prove a match — return False
    so the caller re-downloads (safe default).
    """
    if not path.is_file():
        return False

    sha = expected_sha1(entry)
    if sha is None:
        return False

    try:
        st_size = path.stat().st_size
    except OSError:
        return False

    declared = expected_size(entry)
    if declared is not None and st_size != declared:
        return False

    h = hashlib.sha1()
    try:
        with open(path, "rb") as fp:
            while True:
                chunk = fp.read(1024 * 1024)
                if not chunk:
                    break
                h.update(chunk)
    except OSError:
        return False

    return h.digest() == sha


def _download_one_file(
    f: Any,
    path: Path,
    retries: int,
    progress: Progress,
) -> tuple[FileStatus, int, Exception | None]:
    """Download a single file entry with retries, or skip if already current.

    Returns ``(status, bytes_count, last_error)``.
    ``bytes_count`` is newly written bytes for ``downloaded``, existing size
    for ``skipped``, and 0 for ``failed``.
    """
    if local_matches_remote(path, f):
        try:
            nbytes = path.stat().st_size
        except OSError:
            nbytes = expected_size(f) or 0
        progress.file_skip(f.filename, nbytes)
        return "skipped", nbytes, None

    # Stale / partial / missing — remove before rewrite.
    if path.exists():
        with contextlib.suppress(OSError):
            path.unlink()

    last_err: Exception | None = None
    for attempt in range(1, retries + 1):
        try:
            data = f.read()
            path.parent.mkdir(parents=True, exist_ok=True)
            with open(path, "wb") as fp:
                fp.write(data)
            progress.file_ok(f.filename, len(data))
            return "downloaded", len(data), None
        except Exception as e:
            last_err = e
            if attempt < retries:
                wait = compute_backoff(attempt)
                progress.retry(f.filename, attempt, retries, e, wait)
                time.sleep(wait)

    assert last_err is not None
    progress.file_fail(f.filename, last_err)
    if path.exists():
        with contextlib.suppress(OSError):
            path.unlink()
    return "failed", 0, last_err


def download_item(
    cdn: Any,
    app_id: int,
    workshop_id: int,
    output_dir: Path,
    progress: Progress,
    log: Log,
    *,
    verbose: bool = False,
    retries: int = DEFAULT_RETRIES,
    jobs: int = DEFAULT_JOBS,
) -> ItemStats | None:
    """Download a single workshop item to ``output_dir / str(workshop_id)``.

    Each individual file is retried up to ``retries`` times on failure with
    exponential backoff. Files of one item are downloaded concurrently;
    ``jobs=0`` (default) auto-picks a worker count from the file count.
    Files already present with matching SHA-1 are skipped.
    Returns an :class:`ItemStats` summarising the run.
    """
    del verbose  # reserved; progress.verbose already controls per-file lines
    del app_id  # workshop manifest is keyed by workshop_id only
    t0 = time.perf_counter()
    log.stage("MANIFEST", f"Fetching manifest for {workshop_id}...")
    try:
        manifest = cdn.get_manifest_for_workshop_item(workshop_id)
    except Exception as e:
        # Upstream steam crashes (AttributeError on None.header) or raises
        # SteamError/ManifestError on timeout / missing SteamPipe content.
        # Surface a clean per-item failure instead of aborting the whole run.
        log.err(f"manifest for {workshop_id}: {e}")
        return ItemStats(
            out_dir=output_dir / str(workshop_id),
            ok=0,
            fail=1,
            skipped=0,
            bytes_done=0,
            duration=time.perf_counter() - t0,
            name=str(workshop_id),
        )

    entries = list(manifest.iter_files())
    item_name = manifest.name or str(workshop_id)

    out_dir = output_dir / str(workshop_id)
    out_dir.mkdir(parents=True, exist_ok=True)

    # Create directories first (no network) so worker threads never race on mkdir.
    file_entries: list[Any] = []
    for f in entries:
        if not f.is_file:
            (out_dir / f.filename).mkdir(parents=True, exist_ok=True)
            continue
        (out_dir / f.filename).parent.mkdir(parents=True, exist_ok=True)
        file_entries.append(f)

    n_files = len(file_entries)
    workers = suggest_jobs(n_files) if jobs <= 0 else max(1, jobs)
    # Never spin more workers than files.
    workers = min(workers, max(1, n_files)) if n_files else 1

    log.ok(f"'{item_name}' - {n_files} files, {workers} thread{'s' if workers != 1 else ''}")
    progress.start_files(n_files)

    ok = fail = skipped = bytes_done = 0

    def _tally(status: FileStatus, nbytes: int) -> None:
        nonlocal ok, fail, skipped, bytes_done
        if status == "downloaded":
            ok += 1
            bytes_done += nbytes
        elif status == "skipped":
            skipped += 1
        else:
            fail += 1

    if n_files == 0:
        stats = ItemStats(
            out_dir=out_dir,
            ok=0,
            fail=0,
            skipped=0,
            bytes_done=0,
            duration=time.perf_counter() - t0,
            name=item_name,
        )
        progress.end_files(stats)
        return stats

    if workers == 1:
        for f in file_entries:
            status, nbytes, _err = _download_one_file(f, out_dir / f.filename, retries, progress)
            _tally(status, nbytes)
    else:
        # Counter protection is belt-and-braces; as_completed is sequential
        # in the main thread so plain ints would also be fine.
        counter_lock = threading.Lock()

        def _run(entry: Any) -> tuple[FileStatus, int]:
            status, nbytes, _err = _download_one_file(
                entry, out_dir / entry.filename, retries, progress
            )
            return status, nbytes

        with ThreadPoolExecutor(max_workers=workers, thread_name_prefix="swd-dl") as pool:
            futures = {pool.submit(_run, f): f for f in file_entries}
            for fut in as_completed(futures):
                try:
                    status, nbytes = fut.result()
                except Exception as e:
                    # _download_one_file already handles per-file errors;
                    # this is a last-ditch guard for unexpected blow-ups.
                    f = futures[fut]
                    progress.file_fail(getattr(f, "filename", "?"), e)
                    with counter_lock:
                        fail += 1
                    continue
                with counter_lock:
                    _tally(status, nbytes)

    stats = ItemStats(
        out_dir=out_dir,
        ok=ok,
        fail=fail,
        skipped=skipped,
        bytes_done=bytes_done,
        duration=time.perf_counter() - t0,
        name=item_name,
    )
    progress.end_files(stats)
    return stats


__all__ = [
    "download_item",
    "expected_sha1",
    "expected_size",
    "local_matches_remote",
]
