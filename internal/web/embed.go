package web

import (
	"embed"
	"path/filepath"
)

//go:embed templates/*.html
var embeddedTemplates embed.FS

//go:embed static/*
var embeddedStatic embed.FS

// placeholder so server.go's embedFS mention is unused — keep single embed here.
type embedFS = embed.FS

func confAbsImpl(p string) (string, error) {
	return filepath.Abs(p)
}
