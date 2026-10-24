package config

import (
	"strings"
	"testing"
)

func TestVersion_CompactAndDetailed(t *testing.T) {
	if Version != "0.90.0" {
		t.Fatalf("expected version 0.90.0, got %s", Version)
	}

	compact := CompactVersion()
	if !strings.HasPrefix(compact, "(built: ") {
		t.Errorf("expected compact version to start with '(built: ', got %q", compact)
	}
	if !strings.Contains(compact, "commit: ") {
		t.Errorf("expected compact version to contain 'commit: ', got %q", compact)
	}
	if !strings.Contains(compact, "[tags: ") {
		t.Errorf("expected compact version to contain '[tags: ', got %q", compact)
	}

	detailed := DetailedVersion()
	expectedSnippets := []string{
		"LionSQL v0.90.0",
		"Build Time:",
		"Git Commit:",
		"Go Runtime:",
		"Storage Engine:",
		"Wire Protocols:",
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(detailed, snippet) {
			t.Errorf("expected detailed version to contain %q, got:\n%s", snippet, detailed)
		}
	}
}
