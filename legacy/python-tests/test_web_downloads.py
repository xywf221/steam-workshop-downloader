"""Web download queue API tests (Steam fully mocked)."""

from __future__ import annotations

import time
from pathlib import Path
from unittest.mock import MagicMock

import pytest

from swd.ui import ItemStats, Log
from swd.web.downloads import DownloadManager

flask = pytest.importorskip("flask")


@pytest.fixture
def log(no_color):
    return Log(use_color=False)


def test_download_manager_enqueue_and_run(tmp_path: Path, log: Log) -> None:
    steam = MagicMock()

    def _call(fn, *args, **kwargs):
        # resolve_ids(client, app_id, ids, log) via steam.call
        return list(args[1]) if len(args) >= 2 else [args[0]]

    def _call_cdn(fn, *args, **kwargs):
        # Mirror SteamSession.call_with_cdn: timeout is for the wait, not fn.
        kwargs.pop("timeout", None)
        return fn(object(), object(), *args, **kwargs)

    steam.call.side_effect = _call
    steam.call_with_cdn.side_effect = _call_cdn

    # Patch download_item used inside manager
    import swd.web.downloads as dl_mod

    def fake_download(cdn, app_id, workshop_id, output_dir, progress, log, **kw):
        progress.start_files(2)
        progress.file_ok("a.txt", 10)
        progress.file_ok("b.txt", 20)
        stats = ItemStats(
            out_dir=output_dir / str(workshop_id),
            ok=2,
            fail=0,
            skipped=0,
            bytes_done=30,
            duration=0.1,
            name="Mod",
        )
        progress.end_files(stats)
        return stats

    orig = dl_mod.download_item
    dl_mod.download_item = fake_download  # type: ignore[assignment]
    try:
        mgr = DownloadManager(steam, tmp_path, log)
        jobs = mgr.enqueue(294100, 1001, title="Mod")
        assert len(jobs) == 1
        job = jobs[0]
        # Wait for worker
        for _ in range(50):
            if job.status in ("done", "failed"):
                break
            time.sleep(0.05)
        assert job.status == "done"
        assert job.files_ok == 2
        assert job.bytes_done == 30
        assert job.workshop_id == 1001
    finally:
        dl_mod.download_item = orig  # type: ignore[assignment]


def test_api_download_create_and_list(tmp_path: Path, log: Log) -> None:
    from swd.web.app import create_app
    from swd.web.downloads import DownloadManager

    steam = MagicMock()
    steam.logged_on = True
    steam.has_client = True
    steam.warmup = MagicMock()

    def _call(fn, *args, **kwargs):
        # call(resolve_ids, app_id, ids, log) → args[0]=app_id, args[1]=ids
        if len(args) >= 2 and isinstance(args[1], list):
            return list(args[1])
        return []

    steam.call.side_effect = _call

    finished = []

    def _call_cdn(fn, *args, **kwargs):
        # Simulate quick success without real download_item
        import swd.web.downloads as dl_mod

        kwargs.pop("timeout", None)

        def fake_download(cdn, app_id, workshop_id, output_dir, progress, log, **kw):
            progress.start_files(1)
            progress.file_ok("x.bin", 5)
            st = ItemStats(
                out_dir=output_dir / str(workshop_id),
                ok=1,
                fail=0,
                skipped=0,
                bytes_done=5,
                duration=0.01,
                name="X",
            )
            progress.end_files(st)
            finished.append(workshop_id)
            return st

        orig = dl_mod.download_item
        dl_mod.download_item = fake_download  # type: ignore[assignment]
        try:
            return fn(object(), object(), *args, **kwargs)
        finally:
            dl_mod.download_item = orig  # type: ignore[assignment]

    steam.call_with_cdn.side_effect = _call_cdn

    mgr = DownloadManager(steam, tmp_path, log)
    app = create_app(log=log, steam=steam, downloads=mgr, output_dir=tmp_path)
    app.config["TESTING"] = True
    with app.test_client() as c:
        rv = c.post(
            "/api/downloads",
            json={"appid": 294100, "workshopid": 55, "title": "Hello"},
        )
        assert rv.status_code == 202
        data = rv.get_json()
        assert data["ok"] is True
        assert len(data["jobs"]) == 1
        job_id = data["jobs"][0]["id"]

        for _ in range(50):
            one = c.get(f"/api/downloads/{job_id}").get_json()
            if one["status"] in ("done", "failed"):
                break
            time.sleep(0.05)
        assert one["status"] == "done"
        assert one["workshop_id"] == 55

        listing = c.get("/api/downloads").get_json()
        assert listing["counts"]["done"] >= 1
        assert any(j["id"] == job_id for j in listing["jobs"])

        page = c.get("/downloads")
        assert page.status_code == 200
        assert b"Downloads" in page.data

        health = c.get("/api/health").get_json()
        assert health["download_enabled"] is True
        assert str(tmp_path) in health["output_dir"]


def test_api_download_requires_ids(tmp_path: Path, log: Log) -> None:
    from swd.web.app import create_app
    from swd.web.downloads import DownloadManager

    steam = MagicMock()
    steam.logged_on = False
    steam.has_client = False
    steam.warmup = MagicMock()
    steam.call.side_effect = AssertionError("no steam")
    mgr = DownloadManager(steam, tmp_path, log)
    app = create_app(log=log, steam=steam, downloads=mgr, output_dir=tmp_path)
    app.config["TESTING"] = True
    with app.test_client() as c:
        rv = c.post("/api/downloads", json={})
        assert rv.status_code == 400
