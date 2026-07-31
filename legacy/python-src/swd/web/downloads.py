"""Background download queue for swd-web.

Jobs run on the Steam worker thread (via :meth:`SteamSession.call_with_cdn`)
so gevent CM stays single-threaded. Progress is mirrored into a JSON-friendly
:class:`DownloadJob` that the UI polls.
"""

from __future__ import annotations

import itertools
import queue
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Literal

from swd.constants import DEFAULT_JOBS, DEFAULT_RETRIES
from swd.download import download_item
from swd.steam.workshop import resolve_ids
from swd.ui.log import Log
from swd.utils import fmt_size
from swd.web.session import SteamSession

JobStatus = Literal["queued", "running", "done", "failed"]


@dataclass
class DownloadJob:
    """One workshop item (or collection child) download."""

    id: str
    app_id: int
    workshop_id: int
    title: str = ""
    status: JobStatus = "queued"
    message: str = ""
    out_dir: str = ""
    files_total: int = 0
    files_done: int = 0
    files_ok: int = 0
    files_fail: int = 0
    files_skip: int = 0
    bytes_done: int = 0
    current_file: str = ""
    error: str = ""
    created_at: float = field(default_factory=time.time)
    started_at: float | None = None
    finished_at: float | None = None

    def to_dict(self) -> dict[str, Any]:
        pct = 0.0
        if self.files_total > 0:
            pct = 100.0 * self.files_done / self.files_total
        elif self.status == "done":
            pct = 100.0
        duration = 0.0
        if self.started_at is not None:
            end = self.finished_at if self.finished_at is not None else time.time()
            duration = max(0.0, end - self.started_at)
        return {
            "id": self.id,
            "app_id": self.app_id,
            "workshop_id": self.workshop_id,
            "title": self.title or str(self.workshop_id),
            "status": self.status,
            "message": self.message,
            "out_dir": self.out_dir,
            "files_total": self.files_total,
            "files_done": self.files_done,
            "files_ok": self.files_ok,
            "files_fail": self.files_fail,
            "files_skip": self.files_skip,
            "bytes_done": self.bytes_done,
            "bytes_done_human": fmt_size(self.bytes_done) if self.bytes_done else "0 B",
            "percent": round(pct, 1),
            "current_file": self.current_file,
            "error": self.error,
            "created_at": self.created_at,
            "started_at": self.started_at,
            "finished_at": self.finished_at,
            "duration_s": round(duration, 1),
        }


class JobProgress:
    """Duck-typed stand-in for :class:`~swd.ui.progress.Progress` that updates a job."""

    def __init__(self, job: DownloadJob, log: Log) -> None:
        self.job = job
        self.log = log
        self.verbose = False
        self._lock = threading.Lock()

    def start_files(self, n_files: int) -> None:
        with self._lock:
            self.job.files_total = int(n_files)
            self.job.files_done = 0
            self.job.message = f"Downloading {n_files} files"

    def file_ok(self, filename: str, size_bytes: int) -> None:
        with self._lock:
            self.job.files_done += 1
            self.job.files_ok += 1
            self.job.bytes_done += int(size_bytes)
            self.job.current_file = filename
            self.job.message = f"OK {filename}"

    def file_skip(self, filename: str, size_bytes: int) -> None:
        with self._lock:
            self.job.files_done += 1
            self.job.files_skip += 1
            self.job.current_file = filename
            self.job.message = f"skip {filename}"

    def retry(
        self, filename: str, attempt: int, retries: int, err: Exception, backoff_s: int
    ) -> None:
        with self._lock:
            self.job.current_file = filename
            self.job.message = f"retry {attempt}/{retries} {filename}: {err}"
            self.log.retry(f"{attempt}/{retries} on {filename}: {err} (backoff {backoff_s}s)")

    def file_fail(self, filename: str, err: Exception) -> None:
        with self._lock:
            self.job.files_done += 1
            self.job.files_fail += 1
            self.job.current_file = filename
            self.job.message = f"FAIL {filename}: {err}"

    def end_files(self, stats: Any) -> None:
        with self._lock:
            if stats is not None:
                self.job.files_ok = int(getattr(stats, "ok", self.job.files_ok) or 0)
                self.job.files_fail = int(getattr(stats, "fail", self.job.files_fail) or 0)
                self.job.files_skip = int(getattr(stats, "skipped", self.job.files_skip) or 0)
                self.job.bytes_done = int(getattr(stats, "bytes_done", self.job.bytes_done) or 0)
                out = getattr(stats, "out_dir", None)
                if out is not None:
                    self.job.out_dir = str(out)
                name = getattr(stats, "name", None)
                if name:
                    self.job.title = str(name)
            self.job.current_file = ""


