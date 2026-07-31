//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

func isTerminal(f *os.File) bool {
	var mode uint32
	err := windows.GetConsoleMode(windows.Handle(f.Fd()), &mode)
	return err == nil
}

// EnableVT turns on virtual terminal processing for stdout/stderr on Windows
// so ANSI colours render in modern consoles. Best-effort; returns whether any
// handle was updated.
func EnableVT() bool {
	ok := false
	for _, h := range []windows.Handle{windows.Stdout, windows.Stderr} {
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			continue
		}
		if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err == nil {
			ok = true
		}
	}
	return ok
}
