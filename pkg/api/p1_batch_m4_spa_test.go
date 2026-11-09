package api

import (
	"testing"
)

func TestP1_BatchM4_SPAAssetsSubfolders(t *testing.T) {
	// Defect 13-4: Standard SPA build asset subfolders (/assets/*, /static/*) should be allowed,
	// while path traversal (..) and hidden files (.env, .git) remain strictly forbidden.
	allowed := []string{
		"/assets/index-D7b3.js",
		"assets/style-A1b2.css",
		"/static/media/logo.svg",
		"/js/bundle.js",
		"/css/app.css",
	}

	for _, p := range allowed {
		if !isSafeFlatAsset(p) {
			t.Errorf("expected isSafeFlatAsset(%q) to be true for standard SPA subfolder asset, got false", p)
		}
	}

	disallowed := []string{
		"/assets/../secret.txt",
		"/../etc/passwd",
		"/assets/.hidden.js",
		"/.env",
		"/assets/.git/config",
	}

	for _, p := range disallowed {
		if isSafeFlatAsset(p) {
			t.Errorf("expected isSafeFlatAsset(%q) to be false for unsafe path, got true", p)
		}
	}
}
