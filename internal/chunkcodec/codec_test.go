package chunkcodec

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// buildVSZa wraps payload with the VSZa container around Valve-magic zstd frames.
func buildVSZa(t *testing.T, plain []byte, magicSuffix byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		t.Fatal(err)
	}
	frame := enc.EncodeAll(plain, nil)
	// Re-magic to the Valve variant.
	binary.LittleEndian.PutUint32(frame[0:4], 0xFD2FB500|uint32(magicSuffix))

	crc := crc32.ChecksumIEEE(plain)
	out := new(bytes.Buffer)
	out.WriteString("VSZa")
	_ = binary.Write(out, binary.LittleEndian, crc)
	out.Write(frame)
	_ = binary.Write(out, binary.LittleEndian, crc)
	_ = binary.Write(out, binary.LittleEndian, uint64(len(plain)))
	out.WriteString("zsv")
	return out.Bytes()
}

func TestDecompressVSZa(t *testing.T) {
	plain := bytes.Repeat([]byte("steam depot chunk payload 0123456789 "), 200)
	for _, suffix := range []byte{0x25, 0x26, 0x27} {
		chunk := buildVSZa(t, plain, suffix)
		out, format, err := DecompressWithFormat(chunk)
		if err != nil {
			t.Fatalf("suffix %02X: %v", suffix, err)
		}
		if format != FormatVSZa {
			t.Fatalf("suffix %02X: format=%d", suffix, format)
		}
		if !bytes.Equal(out, plain) {
			t.Fatalf("suffix %02X: roundtrip mismatch (%d vs %d bytes)", suffix, len(out), len(plain))
		}
	}
}

func TestDecompressVSZaCRCGuard(t *testing.T) {
	plain := []byte("some data")
	chunk := buildVSZa(t, plain, 0x25)
	chunk[4] ^= 0xFF // corrupt header CRC
	if _, _, err := DecompressWithFormat(chunk); err == nil {
		t.Fatal("expected CRC error")
	}
}

func TestDecompressGzip(t *testing.T) {
	plain := []byte("gzip chunk data gzip chunk data")
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(plain)
	w.Close()
	out, format, err := DecompressWithFormat(buf.Bytes())
	if err != nil || format != FormatGzip {
		t.Fatalf("err=%v format=%d", err, format)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("gzip roundtrip mismatch")
	}
}

func TestDecompressStore(t *testing.T) {
	plain := []byte("\x01\x02\x03raw bytes no magic")
	out, format, err := DecompressWithFormat(plain)
	if err != nil || format != FormatStore {
		t.Fatalf("err=%v format=%d", err, format)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("store roundtrip mismatch")
	}
}

func TestDecompressVZaFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/vza_fixture.bin")
	if err != nil {
		t.Skip("fixture missing:", err)
	}
	plain, err := os.ReadFile("testdata/vza_plain.bin")
	if err != nil {
		t.Fatal(err)
	}
	out, format, err := DecompressWithFormat(fixture)
	if err != nil {
		t.Fatalf("VZa decode: %v", err)
	}
	if format != FormatVZa {
		t.Fatalf("format=%d want %d", format, FormatVZa)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("VZa mismatch: got %d want %d", len(out), len(plain))
	}
}

// TestRealVSZaSample decodes a VSZa container produced by steamclient64.dll's
// own compressor (sub_138CECC90) — the ground truth for real depot chunks.
func TestRealVSZaSample(t *testing.T) {
	vsza, err := os.ReadFile("testdata/real_vsza.bin")
	if err != nil {
		t.Skip("real sample missing:", err)
	}
	plain, err := os.ReadFile("testdata/real_plain.bin")
	if err != nil {
		t.Fatal(err)
	}
	out, format, err := DecompressWithFormat(vsza)
	if err != nil {
		t.Fatalf("Go decode of DLL-produced VSZa failed: %v", err)
	}
	if format != FormatVSZa {
		t.Fatalf("format=%d want %d", format, FormatVSZa)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("roundtrip mismatch: got %d want %d", len(out), len(plain))
	}
}

// TestRealVZaChunk decodes a real VZa depot chunk captured from Steam's CDN
// (Harmony workshop item) — ground truth for the LZMA1 path. This exact chunk
// previously broke the hand-rolled decoder.
func TestRealVZaChunk(t *testing.T) {
	raw, err := os.ReadFile("testdata/real_vza_chunk.bin")
	if err != nil {
		t.Skip("real VZa chunk missing:", err)
	}
	plain, err := os.ReadFile("testdata/real_vza_plain.bin")
	if err != nil {
		t.Fatal(err)
	}
	out, format, err := DecompressWithFormat(raw)
	if err != nil {
		t.Fatalf("VZa decode of real chunk: %v", err)
	}
	if format != FormatVZa {
		t.Fatalf("format=%d want %d", format, FormatVZa)
	}
	if !bytes.Equal(out[:len(plain)], plain) {
		t.Fatalf("real VZa mismatch: got %d want %d", len(out), len(plain))
	}
}

func TestFormatDetectionOrder(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want int
	}{
		{"gzip", []byte{0x1f, 0x8b, 0x08, 0x00}, FormatGzip},
		{"VSZa", []byte{'V', 'S', 'Z', 'a'}, FormatVSZa},
		{"ZIP", []byte{'P', 'K', 3, 4}, FormatZip},
		{"VZa", []byte{'V', 'Z', 'a', 0}, FormatVZa},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Build a minimal valid container for each, just check detection.
			var data []byte
			switch c.want {
			case FormatGzip:
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				w.Write([]byte("x"))
				w.Close()
				data = buf.Bytes()
			case FormatVSZa:
				data = buildVSZa(t, []byte("x"), 0x28)
			case FormatZip:
				var buf bytes.Buffer
				zw := zip.NewWriter(&buf)
				f, _ := zw.Create("f")
				f.Write([]byte("x"))
				zw.Close()
				data = buf.Bytes()
			case FormatVZa:
				data, _ = os.ReadFile("testdata/vza_fixture.bin")
			}
			_, got, err := DecompressWithFormat(data)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got != c.want {
				t.Fatalf("%s: format=%d want %d", c.name, got, c.want)
			}
		})
	}
}

// TestVZaNotConfusedWithVSZa guards the VSZa-vs-VZa boundary: a VSZa header
// must not be misrouted to the VZa (LZMA) path.
func TestVZaNotConfusedWithVSZa(t *testing.T) {
	vsza := buildVSZa(t, []byte("hello"), 0x28)
	if isVZa(vsza) {
		t.Fatal("VSZa container misdetected as VZa")
	}
	if _, format, err := DecompressWithFormat(vsza); err != nil || format != FormatVSZa {
		t.Fatalf("format=%d err=%v", format, err)
	}
}

func TestIsRawLZMA(t *testing.T) {
	// Heuristic: (b0&0xF)==8 and (b1|b0<<8)%31==0.
	// 0x8? + b1 such that (b1 | b0<<8) % 31 == 0. b0=0x08 -> b1 | 0x800 = v.
	// Find b1 with v%31==0: v=0x800+b1; 0x800=2048; 2048%31=2; need b1≡-2≡29 mod31 -> b1=29.
	if !isRawLZMA([]byte{0x08, 29, 0, 0}) {
		t.Fatal("heuristic should match crafted header")
	}
	if isRawLZMA([]byte{0x1f, 0x8b}) {
		t.Fatal("gzip must not match raw-lzma heuristic")
	}
	if isRawLZMA([]byte{'V', 'Z'}) {
		t.Fatal("VZa must not match raw-lzma heuristic")
	}
}
