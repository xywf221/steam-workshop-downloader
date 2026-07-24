"""Project-wide constants."""

APP_NAME = "Steam Workshop Downloader"
VERSION = "5.0.0"

# Steam decompression dispatcher RVA inside steamclient64.dll.
# sub_138CEAA90 — auto-detects VSZa / VZa / gzip / ZIP / raw LZMA.
RVA_DECOMPRESS_ALL = 0xCEAA90

# CUtlBuffer function pointers used by the dispatcher.
RVA_PUT_FUNC = 0xEB570
RVA_GET_FUNC = 0xD3F20

# Upper bound on a single decompressed chunk; rejects malformed input that
# would otherwise let the dispatcher scribble arbitrarily far in memory.
MAX_CHUNK_SIZE = 100 * 1024 * 1024  # 100 MB

# Steam "PublishedFile.GetDetails#1" UM request batches by 100.
PUBLISHED_FILE_BATCH = 100

# Unified-message (PublishedFile.GetDetails etc.) RPC timeouts.
# Upstream steam.client.cdn uses timeout=7 which is too short over a
# high-latency proxy — send_um_and_wait returns None and then the
# library crashes on ``resp.header``. We re-patch with a longer budget
# and explicit retries.
UM_TIMEOUT = 30
UM_RETRIES = 3

DEFAULT_RETRIES = 5
DEFAULT_OUTPUT = "."

# Concurrent file downloads within a single workshop item.
# 0 = auto (see :func:`swd.utils.suggest_jobs`). Cap auto at MAX_AUTO_JOBS
# so we don't open dozens of CDN connections for huge collections of files.
DEFAULT_JOBS = 0
MAX_AUTO_JOBS = 8

# Optional Flask web UI / API (swd-web).
DEFAULT_WEB_HOST = "127.0.0.1"
DEFAULT_WEB_PORT = 8765
DEFAULT_QUERY_PAGE_SIZE = 20
MAX_QUERY_PAGE_SIZE = 50
DEFAULT_WEB_OUTPUT = "."
