package web

import (
	"fmt"
	"html/template"
	"sync"
)

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"trunc": func(s string, n int) string {
			if n <= 0 || len(s) <= n {
				return s
			}
			// Avoid mid-rune cuts for ASCII-heavy Steam text; still byte-safe for UI.
			return s[:n] + "…"
		},
	}
}

var (
	tmplOnce sync.Once
	tmplErr  error
	tmplMap  map[string]*template.Template
)

func parseTemplates() (map[string]*template.Template, error) {
	tmplOnce.Do(func() {
		pages := []string{"index.html", "detail.html", "downloads.html"}
		tmplMap = make(map[string]*template.Template, len(pages))
		for _, page := range pages {
			t, err := template.New("base").Funcs(templateFuncs()).ParseFS(
				embeddedTemplates,
				"templates/base.html",
				"templates/"+page,
			)
			if err != nil {
				tmplErr = fmt.Errorf("parse %s: %w", page, err)
				return
			}
			tmplMap[page] = t
		}
	})
	return tmplMap, tmplErr
}
