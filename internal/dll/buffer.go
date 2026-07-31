package dll

// CUtlBuffer mirrors the ctypes layout used by the working Python loader
// (src/swd/dll/buffer.py). On Win64 ctypes.sizeof == 56 with:
//
//	data@0 putFunc@40 getFunc@48
type CUtlBuffer struct {
	Data        uintptr  // 0x00
	CbAllocated int32    // 0x08
	Reserved    int32    // 0x0C
	TellGet     int32    // 0x10
	TellPut     int32    // 0x14  ← written size
	Flags       int32    // 0x18
	Error       int32    // 0x1C
	Pad         [4]byte  // 0x20
	// Go aligns PutFunc to 0x28? No — after [4]byte at 0x20, next is 0x24;
	// uintptr needs 8-align so compiler inserts 4 pad → 0x28.
	// But Python has putFunc at 40 (0x28). Wait Python said putFunc 40.
	// 32+4pad field = 36, +4 align = 40. Yes putFunc@40 getFunc@48 size 56.
	PutFunc uintptr
	GetFunc uintptr
}

// SizeofCUtlBuffer is the Win64 ctypes size.
const SizeofCUtlBuffer = 56
