"""Per-item download loop with retry + progress reporting."""

from __future__ import annotations

import contextlib
import time
from pathlib import Path
from typing import Any

from swd.constants import DEFAULT_RETRIES
from swd.ui import ItemStats, Log, Progress
from swd.utils import compute_backoff


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
) -> ItemStats | None:
    """Download a single workshop item to ``output_dir / str(workshop_id)``.

    Each individual file is retried up to ``retries`` times on failure with
    exponential backoff. Returns an :class:`ItemStats` summarising the run.
    Returns ``None`` only when no output directory could be created at all
    (which currently cannot happen given ``mkdir(parents=True, exist_ok=True)``).
    """
    t0 = time.perf_counter()
    log.stage("MANIFEST", f"Fetching manifest for {workshop_id}...")
    manifest = cdn.get_manifest_for_workshop_item(workshop_id)
    files = list(manifest.iter_files())
    item_name = manifest.name or str(workshop_id)
    log.ok(f"'{item_name}' - {len(files)} files")

    out_dir = output_dir / str(workshop_id)
    out_dir.mkdir(parents=True, exist_ok=True)
    progress.start_files(len(files))

    ok = fail = bytes_done = 0
    for idx, f in enumerate(files):
        if not f.is_file:
            (out_dir / f.filename).mkdir(parents=True, exist_ok=True)
            continue
        path = out_dir / f.filename
        path.parent.mkdir(parents=True, exist_ok=True)

        # Remove any partial file from a previous failed attempt so the
        # final size check (below) reflects only completed downloads.
        if path.exists():
            with contextlib.suppress(OSError):
                path.unlink()

        success = False
        last_err: Exception | None = None
        for attempt in range(1, retries + 1):
            try:
                data = f.read()
                with open(path, "wb") as fp:
                    fp.write(data)
                ok += 1
                bytes_done += len(data)
                success = True
                progress.file_ok(f.filename, len(data))
                break
            except Exception as e:
                last_err = e
                if attempt < retries:
                    wait = compute_backoff(attempt)
                    progress.retry(f.filename, attempt, retries, e, wait)
                    time.sleep(wait)
                    # Re-read the SAME entry on the next attempt. We index
                    # back into `files` rather than calling iter_files()
                    # again because the latter is a fresh generator that
                    # starts from the first file.
                    f = files[idx]

        if not success:
            assert last_err is not None
            fail += 1
            progress.file_fail(f.filename, last_err)
            if path.exists():
                with contextlib.suppress(OSError):
                    path.unlink()

    stats = ItemStats(
        out_dir=out_dir,
        ok=ok,
        fail=fail,
        bytes_done=bytes_done,
        duration=time.perf_counter() - t0,
        name=item_name,
    )
    progress.end_files(stats)
    return stats


__all__ = ["download_item"]
