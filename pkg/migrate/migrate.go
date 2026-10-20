package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/engine"
)

var (
	migrationFileRegex = regexp.MustCompile(`^(\d{14})_(.+)\.(up|down)\.sql$`)
	nameSanitizeRegex  = regexp.MustCompile(`[^a-zA-Z0-9_]`)
)

// MigrationFile represents a pair of .up.sql and .down.sql migration files on disk.
type MigrationFile struct {
	Version  string `json:"version"`
	Name     string `json:"name"`
	UpPath   string `json:"up_path"`
	DownPath string `json:"down_path"`
}

// MigrationRecord represents an applied migration row recorded in _migrations table.
type MigrationRecord struct {
	Version    string `json:"version"`
	Name       string `json:"name"`
	AppliedAt  string `json:"applied_at"`
	DurationMS int64  `json:"duration_ms"`
}

// MigrationStatus represents migration deployment state for CLI inspection.
type MigrationStatus struct {
	Version    string `json:"version"`
	Name       string `json:"name"`
	Applied    bool   `json:"applied"`
	AppliedAt  string `json:"applied_at,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// Create generates a new pair of timestamped migration files:
// <timestamp>_<name>.up.sql and <timestamp>_<name>.down.sql in dir.
func Create(dir, name string) (*MigrationFile, error) {
	cleanDir := filepath.Clean(dir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create migrations directory %q: %w", cleanDir, err)
	}

	sanitizedName := strings.ToLower(strings.TrimSpace(name))
	sanitizedName = strings.ReplaceAll(sanitizedName, " ", "_")
	sanitizedName = strings.ReplaceAll(sanitizedName, "-", "_")
	sanitizedName = nameSanitizeRegex.ReplaceAllString(sanitizedName, "")

	if sanitizedName == "" {
		return nil, fmt.Errorf("migration name cannot be empty")
	}

	timestamp := time.Now().UTC().Format("20060102150405")
	for {
		matches, _ := filepath.Glob(filepath.Join(cleanDir, timestamp+"_*.sql"))
		if len(matches) == 0 {
			break
		}
		t, err := time.Parse("20060102150405", timestamp)
		if err != nil {
			break
		}
		timestamp = t.Add(1 * time.Second).Format("20060102150405")
	}

	baseName := fmt.Sprintf("%s_%s", timestamp, sanitizedName)

	upPath := filepath.Join(cleanDir, baseName+".up.sql")
	downPath := filepath.Join(cleanDir, baseName+".down.sql")

	upContent := fmt.Sprintf("-- Migration: %s (Up)\n-- Created at: %s UTC\n\n", sanitizedName, time.Now().UTC().Format(time.RFC3339))
	downContent := fmt.Sprintf("-- Migration: %s (Down)\n-- Created at: %s UTC\n\n", sanitizedName, time.Now().UTC().Format(time.RFC3339))

	if err := os.WriteFile(upPath, []byte(upContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to create up migration file %q: %w", upPath, err)
	}
	if err := os.WriteFile(downPath, []byte(downContent), 0644); err != nil {
		_ = os.Remove(upPath)
		return nil, fmt.Errorf("failed to create down migration file %q: %w", downPath, err)
	}

	return &MigrationFile{
		Version:  timestamp,
		Name:     sanitizedName,
		UpPath:   upPath,
		DownPath: downPath,
	}, nil
}

// Load reads and parses all migration files in dir, sorting chronologically by version.
func Load(dir string) ([]*MigrationFile, error) {
	cleanDir := filepath.Clean(dir)
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read migrations directory %q: %w", cleanDir, err)
	}

	filesByVersion := make(map[string]*MigrationFile)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFileRegex.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}

		version := match[1]
		name := match[2]
		direction := match[3]
		fullPath := filepath.Join(cleanDir, entry.Name())

		mf, exists := filesByVersion[version]
		if !exists {
			mf = &MigrationFile{
				Version: version,
				Name:    name,
			}
			filesByVersion[version] = mf
		}

		if direction == "up" {
			mf.UpPath = fullPath
		} else if direction == "down" {
			mf.DownPath = fullPath
		}
	}

	res := make([]*MigrationFile, 0, len(filesByVersion))
	for _, mf := range filesByVersion {
		res = append(res, mf)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Version < res[j].Version
	})

	return res, nil
}

// EnsureMigrationsTable creates the _migrations history table if it does not already exist.
func EnsureMigrationsTable(ctx context.Context, coord engine.SQLCoordinator) error {
	ddl := `CREATE TABLE IF NOT EXISTS _migrations (
		version VARCHAR(64) PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		applied_at VARCHAR(64) NOT NULL,
		duration_ms INT NOT NULL
	);`

	_, err := coord.ExecuteSessionSQLContext(ctx, "migration", ddl)
	return err
}

// GetAppliedMigrations returns a map of all previously applied migrations keyed by version.
func GetAppliedMigrations(ctx context.Context, coord engine.SQLCoordinator) (map[string]*MigrationRecord, error) {
	if err := EnsureMigrationsTable(ctx, coord); err != nil {
		return nil, err
	}

	query := "SELECT version, name, applied_at, duration_ms FROM _migrations ORDER BY version ASC;"
	res, err := coord.ExecuteSessionSQLContext(ctx, "migration", query)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch applied migrations: %w", err)
	}

	applied := make(map[string]*MigrationRecord)
	for _, row := range res.Rows {
		v, _ := row["version"].(string)
		n, _ := row["name"].(string)
		a, _ := row["applied_at"].(string)
		var dur int64
		switch d := row["duration_ms"].(type) {
		case int:
			dur = int64(d)
		case int64:
			dur = d
		case float64:
			dur = int64(d)
		}

		if v != "" {
			applied[v] = &MigrationRecord{
				Version:    v,
				Name:       n,
				AppliedAt:  a,
				DurationMS: dur,
			}
		}
	}

	return applied, nil
}

// Up applies pending migrations from dir up to the specified number of steps (or all if steps <= 0).
func Up(ctx context.Context, coord engine.SQLCoordinator, dir string, steps int) ([]*MigrationRecord, error) {
	if err := EnsureMigrationsTable(ctx, coord); err != nil {
		return nil, err
	}

	allFiles, err := Load(dir)
	if err != nil {
		return nil, err
	}

	applied, err := GetAppliedMigrations(ctx, coord)
	if err != nil {
		return nil, err
	}

	var pending []*MigrationFile
	for _, mf := range allFiles {
		if _, ok := applied[mf.Version]; !ok {
			if mf.UpPath == "" {
				return nil, fmt.Errorf("migration %s_%s missing .up.sql file", mf.Version, mf.Name)
			}
			pending = append(pending, mf)
		}
	}

	if len(pending) == 0 {
		return nil, nil // Nothing to apply
	}

	limit := len(pending)
	if steps > 0 && steps < limit {
		limit = steps
	}

	var appliedRecords []*MigrationRecord

	for i := 0; i < limit; i++ {
		mf := pending[i]
		content, err := os.ReadFile(mf.UpPath)
		if err != nil {
			return appliedRecords, fmt.Errorf("failed to read %s: %w", mf.UpPath, err)
		}

		stmts := cleanAndSplitSQL(string(content))
		start := time.Now()

		for stmtIdx, stmt := range stmts {
			res, err := coord.ExecuteSessionSQLContext(ctx, "migration", stmt)
			if err != nil {
				return appliedRecords, fmt.Errorf("migration %s statement %d failed: %w", mf.Version, stmtIdx+1, err)
			}
			if res != nil && res.Error != "" {
				return appliedRecords, fmt.Errorf("migration %s statement %d error: %s", mf.Version, stmtIdx+1, res.Error)
			}
		}

		durationMS := time.Since(start).Milliseconds()
		appliedAt := time.Now().UTC().Format(time.RFC3339)

		// Record in _migrations table
		recordSQL := fmt.Sprintf(
			"INSERT INTO _migrations (version, name, applied_at, duration_ms) VALUES ('%s', '%s', '%s', %d);",
			escapeSQL(mf.Version), escapeSQL(mf.Name), escapeSQL(appliedAt), durationMS,
		)

		if _, err := coord.ExecuteSessionSQLContext(ctx, "migration", recordSQL); err != nil {
			return appliedRecords, fmt.Errorf("failed to record migration %s in _migrations table: %w", mf.Version, err)
		}

		appliedRecords = append(appliedRecords, &MigrationRecord{
			Version:    mf.Version,
			Name:       mf.Name,
			AppliedAt:  appliedAt,
			DurationMS: durationMS,
		})
	}

	return appliedRecords, nil
}

// Down rolls back applied migrations using .down.sql files up to steps (default 1 if steps <= 0).
func Down(ctx context.Context, coord engine.SQLCoordinator, dir string, steps int) ([]*MigrationRecord, error) {
	if err := EnsureMigrationsTable(ctx, coord); err != nil {
		return nil, err
	}

	allFiles, err := Load(dir)
	if err != nil {
		return nil, err
	}

	applied, err := GetAppliedMigrations(ctx, coord)
	if err != nil {
		return nil, err
	}

	fileMap := make(map[string]*MigrationFile)
	for _, mf := range allFiles {
		fileMap[mf.Version] = mf
	}

	// Collect applied migrations in reverse order
	var toRevert []*MigrationFile
	for i := len(allFiles) - 1; i >= 0; i-- {
		mf := allFiles[i]
		if _, ok := applied[mf.Version]; ok {
			if mf.DownPath == "" {
				return nil, fmt.Errorf("migration %s_%s missing .down.sql file", mf.Version, mf.Name)
			}
			toRevert = append(toRevert, mf)
		}
	}

	if len(toRevert) == 0 {
		return nil, nil // Nothing to revert
	}

	limit := 1
	if steps > 0 {
		limit = steps
	}
	if limit > len(toRevert) {
		limit = len(toRevert)
	}

	var revertedRecords []*MigrationRecord

	for i := 0; i < limit; i++ {
		mf := toRevert[i]
		content, err := os.ReadFile(mf.DownPath)
		if err != nil {
			return revertedRecords, fmt.Errorf("failed to read %s: %w", mf.DownPath, err)
		}

		stmts := cleanAndSplitSQL(string(content))
		start := time.Now()

		for stmtIdx, stmt := range stmts {
			res, err := coord.ExecuteSessionSQLContext(ctx, "migration", stmt)
			if err != nil {
				return revertedRecords, fmt.Errorf("rollback %s statement %d failed: %w", mf.Version, stmtIdx+1, err)
			}
			if res != nil && res.Error != "" {
				return revertedRecords, fmt.Errorf("rollback %s statement %d error: %s", mf.Version, stmtIdx+1, res.Error)
			}
		}

		durationMS := time.Since(start).Milliseconds()

		// Remove from _migrations table
		delSQL := fmt.Sprintf("DELETE FROM _migrations WHERE version = '%s';", escapeSQL(mf.Version))
		if _, err := coord.ExecuteSessionSQLContext(ctx, "migration", delSQL); err != nil {
			return revertedRecords, fmt.Errorf("failed to remove migration %s from _migrations table: %w", mf.Version, err)
		}

		rec := applied[mf.Version]
		revertedRecords = append(revertedRecords, &MigrationRecord{
			Version:    mf.Version,
			Name:       mf.Name,
			AppliedAt:  rec.AppliedAt,
			DurationMS: durationMS,
		})
	}

	return revertedRecords, nil
}

// Status returns the deployment status of all discovered migrations in dir.
func Status(ctx context.Context, coord engine.SQLCoordinator, dir string) ([]MigrationStatus, error) {
	if err := EnsureMigrationsTable(ctx, coord); err != nil {
		return nil, err
	}

	allFiles, err := Load(dir)
	if err != nil {
		return nil, err
	}

	applied, err := GetAppliedMigrations(ctx, coord)
	if err != nil {
		return nil, err
	}

	statuses := make([]MigrationStatus, 0, len(allFiles))
	for _, mf := range allFiles {
		rec, isApplied := applied[mf.Version]
		st := MigrationStatus{
			Version: mf.Version,
			Name:    mf.Name,
			Applied: isApplied,
		}
		if isApplied && rec != nil {
			st.AppliedAt = rec.AppliedAt
			st.DurationMS = rec.DurationMS
		}
		statuses = append(statuses, st)
	}

	return statuses, nil
}

func cleanAndSplitSQL(content string) []string {
	lines := strings.Split(content, "\n")
	var cleaned []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleaned = append(cleaned, l)
	}
	rawStmts := engine.SplitStatements(strings.Join(cleaned, "\n"))
	var stmts []string
	for _, s := range rawStmts {
		t := strings.TrimSpace(s)
		if t != "" {
			stmts = append(stmts, t)
		}
	}
	return stmts
}

func escapeSQL(val string) string {
	return strings.ReplaceAll(val, "'", "''")
}
