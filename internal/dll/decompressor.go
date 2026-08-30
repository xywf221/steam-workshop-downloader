package dll

// Decompressor exposes the steamclient64.dll multi-format decompressor.
// Test-only oracle: production uses the pure-Go chunkcodec.Decompress.

// Decompressor wraps the loaded DLL's decompress + VSZa-compress functions.
type Decompressor struct {
	path string
}

// NewDecompressor loads steamclient64.dll (empty path = default search, which
// includes the repo-root testdata/ directory).
func NewDecompressor(path string) (*Decompressor, error) {
	if err := Load(path); err != nil {
		return nil, err
	}
	return &Decompressor{path: LoadedPath()}, nil
}

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
