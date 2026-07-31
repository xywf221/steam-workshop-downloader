// Package manifest parses Steam depot manifests (protobuf payload + metadata).
package manifest

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/crypto"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/pb"
)

// Magic numbers from ValvePython steam.core.manifest.DepotManifest.
const (
	magicPayload       = 0x71F617D0
	magicMetadata      = 0x1F4812BE
	magicSignature     = 0x1B81B817
	magicEndOfManifest = 0x32C415AB

	// EDepotFileFlag.Directory
	flagDirectory = 1 << 6 // 64 — verify against enum; Python uses EDepotFileFlag.Directory
)

// Manifest is a parsed depot manifest.
type Manifest struct {
	AppID   uint32
	DepotID uint32
	GID     uint64
	Name    string // display name (workshop title)

	FilenamesEncrypted bool
	Files              []File
}

// File is one mapping entry.
type File struct {
	Filename    string
	Size        uint64
	Flags       uint32
	SHAContent  []byte // 20 bytes
	SHAFilename []byte
	LinkTarget  string
	Chunks      []Chunk
}

// IsDirectory reports the Directory flag.
func (f File) IsDirectory() bool {
	// EDepotFileFlag.Directory = 64 (0x40) in SteamKit
	return f.Flags&0x40 != 0
}

// IsFile is the inverse of IsDirectory (and not a pure symlink-only entry).
func (f File) IsFile() bool { return !f.IsDirectory() }

// Chunk is one content chunk descriptor.
type Chunk struct {
	SHA            []byte // 20 bytes raw
	CRC            uint32
	Offset         uint64
	OriginalSize   uint32
	CompressedSize uint32
}

// Parse deserializes a (possibly zip-wrapped) depot manifest.
func Parse(data []byte) (*Manifest, error) {
	// Optional zip wrapper
	if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil && len(zr.File) > 0 {
		rc, err := zr.File[0].Open()
		if err == nil {
			plain, err2 := io.ReadAll(rc)
			rc.Close()
			if err2 == nil {
				data = plain
			}
		}
	}

	r := bytes.NewReader(data)
	readSection := func(wantMagic uint32) ([]byte, error) {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		magic := binary.LittleEndian.Uint32(hdr[0:4])
		length := binary.LittleEndian.Uint32(hdr[4:8])
		if magic != wantMagic {
			return nil, fmt.Errorf("manifest magic got 0x%X want 0x%X", magic, wantMagic)
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}

	payload, err := readSection(magicPayload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	metaRaw, err := readSection(magicMetadata)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	if _, err := readSection(magicSignature); err != nil {
		return nil, fmt.Errorf("signature: %w", err)
	}
	var endMagic uint32
	if err := binary.Read(r, binary.LittleEndian, &endMagic); err != nil {
		return nil, err
	}
	if endMagic != magicEndOfManifest {
		return nil, fmt.Errorf("end magic 0x%X", endMagic)
	}

	m := &Manifest{}
	if err := parseMetadata(metaRaw, m); err != nil {
		return nil, err
	}
	files, err := parsePayload(payload)
	if err != nil {
		return nil, err
	}
	m.Files = files
	return m, nil
}

func parseMetadata(data []byte, m *Manifest) error {
	fields, err := pb.Decode(data)
	if err != nil {
		return err
	}
	for _, f := range fields {
		switch f.Number {
		case 1:
			m.DepotID = uint32(f.Varint)
		case 2:
			if f.Wire == pb.WireFixed64 {
				m.GID = f.Fixed64
			} else {
				m.GID = f.Varint
			}
		case 4:
			m.FilenamesEncrypted = f.Varint != 0
		}
	}
	return nil
}

func parsePayload(data []byte) ([]File, error) {
	fields, err := pb.Decode(data)
	if err != nil {
		return nil, err
	}
	var files []File
	for _, f := range fields {
		if f.Number != 1 || f.Wire != pb.WireBytes {
			continue
		}
		file, err := parseFileMapping(f.Bytes)
		if err != nil {
			continue
		}
		files = append(files, file)
	}
	return files, nil
}

func parseFileMapping(data []byte) (File, error) {
	fields, err := pb.Decode(data)
	if err != nil {
		return File{}, err
	}
	var file File
	for _, f := range fields {
		switch f.Number {
		case 1:
			file.Filename = string(f.Bytes)
		case 2:
			if f.Wire == pb.WireFixed64 {
				file.Size = f.Fixed64
			} else {
				file.Size = f.Varint
			}
		case 3:
			file.Flags = uint32(f.Varint)
		case 4:
			file.SHAFilename = f.Bytes
		case 5:
			file.SHAContent = f.Bytes
		case 6:
			if f.Wire == pb.WireBytes {
				if ch, err := parseChunk(f.Bytes); err == nil {
					file.Chunks = append(file.Chunks, ch)
				}
			}
		case 7:
			file.LinkTarget = string(f.Bytes)
		}
	}
	return file, nil
}

func parseChunk(data []byte) (Chunk, error) {
	fields, err := pb.Decode(data)
	if err != nil {
		return Chunk{}, err
	}
	var ch Chunk
	for _, f := range fields {
		switch f.Number {
		case 1:
			ch.SHA = f.Bytes
		case 2: // fixed32 crc
			if f.Wire == pb.WireFixed32 {
				ch.CRC = f.Fixed32
			} else {
				ch.CRC = uint32(f.Varint)
			}
		case 3:
			if f.Wire == pb.WireFixed64 {
				ch.Offset = f.Fixed64
			} else {
				ch.Offset = f.Varint
			}
		case 4:
			ch.OriginalSize = uint32(f.Varint)
		case 5:
			ch.CompressedSize = uint32(f.Varint)
		}
	}
	if len(ch.SHA) == 0 {
		return Chunk{}, fmt.Errorf("chunk missing sha")
	}
	return ch, nil
}

// DecryptFilenames decrypts base64-encoded encrypted filenames with the depot key.
func (m *Manifest) DecryptFilenames(depotKey []byte) error {
	if !m.FilenamesEncrypted {
		return nil
	}
	for i := range m.Files {
		f := &m.Files[i]
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(f.Filename))
		if err != nil {
			// try raw as already binary? unlikely
			return fmt.Errorf("filename b64: %w", err)
		}
		plain, err := crypto.SymmetricDecrypt(raw, depotKey)
		if err != nil {
			return fmt.Errorf("filename decrypt: %w", err)
		}
		f.Filename = string(plain)
		if f.LinkTarget != "" {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(f.LinkTarget))
			if err == nil {
				if p, err := crypto.SymmetricDecrypt(raw, depotKey); err == nil {
					f.LinkTarget = string(p)
				}
			}
		}
	}
	m.FilenamesEncrypted = false
	return nil
}

// CleanName converts a Steam filename (backslash, embedded NULs) to a slash path.
func CleanName(name string) string {
	// Steam often stores C-strings with trailing NULs inside protobuf bytes.
	if i := strings.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	return path.Clean(name)
}

// SortChunksByOffset sorts chunks ascending by offset.
func SortChunksByOffset(chunks []Chunk) {
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Offset < chunks[j].Offset })
}
