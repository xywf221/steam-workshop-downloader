package dll

// Oracle exposes the steamclient64.dll multi-format decompressor behind the
// same interface the pure-Go codecs use, so tests can swap implementations
// and require byte-identical output. Production code never constructs this —
// it exists only as a test oracle.

import (
	"path/filepath"
)

// Decompressor implements chunkcodec.Decompressor over steamclient64.dll.
// It is a test oracle, not for production use.
type Decompressor struct {
	path string
}

// NewDecompressor loads steamclient64.dll from path (empty = default search,
// which includes the repo-root testdata/ directory) and returns the oracle.
func NewDecompressor(path string) (*Decompressor, error) {
	if err := Load(path); err != nil {
		return nil, err
	}
	return &Decompressor{path: LoadedPath()}, nil
}

// Name identifies the implementation.
func (d *Decompressor) Name() string { return "dll" }

// Path returns the loaded DLL path.
func (d *Decompressor) Path() string { return d.path }

// Decompress runs the DLL dispatcher over a chunk.
func (d *Decompressor) Decompress(data []byte) ([]byte, error) {
	return Decompress(data)
}

// CompressVSZa mints a ground-truth VSZa container via the DLL's own
// compressor (sub_138CECC90). Test-only.
func (d *Decompressor) CompressVSZa(plain []byte) ([]byte, error) {
	return CompressVSZa(plain)
}

// TestdataPath returns the canonical repo-root testdata DLL path relative to
// a package dir depth (e.g. "../../testdata/steamclient64.dll" from
// internal/chunkcodec). Kept here so all oracle lookups share one constant.
func TestdataPath(repoRootFromCwd string) string {
	return filepath.Join(repoRootFromCwd, "testdata", "steamclient64.dll")
}
