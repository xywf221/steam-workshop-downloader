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

DEFAULT_RETRIES = 5
DEFAULT_OUTPUT = "."
