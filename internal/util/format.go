// Package util holds small pure helpers shared across packages.
package util

import "fmt"

// FormatSize renders a byte count as B / KB / MB (one decimal for KB+).
func FormatSize(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// FormatDuration renders seconds as H:MM:SS (minutes/seconds always 2 digits).
func FormatDuration(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	s := int(seconds)
	h := s / 3600
	rem := s % 3600
	m := rem / 60
	sec := rem % 60
	return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
}