class DownloadManager:
    """Serial download queue. One active job at a time."""

    def __init__(
        self,
        steam: SteamSession,
        output_dir: Path,
        log: Log,
        *,
        retries: int = DEFAULT_RETRIES,
        jobs: int = DEFAULT_JOBS,
    ) -> None:
        self._steam = steam
        self.output_dir = Path(output_dir).resolve()
        self._log = log
        self._retries = retries
        # Parallel file jobs inside one item would re-enter SteamClient from
        # other threads; keep serial for web (``jobs`` reserved for later).
        del jobs
        self._lock = threading.Lock()
        self._jobs_map: dict[str, DownloadJob] = {}
        self._order: list[str] = []
        self._queue: queue.Queue[str | None] = queue.Queue()
        self._id_seq = itertools.count(1)
        self._thread = threading.Thread(
            target=self._loop,
            name="swd-download",
            daemon=True,
        )
        self._thread.start()
        self.output_dir.mkdir(parents=True, exist_ok=True)

    def enqueue(
        self,
        app_id: int,
        workshop_id: int,
        *,
        title: str = "",
        expand_collections: bool = True,
    ) -> list[DownloadJob]:
        """Queue one workshop id (optionally expand collections into children)."""
        ids: list[int]
        if expand_collections:
            try:
                ids = self._steam.call(
                    resolve_ids,
                    app_id,
                    [int(workshop_id)],
                    self._log,
                )
            except Exception as e:
                self._log.err(f"resolve_ids {workshop_id}: {e}")
                # Fall back to the single id so the job still surfaces as failed.
                ids = [int(workshop_id)]
        else:
            ids = [int(workshop_id)]

        if not ids:
            ids = [int(workshop_id)]

        created: list[DownloadJob] = []
        with self._lock:
            for wid in ids:
                # Dedupe: skip if same workshop already queued or running.
                existing = self._find_active(app_id, wid)
                if existing is not None:
                    created.append(existing)
                    continue
                job_id = f"dl-{next(self._id_seq)}"
                job = DownloadJob(
                    id=job_id,
                    app_id=int(app_id),
                    workshop_id=int(wid),
                    title=title if len(ids) == 1 else (title or str(wid)),
                    status="queued",
                    message="Queued",
                    out_dir=str(self.output_dir / str(wid)),
                )
                self._jobs_map[job_id] = job
                self._order.append(job_id)
                self._queue.put(job_id)
                created.append(job)
                self._log.info(f"[DL] queued {wid} -> {job.out_dir}")
        return created

    def _find_active(self, app_id: int, workshop_id: int) -> DownloadJob | None:
        for jid in reversed(self._order):
            job = self._jobs_map.get(jid)
            if job is None:
                continue
            if (
                job.app_id == app_id
                and job.workshop_id == workshop_id
                and job.status in ("queued", "running")
            ):
                return job
        return None

    def list_jobs(self, *, limit: int = 50) -> list[DownloadJob]:
        with self._lock:
            ids = list(reversed(self._order))[: max(1, limit)]
            return [self._jobs_map[i] for i in ids if i in self._jobs_map]

    def get_job(self, job_id: str) -> DownloadJob | None:
        with self._lock:
            return self._jobs_map.get(job_id)

    def counts(self) -> dict[str, int]:
        with self._lock:
            c = {"queued": 0, "running": 0, "done": 0, "failed": 0, "total": len(self._jobs_map)}
            for job in self._jobs_map.values():
                c[job.status] = c.get(job.status, 0) + 1
            return c

    def _loop(self) -> None:
        while True:
            job_id = self._queue.get()
            if job_id is None:
                return
            job = self._jobs_map.get(job_id)
            if job is None:
                continue
            self._run_one(job)

    def _run_one(self, job: DownloadJob) -> None:
        job.status = "running"
        job.started_at = time.time()
        job.message = "Starting"
        job.error = ""
        self._log.stage("DL", f"Downloading {job.workshop_id} ({job.title or '…'})")

        prog = JobProgress(job, self._log)

        def _do(client: Any, cdn: Any) -> Any:
            del client  # download_item only needs CDN (+ steam inside CDN)
            # Force serial file I/O (jobs=1): multi-file workers would call into
            # the gevent SteamClient from other threads and wedge the CM again.
            return download_item(
                cdn,
                job.app_id,
                job.workshop_id,
                self.output_dir,
                prog,  # type: ignore[arg-type]
                self._log,
                retries=self._retries,
                jobs=1,
            )

        try:
            stats = self._steam.call_with_cdn(_do, timeout=None)
        except Exception as e:
            job.status = "failed"
            job.error = str(e)
            job.message = f"Failed: {e}"
            job.finished_at = time.time()
            self._log.err(f"[DL] {job.workshop_id}: {e}")
            return

        job.finished_at = time.time()
        if stats is None or getattr(stats, "fully_failed", False):
            job.status = "failed"
            job.error = job.error or "download failed"
            job.message = "Failed"
            self._log.err(f"[DL] {job.workshop_id} failed")
        else:
            job.status = "done"
            job.message = (
                f"Done — {job.files_ok} ok / {job.files_skip} skip / {job.files_fail} fail"
            )
            self._log.ok(f"[DL] {job.workshop_id} -> {job.out_dir}")


__all__ = ["DownloadJob", "DownloadManager", "JobProgress"]
