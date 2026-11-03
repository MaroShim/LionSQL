package config

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"time"
)

// Version is the authoritative single source of truth for the LionSQL release version.
const Version = "0.90.0"

// Injected via ldflags or auto-detected via debug.ReadBuildInfo()
var (
	BuildDate = ""
	GitCommit = ""
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && GitCommit == "" {
				GitCommit = setting.Value
				if len(GitCommit) > 12 {
					GitCommit = GitCommit[:12]
				}
			}
			if setting.Key == "vcs.time" && BuildDate == "" {
				if t, err := time.Parse(time.RFC3339, setting.Value); err == nil {
					BuildDate = t.Local().Format("2006-01-02 15:04:05 MST")
				} else {
					BuildDate = setting.Value
				}
			}
		}
	}
	if BuildDate == "" {
		BuildDate = "unknown"
	}
	if GitCommit == "" {
		GitCommit = "dev"
	}
}

// CompactVersion returns a single-line summary (Option B style).
// Example: "(built: 2026-10-24 11:28:38 KST, commit: ad0bb2c, go1.27.1 darwin/arm64) [tags: wiredtiger]"
func CompactVersion() string {
	return fmt.Sprintf("(built: %s, commit: %s, %s %s/%s) [tags: %s]",
		BuildDate, GitCommit, runtime.Version(), runtime.GOOS, runtime.GOARCH, BuildTags())
}

// DetailedVersion returns a structured multi-line report (Option A style).
func DetailedVersion() string {
	return fmt.Sprintf("LionSQL v%s (%s/%s)\n"+
		"  Build Time:     %s\n"+
		"  Git Commit:     %s\n"+
		"  Go Runtime:     %s\n"+
		"  Storage Engine: WiredTiger (Lock-free B+Tree MVCC), In-Memory OCC\n"+
		"  Wire Protocols: PostgreSQL v3 (:5433), TDS 7.x/8.0 (:1433), HTTP REST (:8090)",
		Version, runtime.GOOS, runtime.GOARCH,
		BuildDate, GitCommit, runtime.Version())
}
