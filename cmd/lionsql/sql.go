package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/storage/wiredtiger"
)

func runSQL(args []string) error {
	fs := flag.NewFlagSet("sql", flag.ContinueOnError)
	dataDir := fs.String("data", "data", "Path to database data directory")
	fs.StringVar(dataDir, "data-dir", "data", "Path to database data directory")
	fs.StringVar(dataDir, "dir", "data", "Path to database data directory")
	engineType := fs.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	queries := fs.Args()
	if len(queries) == 0 {
		return fmt.Errorf("missing SQL statement (e.g. lionsql sql \"SELECT * FROM users\")")
	}
	query := strings.Join(queries, " ")

	eng, coord, err := openCoordinator(*dataDir, *engineType)
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
	cleanDir := filepath.Clean(dataDir)
	var eng storage.Engine

	switch engineType {
	case "wiredtiger":
		eng = wiredtiger.New()
		if err := eng.Open(cleanDir); err != nil {
			return nil, nil, fmt.Errorf("failed to open wiredtiger storage at %q: %w", cleanDir, err)
		}
	case "memory":
		memEng := memengine.New()
		opts := memengine.DefaultOptions(cleanDir)
		if err := memEng.OpenWithOptions(opts); err != nil {
			return nil, nil, fmt.Errorf("failed to open memory storage: %w", err)
		}
		eng = memEng
	default:
		return nil, nil, fmt.Errorf("unknown engine %q (expected wiredtiger or memory)", engineType)
	}

	cat := catalog.New(eng)
	_ = cat.SetPersistenceDir(cleanDir)

	coord := engine.NewCoordinator(cat, eng, engine.Config{
		DefaultWorkMem: 64 * 1024 * 1024,
	})

	return eng, coord, nil
}
