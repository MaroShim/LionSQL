package demo

import (
	_ "embed"
	"os"
)

//go:embed demo_seed.sql
var DefaultSeedSQL string

// GetSeedSQL reads scripts/demo_seed.sql from disk if present; otherwise returns embedded DefaultSeedSQL.
func GetSeedSQL() string {
	paths := []string{
		"scripts/demo_seed.sql",
		"../scripts/demo_seed.sql",
		"../../scripts/demo_seed.sql",
	}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return DefaultSeedSQL
}
