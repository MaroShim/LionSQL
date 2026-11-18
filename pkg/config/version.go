package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Version is the authoritative single source of truth for the LionSQL release version.
// Can be overridden at build time via -ldflags "-X 'lionsql/pkg/config.Version=...'".
var Version = "0.93.0"

// Injected via ldflags or auto-detected via debug.ReadBuildInfo() or local .git/HEAD
var (
	BuildDate = "unknown"
	GitCommit = "dev"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && (GitCommit == "dev" || GitCommit == "unknown" || GitCommit == "") {
				GitCommit = setting.Value
				if len(GitCommit) > 12 {
					GitCommit = GitCommit[:12]
				}
			}
			if setting.Key == "vcs.time" && (BuildDate == "unknown" || BuildDate == "") {
				if t, err := time.Parse(time.RFC3339, setting.Value); err == nil {
					BuildDate = t.Local().Format("2006-01-02 15:04:05 MST")
				} else {
					BuildDate = setting.Value
				}
			}
		}
	}
	if GitCommit == "dev" || GitCommit == "unknown" || GitCommit == "" {
		if localCommit := detectLocalGitCommit(); localCommit != "" {
			GitCommit = localCommit
		}
	}
	if BuildDate == "unknown" || BuildDate == "" {
		BuildDate = time.Now().Format("2006-01-02 15:04:05 MST")
	}
}

// detectLocalGitCommit searches for a local .git directory from the working directory
// and executable path, reading .git/HEAD to resolve the short commit hash ([RULE:PlatformPrimitives]).
func detectLocalGitCommit() string {
	candidates := make([]string, 0, 2)
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}

	for _, startDir := range candidates {
		dir := startDir
		for i := 0; i < 6; i++ {
			gitPath := filepath.Join(dir, ".git")
			if stat, err := os.Stat(gitPath); err == nil {
				var gitDir string
				if stat.IsDir() {
					gitDir = gitPath
				} else {
					// Worktree or submodule where .git is a file ("gitdir: <path>")
					if data, err := os.ReadFile(gitPath); err == nil {
						line := strings.TrimSpace(string(data))
						if strings.HasPrefix(line, "gitdir:") {
							rel := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
							if filepath.IsAbs(rel) {
								gitDir = rel
							} else {
								gitDir = filepath.Join(dir, rel)
							}
						}
					}
				}
				if gitDir != "" {
					if commit := readGitCommitFromDir(gitDir); commit != "" {
						return commit
					}
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// readGitCommitFromDir parses .git/HEAD and resolves branch refs or packed-refs.
func readGitCommitFromDir(gitDir string) string {
	headPath := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "ref:") {
		// Detached HEAD: line itself is the commit hash
		if len(line) >= 7 {
			if len(line) > 12 {
				return line[:12]
			}
			return line
		}
		return ""
	}

	refName := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
	// 1. Try loose ref: .git/refs/...
	refPath := filepath.Join(gitDir, filepath.FromSlash(refName))
	if refData, err := os.ReadFile(refPath); err == nil {
		hash := strings.TrimSpace(string(refData))
		if len(hash) >= 7 {
			if len(hash) > 12 {
				return hash[:12]
			}
			return hash
		}
	}

	// 2. Try packed-refs: .git/packed-refs
	packedPath := filepath.Join(gitDir, "packed-refs")
	if packedData, err := os.ReadFile(packedPath); err == nil {
		lines := strings.Split(string(packedData), "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "#") || strings.HasPrefix(l, "^") || l == "" {
				continue
			}
			parts := strings.Fields(l)
			if len(parts) == 2 && parts[1] == refName {
				hash := parts[0]
				if len(hash) > 12 {
					return hash[:12]
				}
				return hash
			}
		}
	}
	return ""
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
