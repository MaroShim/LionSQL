package api

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// isSafeFlatAsset checks whether the requested asset filename complies with path safety invariants:
// 1. No path traversal elements ("..")
// 2. No dot-prefixed hidden files or directories (".env", ".git", etc.)
// 3. Subdirectory paths are safely permitted (e.g. "/assets/*.js", "/static/*") provided they do not escape the root
func isSafeFlatAsset(requestedPath string) bool {
	clean := strings.TrimPrefix(requestedPath, "/")
	if clean == "" || clean == "index.html" {
		return true
	}

	// 1. Prevent directory traversal
	if strings.Contains(clean, "..") {
		return false
	}

	// 2. Reject dot-prefixed hidden files or hidden segments (.env, .git, etc.)
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}

	return true
}

// BindAppHandler inspects the configured app path and determines the serving strategy:
// Single HTML file mode vs Flat directory SPA mode.
func BindAppHandler(appPath string) (http.Handler, error) {
	cleanPath := filepath.Clean(appPath)
	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open app path %q: %w", appPath, err)
	}

	if fi.IsDir() {
		return NewFlatDirectoryHandler(cleanPath)
	}
	return NewSingleFileHandler(cleanPath)
}

// singleFileHandler serves a single standalone HTML file for all non-API routes.
type singleFileHandler struct {
	filePath string
}

// NewSingleFileHandler creates a handler that serves a single HTML file for all routes.
func NewSingleFileHandler(filePath string) (http.Handler, error) {
	fi, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat single app file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("app file %q must be a regular file", filePath)
	}
	return &singleFileHandler{filePath: filePath}, nil
}

func (h *singleFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(h.filePath)
	if err != nil {
		http.Error(w, "Failed to read app file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "Failed to stat app file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// flatDirectoryHandler serves static assets from a flat directory and falls back to index.html for SPA routes.
type flatDirectoryHandler struct {
	dirPath   string
	indexPath string
}

// NewFlatDirectoryHandler creates an SPA handler adhering to the Flat Directory Invariant.
func NewFlatDirectoryHandler(dirPath string) (http.Handler, error) {
	indexPath := filepath.Join(dirPath, "index.html")
	fi, err := os.Lstat(indexPath)
	if err != nil {
		return nil, fmt.Errorf("flat directory %q must contain index.html: %w", dirPath, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("index.html in %q must be a regular file", dirPath)
	}

	return &flatDirectoryHandler{
		dirPath:   dirPath,
		indexPath: indexPath,
	}, nil
}

func (h *flatDirectoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Path
	clean := strings.TrimPrefix(reqPath, "/")

	// 1. Root or explicit index.html request -> serve index.html
	if clean == "" || clean == "index.html" {
		h.serveIndex(w, r)
		return
	}

	// 2. Enforce Flat Directory Invariant: Reject traversal, hidden files, or non-flat assets
	if !isSafeFlatAsset(clean) {
		http.NotFound(w, r)
		return
	}

	// 3. For asset files (with an extension), attempt to serve from disk
	if filepath.Ext(clean) != "" {
		targetPath := filepath.Join(h.dirPath, filepath.FromSlash(clean))
		rel, relErr := filepath.Rel(h.dirPath, targetPath)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			http.NotFound(w, r)
			return
		}
		fi, err := os.Lstat(targetPath)
		if err == nil && fi.Mode().IsRegular() {
			ext := filepath.Ext(clean)
			if ctype := mime.TypeByExtension(ext); ctype != "" {
				w.Header().Set("Content-Type", ctype)
			}
			http.ServeFile(w, r, targetPath)
			return
		}
		// Asset not found on disk -> 404 (avoid feeding HTML into script/style tags)
		http.NotFound(w, r)
		return
	}

	// 4. Clean URL without extension (HTML5 History API navigation e.g. /dashboard, /users/1)
	// Fall back to index.html
	h.serveIndex(w, r)
}

func (h *flatDirectoryHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(h.indexPath)
	if err != nil {
		http.Error(w, "Failed to read index.html", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "Failed to stat index.html", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", fi.ModTime(), f)
}
