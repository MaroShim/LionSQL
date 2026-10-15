package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/migrate"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/storage/wiredtiger"
)

const version = "0.20.0"

func printUsage() {
	fmt.Printf(`lionsql v%s - High-Concurrency Embedded OLTP Database CLI

Usage:
  lionsql <command> [arguments...]

Commands:
  migrate    Declarative database schema migration management
  sql        Execute one-off SQL statements against the local database
  version    Display version and engine information
  help       Show this help message

Migrate Subcommands:
  lionsql migrate new <name> [-dir=./migrations]
      Generate a new timestamped migration pair (<name>.up.sql / <name>.down.sql)

  lionsql migrate up [-dir=./migrations] [-data=./data] [-engine=wiredtiger|memory] [-steps=N]
      Apply pending migrations to the database

  lionsql migrate down [-dir=./migrations] [-data=./data] [-engine=wiredtiger|memory] [-steps=N]
      Roll back the most recently applied migration(s)

  lionsql migrate status [-dir=./migrations] [-data=./data] [-engine=wiredtiger|memory]
      Display the current deployment status of all discovered migrations

Examples:
  lionsql migrate new create_users_table
  lionsql migrate up -data=./data
  lionsql migrate status
  lionsql sql "SELECT * FROM sys.databases;" -data=./data
`, version)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	cmd := strings.ToLower(os.Args[1])
	switch cmd {
	case "migrate":
		if err := runMigrate(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "sql":
		if err := runSQL(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Printf("lionsql version v%s\n", version)
		fmt.Println("Database Engine: SQLion High-Concurrency Embedded OLTP")
		fmt.Println("Storage Backends: WiredTiger (Lock-free B+Tree MVCC), In-Memory OCC")
		fmt.Println("Wire Protocols:   PostgreSQL v3 (:5433), TDS 7.x/8.0 (:1433), HTTP REST (:8090)")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runMigrate(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: lionsql migrate <new|up|down|status> [flags]")
		return nil
	}

	subcmd := strings.ToLower(args[0])
	subargs := args[1:]

	switch subcmd {
	case "new", "create":
		return runMigrateNew(subargs)
	case "up":
		return runMigrateUp(subargs)
	case "down":
		return runMigrateDown(subargs)
	case "status":
		return runMigrateStatus(subargs)
	default:
		return fmt.Errorf("unknown migrate subcommand %q (expected: new, up, down, status)", subcmd)
	}
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

func runMigrateNew(args []string) error {
	fs := flag.NewFlagSet("migrate new", flag.ContinueOnError)
	migrationsDir := fs.String("dir", "migrations", "Path to migrations directory")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	nonFlags := fs.Args()
	if len(nonFlags) == 0 {
		return fmt.Errorf("migration name is required (e.g. lionsql migrate new create_users)")
	}
	name := nonFlags[0]

	mf, err := migrate.Create(*migrationsDir, name)
	if err != nil {
		return err
	}

	fmt.Printf("[lionsql] Created migration pair (%s):\n", mf.Version)
	fmt.Printf("  Up:   %s\n", mf.UpPath)
	fmt.Printf("  Down: %s\n", mf.DownPath)
	return nil
}

func runMigrateUp(args []string) error {
	fs := flag.NewFlagSet("migrate up", flag.ContinueOnError)
	migrationsDir := fs.String("dir", "migrations", "Path to migrations directory")
	dataDir := fs.String("data", "data", "Path to database data directory")
	fs.StringVar(dataDir, "data-dir", "data", "Path to database data directory")
	engineType := fs.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	steps := fs.Int("steps", 0, "Number of migrations to apply (default: 0 = all pending)")
	fs.IntVar(steps, "n", 0, "Number of migrations to apply (default: 0 = all pending)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	eng, coord, err := openCoordinator(*dataDir, *engineType)
	if err != nil {
		return err
	}
	defer eng.Close()

	ctx := context.Background()
	fmt.Printf("[lionsql] Applying migrations from %s...\n", *migrationsDir)

	applied, err := migrate.Up(ctx, coord, *migrationsDir, *steps)
	if err != nil {
		return err
	}

	if len(applied) == 0 {
		fmt.Println("[lionsql] No pending migrations. Database schema is up to date.")
		return nil
	}

	for _, rec := range applied {
		fmt.Printf("  [%s] %s (%dms)\n", rec.Version, rec.Name, rec.DurationMS)
	}
	fmt.Printf("[lionsql] Successfully applied %d migration(s).\n", len(applied))
	return nil
}

func runMigrateDown(args []string) error {
	fs := flag.NewFlagSet("migrate down", flag.ContinueOnError)
	migrationsDir := fs.String("dir", "migrations", "Path to migrations directory")
	dataDir := fs.String("data", "data", "Path to database data directory")
	fs.StringVar(dataDir, "data-dir", "data", "Path to database data directory")
	engineType := fs.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	steps := fs.Int("steps", 1, "Number of migrations to revert (default: 1)")
	fs.IntVar(steps, "n", 1, "Number of migrations to revert (default: 1)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	eng, coord, err := openCoordinator(*dataDir, *engineType)
	if err != nil {
		return err
	}
	defer eng.Close()

	ctx := context.Background()
	fmt.Printf("[lionsql] Rolling back migrations from %s...\n", *migrationsDir)

	reverted, err := migrate.Down(ctx, coord, *migrationsDir, *steps)
	if err != nil {
		return err
	}

	if len(reverted) == 0 {
		fmt.Println("[lionsql] No applied migrations to revert.")
		return nil
	}

	for _, rec := range reverted {
		fmt.Printf("  [%s] %s (%dms)\n", rec.Version, rec.Name, rec.DurationMS)
	}
	fmt.Printf("[lionsql] Successfully rolled back %d migration(s).\n", len(reverted))
	return nil
}

func runMigrateStatus(args []string) error {
	fs := flag.NewFlagSet("migrate status", flag.ContinueOnError)
	migrationsDir := fs.String("dir", "migrations", "Path to migrations directory")
	dataDir := fs.String("data", "data", "Path to database data directory")
	fs.StringVar(dataDir, "data-dir", "data", "Path to database data directory")
	engineType := fs.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	eng, coord, err := openCoordinator(*dataDir, *engineType)
	if err != nil {
		return err
	}
	defer eng.Close()

	ctx := context.Background()
	statuses, err := migrate.Status(ctx, coord, *migrationsDir)
	if err != nil {
		return err
	}

	if len(statuses) == 0 {
		fmt.Printf("[lionsql] No migrations found in directory %s\n", *migrationsDir)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "VERSION\tNAME\tSTATUS\tAPPLIED AT\tDURATION")
	for _, s := range statuses {
		statusStr := "Pending"
		appliedAtStr := "-"
		durStr := "-"
		if s.Applied {
			statusStr = "Applied"
			appliedAtStr = s.AppliedAt
			durStr = fmt.Sprintf("%dms", s.DurationMS)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Version, s.Name, statusStr, appliedAtStr, durStr)
	}
	w.Flush()
	return nil
}

func runSQL(args []string) error {
	fs := flag.NewFlagSet("sql", flag.ContinueOnError)
	dataDir := fs.String("data", "data", "Path to database data directory")
	fs.StringVar(dataDir, "data-dir", "data", "Path to database data directory")
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
