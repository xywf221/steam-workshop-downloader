//go:build !windows

package ui

import "os"

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// EnableVT is a no-op on non-Windows.
func EnableVT() bool { return false }
