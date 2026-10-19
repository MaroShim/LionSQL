package main

import (
	"path/filepath"
	"testing"
)

func TestResolveDefaultDataDirFromExe(t *testing.T) {
	tests := []struct {
		name     string
		exePath  string
		expected string
	}{
		{
			name:     "binary located inside bin folder",
			exePath:  "/tmp/sqlion_app/bin/lionsql",
			expected: "/tmp/sqlion_app/data",
		},
		{
			name:     "binary located inside nested bin folder",
			exePath:  "/opt/sqlion/bin/lionsql",
			expected: "/opt/sqlion/data",
		},
		{
			name:     "binary located directly in root or other dir",
			exePath:  "/usr/local/lionsql",
			expected: "data",
		},
		{
			name:     "relative bin path",
			exePath:  filepath.Join("bin", "lionsql"),
			expected: "data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := resolveDefaultDataDirFromExe(tc.exePath)
			if actual != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}
