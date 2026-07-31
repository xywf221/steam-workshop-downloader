//go:build ignore

package main

import (
	"fmt"
	"unsafe"

	"github.com/xywf221/steam-workshop-downloader/internal/dll"
)

func main() {
	var b dll.CUtlBuffer
	fmt.Println("size", unsafe.Sizeof(b))
	fmt.Println("Data", unsafe.Offsetof(b.Data))
	fmt.Println("TellPut", unsafe.Offsetof(b.TellPut))
	fmt.Println("Pad", unsafe.Offsetof(b.Pad))
	fmt.Println("PutFunc", unsafe.Offsetof(b.PutFunc))
	fmt.Println("GetFunc", unsafe.Offsetof(b.GetFunc))
}
