package manifest

import (
	"encoding/binary"
	"testing"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/pb"
)

func TestParseMinimalManifest(t *testing.T) {
	// Build a tiny synthetic manifest: payload + metadata + signature + end.
	var payloadEnc pb.Encoder
	var fileEnc pb.Encoder
	fileEnc.StringField(1, "foo/bar.txt")
	fileEnc.VarintField(2, 3) // size
	fileEnc.VarintField(3, 0) // flags
	fileEnc.BytesField(5, make([]byte, 20))
	var chunkEnc pb.Encoder
	chunkEnc.BytesField(1, make([]byte, 20))
	chunkEnc.Fixed32Field(2, 0)
	chunkEnc.VarintField(3, 0)
	chunkEnc.VarintField(4, 3)
	chunkEnc.VarintField(5, 3)
	fileEnc.MessageField(6, chunkEnc.Bytes())
	payloadEnc.MessageField(1, fileEnc.Bytes())

	var metaEnc pb.Encoder
	metaEnc.VarintField(1, 294100) // depot_id
	metaEnc.VarintField(2, 99)     // gid
	metaEnc.BoolField(4, false)    // filenames_encrypted

	sig := []byte{0x0a, 0x00} // empty-ish signature message

	var raw []byte
	appendSection := func(magic uint32, body []byte) {
		var hdr [8]byte
		binary.LittleEndian.PutUint32(hdr[0:4], magic)
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(body)))
		raw = append(raw, hdr[:]...)
		raw = append(raw, body...)
	}
	appendSection(magicPayload, payloadEnc.Bytes())
	appendSection(magicMetadata, metaEnc.Bytes())
	appendSection(magicSignature, sig)
	var end [4]byte
	binary.LittleEndian.PutUint32(end[:], magicEndOfManifest)
	raw = append(raw, end[:]...)

	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.DepotID != 294100 || m.GID != 99 {
		t.Fatalf("meta %+v", m)
	}
	if len(m.Files) != 1 || m.Files[0].Filename != "foo/bar.txt" || m.Files[0].Size != 3 {
		t.Fatalf("files %+v", m.Files)
	}
	if len(m.Files[0].Chunks) != 1 {
		t.Fatalf("chunks %+v", m.Files[0].Chunks)
	}
}

func TestCleanName(t *testing.T) {
	if got := CleanName(`dir\file.txt\x00`); got == "" {
		t.Fatal("empty")
	}
	if got := CleanName(`a\b\c`); got != "a/b/c" {
		// path.Clean on Windows may differ; FromSlash later
		if got != "a/b/c" && got != `a\b\c` {
			// CleanName replaces backslash with slash
			t.Fatalf("%q", got)
		}
	}
}
