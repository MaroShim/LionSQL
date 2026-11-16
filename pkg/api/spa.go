package api

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// isSafeFlatAsset checks whether the requested asset path complies with path safety invariants:
// 1. No path traversal elements ("..")
// 2. No dot-prefixed hidden files or directories (".env", ".git", etc.)
// 3. Subdirectory paths are safely permitted (e.g. "/assets/*.js", "/static/*") provided they do not escape root
func isSafeFlatAsset(requestedPath string) bool {
	clean := strings.TrimPrefix(requestedPath, "/")
	return isSafePath(clean)
}

func isSafePath(clean string) bool {
	if clean == "" || clean == "index.html" {
		return true
	}
	if strings.Contains(clean, "..") {
		return false
	}
	if !fs.ValidPath(clean) {
		return false
	}
	// Reject dot-prefixed hidden files or hidden directory segments (.env, .git, etc.)
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
// Single HTML file mode vs Chroot Sandbox SPA directory mode.
func BindAppHandler(appPath string) (http.Handler, error) {
	cleanPath := filepath.Clean(appPath)
	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open app path %q: %w", appPath, err)
	}

	if fi.IsDir() {
		return NewSandboxSPAHandler(cleanPath)
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
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// SandboxSPAHandler hosts Single Page Applications (SPA) using Go standard os.DirFS chroot jail.
// It supports subdirectories (e.g. assets/*.js) while sandboxing path traversal and hidden files.
type SandboxSPAHandler struct {
	fsys      fs.FS
	dirPath   string
	indexPath string
}

// NewSandboxSPAHandler creates an SPA handler backed by Go standard os.DirFS.
func NewSandboxSPAHandler(dirPath string) (http.Handler, error) {
	indexPath := filepath.Join(dirPath, "index.html")
	fi, err := os.Lstat(indexPath)
	if err != nil {
		return nil, fmt.Errorf("app directory %q must contain index.html: %w", dirPath, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("index.html in %q must be a regular file", dirPath)
	}

	return &SandboxSPAHandler{
		fsys:      os.DirFS(dirPath),
		dirPath:   dirPath,
		indexPath: indexPath,
	}, nil
}

// NewFlatDirectoryHandler is a legacy alias for NewSandboxSPAHandler.
func NewFlatDirectoryHandler(dirPath string) (http.Handler, error) {
	return NewSandboxSPAHandler(dirPath)
}

func (h *SandboxSPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath := path.Clean("/" + r.URL.Path)
	clean := strings.TrimPrefix(reqPath, "/")

	// 1. Root or explicit index.html request -> serve index.html
	if clean == "" || clean == "index.html" {
		h.serveIndex(w, r)
		return
	}

	// 2. Security Firewall: Reject path traversal, invalid characters, or hidden files/dirs
	if !isSafePath(clean) {
		http.NotFound(w, r)
		return
	}

	// 3. For asset files (with an extension), attempt to serve from os.DirFS jail
	if path.Ext(clean) != "" {
		f, err := h.fsys.Open(clean)
		if err == nil {
			defer f.Close()
			if fi, sErr := f.Stat(); sErr == nil && fi.Mode().IsRegular() {
				if rsc, ok := f.(io.ReadSeeker); ok {
					if ctype := mime.TypeByExtension(path.Ext(clean)); ctype != "" {
						w.Header().Set("Content-Type", ctype)
					}
					if strings.HasPrefix(clean, "assets/") || strings.HasPrefix(clean, "static/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					http.ServeContent(w, r, fi.Name(), fi.ModTime(), rsc)
					return
				}
			}
		}
		// Asset not found on disk -> 404 (avoid feeding HTML into script/style tags)
		http.NotFound(w, r)
		return
	}

	// 4. Clean URL without extension (HTML5 History API navigation e.g. /dashboard, /users/1)
	// Fall back to index.html
	h.serveIndex(w, r)
}

func (h *SandboxSPAHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := h.fsys.Open("index.html")
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

	rsc, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "Failed to seek index.html", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	http.ServeContent(w, r, "index.html", fi.ModTime(), rsc)
}
