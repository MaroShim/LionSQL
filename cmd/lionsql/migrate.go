package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"sqlion/pkg/migrate"
)

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
