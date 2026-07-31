package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogNoColor(t *testing.T) {
	var buf bytes.Buffer
	off := false
	l, err := New(Options{Color: &off, Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	l.Stage("INIT", "Connecting")
	l.OK("done")
	l.Err("boom")
	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Fatalf("unexpected ANSI in %q", out)
	}
	if !strings.Contains(out, "[INIT] Connecting") || !strings.Contains(out, "OK  done") || !strings.Contains(out, "FAIL boom") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestStripANSI(t *testing.T) {
	s := "\033[32mOK\033[0m hi"
	if got := StripANSI(s); got != "OK hi" {
		t.Fatalf("got %q", got)
	}
}
