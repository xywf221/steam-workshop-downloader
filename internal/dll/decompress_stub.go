//go:build !windows

package dll

import "fmt"

// Load is a no-op stub on non-Windows.
func Load(path string) error {
	return fmt.Errorf("steamclient64.dll decompression is Windows-only")
}

// LoadedPath always returns empty on non-Windows.
func LoadedPath() string { return "" }

// Decompress returns an error on non-Windows.
func Decompress(data []byte) ([]byte, error) {
	return nil, fmt.Errorf("steamclient64.dll decompression is Windows-only")
}

// CompressVSZa returns an error on non-Windows.
func CompressVSZa(data []byte) ([]byte, error) {
	return nil, fmt.Errorf("steamclient64.dll compression is Windows-only")
}
