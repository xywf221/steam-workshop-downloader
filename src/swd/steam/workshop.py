"""Expand a list of workshop IDs into the flat set of items to download.

Collections (``file_type == 2``) are recursively exploded. The result is
deduplicated while preserving the user's order.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

from swd.constants import PUBLISHED_FILE_BATCH
from swd.ui.log import Log


def resolve_ids(client: Any, app_id: int, ids: Iterable[int], log: Log) -> list[int]:
    """Resolve ``ids`` against the Steam API, expanding collections."""
    from steam.client import EResult

    all_ids = list(dict.fromkeys(ids))
    resolved: list[int] = []

    for start in range(0, len(all_ids), PUBLISHED_FILE_BATCH):
        batch = all_ids[start : start + PUBLISHED_FILE_BATCH]
        resp = client.send_um_and_wait(
            "PublishedFile.GetDetails#1",
            {
                "publishedfileids": batch,
                "includetags": False,
                "includeadditionalpreviews": False,
                "includechildren": True,
                "includekvtags": False,
                "includevotes": False,
                "short_description": True,
                "includeforsaledata": False,
                "includemetadata": False,
                "language": 0,
            },
            timeout=10,
        )

        for wf in resp.body.publishedfiledetails:
            if wf.result != EResult.OK:
                log.warn(f"failed to get details for {wf.publishedfileid}, skipping")
                continue

            title = wf.title or str(wf.publishedfileid)
            if wf.file_type == 2:  # Collection
                child_ids = [c.publishedfileid for c in wf.children]
                log.info(f"  '{title}' is a collection, {len(child_ids)} items")
                resolved.extend(resolve_ids(client, app_id, child_ids, log))
            else:
                resolved.append(wf.publishedfileid)

    return resolved


__all__ = ["resolve_ids"]
