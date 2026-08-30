package chunkcodec

// Decompressor abstracts depot-chunk decompression so the implementation can
// be swapped between the pure-Go codecs (default, production) and the
// steamclient64.dll oracle (test-only).
type Decompressor interface {
	// Decompress auto-detects the chunk format and returns plaintext.
	Decompress(data []byte) ([]byte, error)
	// Name identifies the implementation ("go" or "dll").
	Name() string
}

// DecompressorFunc adapts a plain function to Decompressor.
type DecompressorFunc func(data []byte) ([]byte, error)

// Decompress implements Decompressor.
func (f DecompressorFunc) Decompress(data []byte) ([]byte, error) { return f(data) }

// Go returns the production pure-Go decompressor. It handles every format
// (VSZa/VZa/gzip/ZIP/raw-LZMA/store) and needs no external DLL.
func Go() Decompressor {
	return goDecompressor{}
}

type goDecompressor struct{}

func (goDecompressor) Name() string { return "go" }
func (goDecompressor) Decompress(data []byte) ([]byte, error) {
	return Decompress(data)
}
