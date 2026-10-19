package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dashboard.html
var content embed.FS

// Handler returns an http.Handler that serves the embedded web admin dashboard with no-cache headers.
func Handler() http.Handler {
	sub, err := fs.Sub(content, ".")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		fileServer.ServeHTTP(w, r)
	})
}

// IndexHTML returns raw dashboard HTML bytes.
func IndexHTML() ([]byte, error) {
	return content.ReadFile("dashboard.html")
}
