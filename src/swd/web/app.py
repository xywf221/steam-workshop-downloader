"""Flask application factory and routes."""

from __future__ import annotations

from pathlib import Path
from typing import Any

from swd.constants import (
    DEFAULT_QUERY_PAGE_SIZE,
    DEFAULT_WEB_OUTPUT,
    MAX_QUERY_PAGE_SIZE,
)
from swd.steam.workshop import get_item_details, query_files, resolve_query_type
from swd.ui.log import Log
from swd.web.cache import BrowseCache
from swd.web.downloads import DownloadManager
from swd.web.media import fetch_media
from swd.web.serialize import item_to_dict, query_to_dict
from swd.web.session import SteamSession

_PKG = Path(__file__).resolve().parent


def create_app(
    *,
    proxy_url: str | None = None,
    log: Log | None = None,
    steam: SteamSession | None = None,
    cache: BrowseCache | None = None,
    output_dir: str | Path | None = None,
    downloads: DownloadManager | None = None,
) -> Any:
    """Build a Flask app. ``steam`` / ``cache`` / ``downloads`` may be injected for tests."""
    from flask import Flask, Response, abort, jsonify, render_template, request

    if log is None:
        log = Log.create(use_color=True)

    # Wire SOCKS/HTTP proxy early so media fetches (and later Steam) share it.
    if proxy_url:
        from swd.steam.proxy import setup_proxy

        setup_proxy(proxy_url)

    out = Path(output_dir if output_dir is not None else DEFAULT_WEB_OUTPUT).resolve()

    app = Flask(
        "swd.web",
        template_folder=str(_PKG / "templates"),
        static_folder=str(_PKG / "static"),
    )
    app.config["SWD_PROXY"] = proxy_url
    app.config["SWD_OUTPUT"] = str(out)
    # Separate Steam sessions: downloads hold the worker for minutes (CDN
    # chunks). If they share the browse session, QueryFiles/GetDetails stall.
    session = steam if steam is not None else SteamSession(proxy_url, log, name="browse")
    browse_cache = cache if cache is not None else BrowseCache()
    if downloads is not None:
        dl_mgr = downloads
    else:
        dl_steam = SteamSession(proxy_url, log, name="download")
        app.extensions["steam_download"] = dl_steam
        dl_mgr = DownloadManager(dl_steam, out, log)
    app.extensions["steam"] = session
    app.extensions["swd_log"] = log
    app.extensions["browse_cache"] = browse_cache
    app.extensions["downloads"] = dl_mgr

    def _steam() -> SteamSession:
        return app.extensions["steam"]

    def _log() -> Log:
        return app.extensions["swd_log"]

    def _cache() -> BrowseCache:
        return app.extensions["browse_cache"]

    def _downloads() -> DownloadManager:
        return app.extensions["downloads"]

    def _proxy() -> str | None:
        return app.config.get("SWD_PROXY")

    def _output() -> str:
        return app.config.get("SWD_OUTPUT") or str(out)

    def _item_dict(item: Any) -> dict[str, Any]:
        return item_to_dict(item, proxy_url=_proxy())

    def _downloads_enabled() -> bool:
        return app.extensions.get("downloads") is not None

    def _parse_list_args() -> tuple[int, str, int, int, str, str]:
        appid_raw = request.args.get("appid", type=str)
        if not appid_raw:
            abort(400, description="missing required query parameter: appid")
        try:
            app_id = int(appid_raw)
        except (TypeError, ValueError):
            abort(400, description="appid must be an integer")
        if app_id <= 0:
            abort(400, description="appid must be positive")

        q = (request.args.get("q") or request.args.get("search") or "").strip()
        page = request.args.get("page", default=1, type=int) or 1
        if page < 1:
            page = 1
        per_page = request.args.get("per_page", default=DEFAULT_QUERY_PAGE_SIZE, type=int)
        if per_page is None or per_page < 1:
            per_page = DEFAULT_QUERY_PAGE_SIZE
        per_page = min(per_page, MAX_QUERY_PAGE_SIZE)
        sort = (request.args.get("sort") or "").strip().lower()
        cursor = (request.args.get("cursor") or "").strip()
        return app_id, q, page, per_page, sort, cursor

    def _run_query(
        app_id: int,
        q: str,
        page: int,
        per_page: int,
        sort: str,
        cursor: str,
    ) -> Any:
        """QueryFiles with TTL cache. Populates item cache for fast detail views."""
        sort_key = sort or ("search" if q else "trend")
        query_type = resolve_query_type(sort or None, q)
        use_cursor = _cache().cursor_for_page(
            app_id, q=q, sort=sort_key, page=page, per_page=per_page, cursor=cursor
        )
        key = BrowseCache.query_key(
            app_id, q=q, sort=sort_key, page=page, per_page=per_page, cursor=use_cursor
        )
        cached = _cache().get_query(key)
        if cached is not None:
            return cached

        result = _steam().call(
            query_files,
            app_id,
            search_text=q,
            page=page,
            numperpage=per_page,
            query_type=query_type,
            cursor=use_cursor or None,
            log=_log(),
        )
        _cache().put_query(key, result)
        return result

    def _run_detail(item_id: int) -> Any:
        cached = _cache().get_item(item_id)
        if cached is not None:
            return cached
        item = _steam().call(get_item_details, item_id, log=_log())
        if item is not None:
            _cache().put_item(item)
        return item

    @app.context_processor
    def _inject_globals() -> dict[str, Any]:
        counts = _downloads().counts() if _downloads_enabled() else {}
        return {
            "download_enabled": _downloads_enabled(),
            "download_output": _output(),
            "download_counts": counts,
        }

    @app.get("/api/health")
    def api_health() -> Any:
        s = _steam()
        try:
            logged = bool(s.logged_on)
        except Exception:
            logged = False
        return jsonify(
            {
                "ok": True,
                "steam_logged_on": logged,
                "steam_client": s.has_client,
                "proxy": bool(_proxy()),
                "download_enabled": _downloads_enabled(),
                "output_dir": _output(),
                "downloads": _downloads().counts() if _downloads_enabled() else {},
            }
        )

    @app.get("/api/workshop")
    def api_workshop_list() -> Any:
        app_id, q, page, per_page, sort, cursor = _parse_list_args()
        try:
            result = _run_query(app_id, q, page, per_page, sort, cursor)
        except Exception as e:
            _log().err(f"QueryFiles failed: {e}")
            abort(502, description=f"Steam QueryFiles failed: {e}")
        return jsonify(query_to_dict(result, proxy_url=_proxy()))

    @app.get("/api/workshop/<int:item_id>")
    def api_workshop_detail(item_id: int) -> Any:
        try:
            item = _run_detail(item_id)
        except Exception as e:
            _log().err(f"GetDetails failed for {item_id}: {e}")
            abort(502, description=f"Steam GetDetails failed: {e}")
        if item is None:
            abort(404, description=f"Workshop item {item_id} not found")
        return jsonify(_item_dict(item))

    @app.post("/api/downloads")
    def api_downloads_create() -> Any:
        if not _downloads_enabled():
            abort(503, description="downloads not configured")
        data = request.get_json(silent=True) or {}
        # Also accept form fields.
        app_id_raw = data.get("appid") or request.form.get("appid") or request.args.get("appid")
        wid_raw = (
            data.get("workshopid")
            or data.get("id")
            or request.form.get("workshopid")
            or request.args.get("workshopid")
        )
        title = (
            data.get("title")
            or request.form.get("title")
            or request.args.get("title")
            or ""
        )
        try:
            app_id = int(app_id_raw)
            workshop_id = int(wid_raw)
        except (TypeError, ValueError):
            abort(400, description="appid and workshopid must be integers")
        if app_id <= 0 or workshop_id <= 0:
            abort(400, description="appid and workshopid must be positive")

        jobs = _downloads().enqueue(app_id, workshop_id, title=str(title or ""))
        return jsonify(
            {
                "ok": True,
                "jobs": [j.to_dict() for j in jobs],
                "output_dir": _output(),
            }
        ), 202

    @app.get("/api/downloads")
    def api_downloads_list() -> Any:
        if not _downloads_enabled():
            abort(503, description="downloads not configured")
        limit = request.args.get("limit", default=50, type=int) or 50
        jobs = _downloads().list_jobs(limit=min(max(limit, 1), 200))
        return jsonify(
            {
                "output_dir": _output(),
                "counts": _downloads().counts(),
                "jobs": [j.to_dict() for j in jobs],
            }
        )

    @app.get("/api/downloads/<job_id>")
    def api_downloads_one(job_id: str) -> Any:
        if not _downloads_enabled():
            abort(503, description="downloads not configured")
        job = _downloads().get_job(job_id)
        if job is None:
            abort(404, description="job not found")
        return jsonify(job.to_dict())

    @app.get("/media/image")
    def media_image() -> Any:
        """Proxy a Steam preview image so it rides the configured --proxy."""
        raw = request.args.get("u") or request.args.get("url") or ""
        if not raw:
            abort(400, description="missing u= image url")
        try:
            body, ctype = fetch_media(raw, proxy_url=_proxy())
        except ValueError as e:
            abort(400, description=str(e))
        except Exception as e:
            _log().warn(f"media fetch failed: {e}")
            abort(502, description=f"media fetch failed: {e}")
        return Response(
            body,
            mimetype=ctype,
            headers={
                "Cache-Control": "public, max-age=3600",
            },
        )

    @app.get("/")
    def index() -> Any:
        appid_raw = request.args.get("appid", default="", type=str) or ""
        q = (request.args.get("q") or "").strip()
        sort = (request.args.get("sort") or "").strip().lower()
        page = request.args.get("page", default=1, type=int) or 1
        if page < 1:
            page = 1
        per_page = request.args.get("per_page", default=DEFAULT_QUERY_PAGE_SIZE, type=int)
        if per_page is None or per_page < 1:
            per_page = DEFAULT_QUERY_PAGE_SIZE
        per_page = min(per_page, MAX_QUERY_PAGE_SIZE)
        cursor = (request.args.get("cursor") or "").strip()

        error: str | None = None
        result = None
        if appid_raw.strip():
            try:
                app_id = int(appid_raw)
                if app_id <= 0:
                    raise ValueError("appid must be positive")
            except ValueError:
                error = "AppID must be a positive integer."
            else:
                try:
                    result = _run_query(app_id, q, page, per_page, sort, cursor)
                except Exception as e:
                    _log().err(f"QueryFiles failed: {e}")
                    error = f"Steam query failed: {e}"

        items = [_item_dict(i) for i in result.items] if result else []
        total = result.total if result else 0
        total_pages = max(1, (total + per_page - 1) // per_page) if result else 1
        next_cursor = result.next_cursor if result else ""
        sort_effective = sort or ("search" if q else "trend")

        return render_template(
            "index.html",
            appid=appid_raw,
            q=q,
            sort=sort_effective,
            page=page,
            per_page=per_page,
            total=total,
            total_pages=total_pages,
            items=items,
            error=error,
            has_query=bool(appid_raw.strip()),
            proxy_enabled=bool(_proxy()),
            cursor=cursor,
            next_cursor=next_cursor,
        )

    @app.get("/item/<int:item_id>")
    def item_detail(item_id: int) -> Any:
        error: str | None = None
        item_dict: dict[str, Any] | None = None
        try:
            item = _run_detail(item_id)
            if item is None:
                error = f"Workshop item {item_id} not found."
            else:
                item_dict = _item_dict(item)
        except Exception as e:
            _log().err(f"GetDetails failed for {item_id}: {e}")
            error = f"Steam GetDetails failed: {e}"

        return render_template(
            "detail.html",
            item=item_dict,
            item_id=item_id,
            error=error,
            proxy_enabled=bool(_proxy()),
        )

    @app.get("/downloads")
    def downloads_page() -> Any:
        jobs = _downloads().list_jobs(limit=100) if _downloads_enabled() else []
        return render_template(
            "downloads.html",
            jobs=[j.to_dict() for j in jobs],
            counts=_downloads().counts() if _downloads_enabled() else {},
        )

    return app


__all__ = ["create_app"]
