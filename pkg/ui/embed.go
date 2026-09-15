package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dashboard.html
var content embed.FS

// Handler returns an http.Handler that serves the embedded web admin dashboard.
func Handler() http.Handler {
	sub, err := fs.Sub(content, ".")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

// IndexHTML returns raw dashboard HTML bytes.
func IndexHTML() ([]byte, error) {
	return content.ReadFile("dashboard.html")
}
