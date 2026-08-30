//go:build windows

package dll

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	loadMu     sync.Mutex
	decompMu   sync.Mutex
	mod        windows.Handle
	baseAddr   uintptr
	fnDecomp   uintptr
	fnCompress uintptr
	fnPut      uintptr
	fnGet      uintptr
	loadedPath string
)

// Load loads steamclient64.dll (and dependencies) from path or default locations.
func Load(path string) error {
	loadMu.Lock()
	defer loadMu.Unlock()
	if mod != 0 {
		return nil
	}
	candidates := []string{}
	if path != "" {
		candidates = append(candidates, path)
	}
	if env := os.Getenv("SWD_STEAMCLIENT_DLL"); env != "" {
		candidates = append(candidates, env)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "steamclient64.dll"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "steamclient64.dll"))
		// Repo-root testdata/ holds the DLL as a test oracle. `go test` runs
		// with CWD = the tested package's dir, so probe a few relative forms.
		for _, up := range []string{".", "..", "../..", "../../.."} {
			candidates = append(candidates, filepath.Join(cwd, up, "testdata", "steamclient64.dll"))
		}
	}
	var last error
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			last = err
			continue
		}
		// Ensure sibling DLLs (tier0_s64, vstdlib_s64) resolve.
		dir := filepath.Dir(p)
		_ = windows.SetDllDirectory(dir)
		h, err := windows.LoadLibraryEx(
			p,
			0,
			windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS,
		)
		if err != nil {
			// fallback without flags
			h, err = windows.LoadLibrary(p)
		}
		if err != nil {
			last = err
			continue
		}
		mod = h
		baseAddr = uintptr(h)
		fnDecomp = baseAddr + RVADecompressAll
		fnCompress = baseAddr + RVACompressVSZa
		fnPut = baseAddr + RVAPutFunc
		fnGet = baseAddr + RVAGetFunc
		loadedPath = p
		return nil
	}
	if last == nil {
		last = fmt.Errorf("steamclient64.dll not found")
	}
	return fmt.Errorf("steamclient64.dll: %w (place next to swd.exe or set SWD_STEAMCLIENT_DLL)", last)
}

// LoadedPath returns the path of the loaded DLL, if any.
func LoadedPath() string {
	loadMu.Lock()
	defer loadMu.Unlock()
	return loadedPath
}

// CompressVSZa runs the DLL's own VSZa compressor (sub_138CECC90) over data
// and returns the finished "VSZa…zsv" container. Used by tests to produce
// ground-truth samples for the pure-Go decoder.
func CompressVSZa(data []byte) ([]byte, error) {
	if err := Load(""); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	decompMu.Lock()
	defer decompMu.Unlock()

	outBuf := make([]byte, MaxChunkSize)
	var buf CUtlBuffer
	buf.Data = uintptr(unsafe.Pointer(&outBuf[0]))
	buf.CbAllocated = int32(MaxChunkSize)
	buf.PutFunc = fnPut
	buf.GetFunc = fnGet

	// int fn(void* data, int size, CUtlBuffer* out)
	r1, _, callErr := syscall.SyscallN(
		fnCompress,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&buf)),
	)
	runtime.KeepAlive(data)
	runtime.KeepAlive(outBuf)
	runtime.KeepAlive(buf)
	_ = callErr
	if r1 != 1 {
		return nil, fmt.Errorf("VSZa compression failed (returned=%d)", r1)
	}
	n := int(buf.TellPut)
	if n <= 0 || n > MaxChunkSize {
		return nil, fmt.Errorf("compression produced invalid size %d", n)
	}
	out := make([]byte, n)
	copy(out, outBuf[:n])
	return out, nil
}

// Decompress runs the multi-format dispatcher on a post-AES chunk.
func Decompress(data []byte) ([]byte, error) {
	if err := Load(""); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty chunk")
	}

	decompMu.Lock()
	defer decompMu.Unlock()

	outBuf := make([]byte, MaxChunkSize)
	var buf CUtlBuffer
	buf.Data = uintptr(unsafe.Pointer(&outBuf[0]))
	buf.CbAllocated = int32(MaxChunkSize)
	buf.PutFunc = fnPut
	buf.GetFunc = fnGet

	// int fn(void* data, int size, CUtlBuffer* out, int maxSize, void* outFormat)
	r1, _, callErr := syscall.SyscallN(
		fnDecomp,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(MaxChunkSize),
		0,
	)
	runtime.KeepAlive(data)
	runtime.KeepAlive(outBuf)
	runtime.KeepAlive(buf)

	// SyscallN sets errno-style last error even on success sometimes; only
	// trust the return code from the Steam function.
	_ = callErr
	if r1 != 1 {
		mag := ""
		if len(data) >= 4 {
			mag = fmt.Sprintf("%x", data[:4])
		}
		return nil, fmt.Errorf("decompression failed (format=%s, returned=%d)", mag, r1)
	}
	n := int(buf.TellPut)
	if n <= 0 || n > MaxChunkSize {
		return nil, fmt.Errorf("decompression produced invalid size %d", n)
	}
	out := make([]byte, n)
	copy(out, outBuf[:n])
	return out, nil
}
