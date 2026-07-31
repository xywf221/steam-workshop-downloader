package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMaybeInsertDownload(t *testing.T) {
	cases := []struct {
		in        []string
		wantFirst string
		lenDelta  int
	}{
		{[]string{"294100", "123"}, "download", 1},
		{[]string{"download", "294100", "123"}, "download", 0},
		{[]string{"version"}, "version", 0},
		{[]string{"--help"}, "--help", 0},
		{[]string{"web"}, "web", 0},
	}
	for _, c := range cases {
		out := maybeInsertDownload(c.in)
		if out[0] != c.wantFirst {
			t.Errorf("maybeInsertDownload(%v)[0]=%q want %q", c.in, out[0], c.wantFirst)
		}
		if len(out) != len(c.in)+c.lenDelta {
			t.Errorf("len out=%d want %d", len(out), len(c.in)+c.lenDelta)
		}
	}
}

func TestVersionCmd(t *testing.T) {
	// Capture stdout from VersionCmd via Run path is heavy; call Run on VersionCmd.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := (&VersionCmd{}).Run()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if !strings.Contains(buf.String(), "Steam Workshop Downloader") {
		t.Fatalf("version output %q", buf.String())
	}
}

func TestWebStub(t *testing.T) {
	err := (&WebCmd{}).Run()
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not implemented, got %v", err)
	}
}
