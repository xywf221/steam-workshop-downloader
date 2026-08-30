//go:build windows

package chunkcodec

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/xywf221/steam-workshop-downloader/internal/dll"
)

// TestCrossCheckVSZaWithDLL is the definitive round-trip proof. It feeds a
// variety of payloads through steamclient64.dll's own VSZa compressor and
// decompressor, and requires our pure-Go decoder to produce byte-identical
// output to the DLL's decompressor on the DLL's own compressed data.
//
// The sample at testdata/real_vsza.bin was produced exactly this way
// (offline, by scripts/xcheck) and is decoded by TestRealVSZaSample; this
// test extends coverage to more payload shapes when the DLL is present.
func TestCrossCheckVSZaWithDLL(t *testing.T) {
	if err := dll.Load(""); err != nil {
		t.Skipf("steamclient64.dll unavailable: %v", err)
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
		vsza, err := dll.CompressVSZa(plain)
		if err != nil {
			t.Fatalf("%s: dll compress: %v", name, err)
		}

		goOut, err := Decompress(vsza)
		if err != nil {
			t.Fatalf("%s: go decode of dll-compressed VSZa: %v", name, err)
		}
		dllOut, err := dll.Decompress(vsza)
		if err != nil {
			t.Fatalf("%s: dll decode of dll-compressed VSZa: %v", name, err)
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
