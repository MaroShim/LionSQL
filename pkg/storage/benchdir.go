package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// PrepareBenchmarkDir creates an isolated, freshly created unique benchmark directory
// inside parentDir using the specified prefix.
//
// The parentDir is treated as the base directory (created via os.MkdirAll if it does not exist).
// A brand-new unique subdirectory is created inside it via os.MkdirTemp.
//
// The returned cleanup closure removes ONLY the newly created unique child directory upon completion.
// The parent directory and any pre-existing files within it are strictly preserved.
func PrepareBenchmarkDir(parentDir, prefix string, keepDir bool) (dir string, cleanup func(), err error) {
	cleanParent := filepath.Clean(parentDir)
	if cleanParent == "" || cleanParent == "." || cleanParent == "/" || cleanParent == filepath.VolumeName(cleanParent)+"\\" {
		cleanParent = filepath.Join(".", "data")
	}

	// 1. Ensure parent base directory exists (never delete it!)
	if err := os.MkdirAll(cleanParent, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to prepare parent benchmark directory %q: %w", cleanParent, err)
	}

	// 2. Create an exclusively fresh, isolated child directory
	pattern := prefix + "_*"
	subDir, err := os.MkdirTemp(cleanParent, pattern)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create isolated benchmark directory in %q: %w", cleanParent, err)
	}

	// 3. Cleanup closure scoped strictly to the newly created subdirectory
	cleanupFunc := func() {
		if keepDir {
			fmt.Printf("[LionSQL Benchmark] Data directory preserved: %s\n", subDir)
			return
		}
		if rmErr := os.RemoveAll(subDir); rmErr != nil {
			fmt.Fprintf(os.Stderr, "[LionSQL Benchmark] Warning: failed to clean up benchmark directory %q: %v\n", subDir, rmErr)
		}
	}

	return subDir, cleanupFunc, nil
}
