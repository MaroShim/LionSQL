package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"lionsql/pkg/catalog"
	"lionsql/pkg/config"
	"lionsql/pkg/engine"
	"lionsql/pkg/pathutil"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
)

// memoryPerformanceDeprecationNotice returns a deprecation warning message when MemoryPerformance is selected,
// or an empty string otherwise. MemoryPerformance remains functional in v1.0 and is scheduled for post-1.0 removal.
func memoryPerformanceDeprecationNotice(mode memengine.MemoryMode) string {
	if mode != memengine.MemoryPerformance {
		return ""
	}
	return "[LionSQL] DEPRECATED: -memory=performance is deprecated and scheduled for removal post-1.0; use -memory=compact (default)."
}

func warnIfMemoryPerformanceDeprecated(mode memengine.MemoryMode) {
	if msg := memoryPerformanceDeprecationNotice(mode); msg != "" {
		log.Print(msg)
	}
}

func formatMemoryBannerValue(mem string) string {
	trimmed := strings.TrimSpace(mem)
	if trimmed != "" {
		if mode, err := memengine.ParseMemoryMode(trimmed); err == nil && mode == memengine.MemoryPerformance {
			return trimmed + " [DEPRECATED: use compact]"
		}
	}
	return mem
}

func runSQL(args []string) error {
	fs := flag.NewFlagSet("sql", flag.ContinueOnError)
	dataDir := fs.String("data", "data", "Path to database data directory")
	fs.StringVar(dataDir, "data-dir", "data", "Path to database data directory")
	fs.StringVar(dataDir, "dir", "data", "Path to database data directory")
	engineType := fs.String("engine", config.DefaultEngine(), "Storage engine backend: wiredtiger or memory")
	diskType := fs.String("disk", "", "Low-level disk persistence backend (compact, wiredtiger)")
	memoryType := fs.String("memory", config.DefaultMemory(), "In-memory tuple representation: compact (default) or performance [Deprecated]")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	queries := fs.Args()
	if len(queries) == 0 {
		return fmt.Errorf("missing SQL statement (e.g. lionsql sql \"SELECT * FROM users\")")
	}
	query := strings.Join(queries, " ")

	resolvedDisk := *diskType
	if resolvedDisk == "" {
		if strings.ToLower(*engineType) == "memory" {
			resolvedDisk = "compact"
		} else if *engineType != "" {
			resolvedDisk = *engineType
		} else {
			resolvedDisk = config.DefaultDisk()
		}
	} else if strings.ToLower(resolvedDisk) == "memory" {
		resolvedDisk = "compact"
	}

	eng, coord, err := openCoordinatorWithOptions(*dataDir, resolvedDisk, *memoryType)
	if err != nil {
		return err
	}
	defer eng.Close()

	res, err := coord.ExecuteSQL(query)
	if err != nil {
		return err
	}
	if res.Error != "" {
		return fmt.Errorf("sql execution error: %s", res.Error)
	}

	if len(res.Columns) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, strings.Join(res.Columns, "\t"))
		for _, row := range res.Rows {
			var vals []string
			for _, col := range res.Columns {
				v := row[col]
				if v == nil {
					vals = append(vals, "NULL")
				} else {
					vals = append(vals, fmt.Sprintf("%v", v))
				}
			}
			fmt.Fprintln(w, strings.Join(vals, "\t"))
		}
		w.Flush()
	}

	fmt.Printf("(%d row(s) affected, %d µs)\n", res.RowsAffected, res.DurationUS)
	return nil
}

func reorderArgs(args []string) []string {
	var flags []string
	var nonFlags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			nonFlags = append(nonFlags, arg)
		}
	}
	return append(flags, nonFlags...)
}

func openCoordinator(dataDir, engineType string) (storage.Engine, *engine.Coordinator, error) {
	return openCoordinatorWithOptions(dataDir, engineType, config.DefaultMemory())
}

func openCoordinatorWithOptions(dataDir, diskType, memoryType string) (storage.Engine, *engine.Coordinator, error) {
	cleanDir := filepath.Clean(dataDir)
	if err := pathutil.ValidateDir(cleanDir, false); err != nil {
		return nil, nil, fmt.Errorf("data directory validation error: %w", err)
	}
	var eng storage.Engine

	pidFile := filepath.Join(cleanDir, "lionsql.pid")
	daemonActive := false
	if pid, err := readPidFile(pidFile); err == nil && isProcessAlive(pid) {
		daemonActive = true
	}

	normDisk := strings.ToLower(strings.TrimSpace(diskType))
	switch normDisk {
	case "wiredtiger", "wt":
		eng = wiredtiger.New()
		if err := eng.Open(cleanDir); err != nil {
			if daemonActive {
				return nil, nil, fmt.Errorf("failed to open wiredtiger storage at %q (LionSQL daemon is currently running on this data directory; connect via HTTP/PGWire/TDS or stop the daemon first): %w", cleanDir, err)
			}
			return nil, nil, fmt.Errorf("failed to open wiredtiger storage at %q: %w", cleanDir, err)
		}
	case "compact", "none", "memory", "mem":
		memEng := memengine.New()
		opts := memengine.DefaultOptions(cleanDir)
		if mode, err := memengine.ParseMemoryMode(memoryType); err == nil {
			opts.Mode = mode
			warnIfMemoryPerformanceDeprecated(mode)
		}
		if err := memEng.OpenWithOptions(opts); err != nil {
			if daemonActive {
				return nil, nil, fmt.Errorf("failed to open memory storage at %q (LionSQL daemon is currently running on this data directory; connect via HTTP/PGWire/TDS or stop the daemon first): %w", cleanDir, err)
			}
			return nil, nil, fmt.Errorf("failed to open memory storage: %w", err)
		}
		eng = memEng
	default:
		return nil, nil, fmt.Errorf("unknown disk storage %q (expected compact or wiredtiger)", diskType)
	}

	cat := catalog.New(eng)
	_ = cat.SetPersistenceDir(cleanDir)

	coord := engine.NewCoordinator(cat, eng, engine.Config{
		DefaultWorkMem: 64 * 1024 * 1024,
	})

	return eng, coord, nil
}
