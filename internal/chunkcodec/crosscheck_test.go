//go:build windows

package chunkcodec_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/xywf221/steam-workshop-downloader/internal/chunkcodec"
	"github.com/xywf221/steam-workshop-downloader/internal/dll"
)

// TestDecompressorParity proves the pure-Go decoder and the DLL oracle agree
// byte-for-byte on ground-truth data. Data is minted by the DLL's own
// compressor, then both implementations must decode it identically.
func TestDecompressorParity(t *testing.T) {
	dllImpl, err := dll.NewDecompressor("") // default search includes repo-root testdata/
	if err != nil {
		t.Skipf("steamclient64.dll oracle unavailable: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	repetitive := bytes.Repeat([]byte("Steam depot chunk content! "), 5000) // ~135KB
	random200k := make([]byte, 200_000)
	rng.Read(random200k)
	compressible := make([]byte, 300_000)
	for i := range compressible {
		compressible[i] = byte(i % 251)
	}
	small := []byte("hi")
	medium := bytes.Repeat([]byte("medium sized payload for realistic chunk sizes "), 100)

	payloads := map[string][]byte{
		"small":        small,
		"medium":       medium,
		"repetitive":   repetitive,
		"random200k":   random200k,
		"compressible": compressible,
	}

	for name, plain := range payloads {
		vsza, err := dllImpl.CompressVSZa(plain)
		if err != nil {
			t.Fatalf("%s: dll compress: %v", name, err)
		}

		goOut, err := chunkcodec.Decompress(vsza)
		if err != nil {
			t.Fatalf("%s: go decode: %v", name, err)
		}
		dllOut, err := dllImpl.Decompress(vsza)
		if err != nil {
			t.Fatalf("%s: dll decode: %v", name, err)
		}
		if !bytes.Equal(goOut, dllOut) {
			t.Fatalf("%s: go (%dB) != dll (%dB)", name, len(goOut), len(dllOut))
		}
		if !bytes.Equal(goOut, plain) {
			t.Fatalf("%s: decoded != original (%d vs %d bytes)", name, len(goOut), len(plain))
		}
		t.Logf("%s: go == dll == original (%d -> %d bytes VSZa)", name, len(plain), len(vsza))
	}
}
