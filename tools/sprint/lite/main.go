package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lionsql/tools/sprint/core"
)

type SQLiteBackend struct {
	dbPath string
}

func NewSQLiteBackend(dbPath string) *SQLiteBackend {
	if dbPath == "" {
		dbPath = ".sprint.db"
	}
	return &SQLiteBackend{dbPath: dbPath}
}

func (b *SQLiteBackend) Name() string {
	return "SQLite"
}

func (b *SQLiteBackend) Init() error {
	if dir := filepath.Dir(b.dbPath); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	_, err := b.Exec(core.SchemaDDL)
	return err
}

func (b *SQLiteBackend) Exec(sql string) (*core.QueryResp, error) {
	trimmed := strings.TrimSpace(sql)
	isSelect := strings.HasPrefix(strings.ToUpper(trimmed), "SELECT")

	var cmd *exec.Cmd
	if isSelect {
		cmd = exec.Command("sqlite3", b.dbPath, "-json", sql)
	} else {
		cmd = exec.Command("sqlite3", b.dbPath, sql)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite3 error: %v, output: %s", err, string(out))
	}

	res := &core.QueryResp{Success: true}
	if isSelect {
		trimmedOut := strings.TrimSpace(string(out))
		if trimmedOut == "" || trimmedOut == "[]" {
			res.Rows = []map[string]any{}
		} else {
			var rows []map[string]any
			if err := json.Unmarshal([]byte(trimmedOut), &rows); err != nil {
				return nil, fmt.Errorf("failed to parse sqlite3 json output: %w", err)
			}
			res.Rows = rows
		}
	}
	return res, nil
}

func (b *SQLiteBackend) Close() error {
	return nil
}

func main() {
	defaultDB := ".sprint.db"
	if envDB := os.Getenv("SPRINT_SQLITE_PATH"); envDB != "" {
		defaultDB = envDB
	}
	dbPath := flag.String("db", defaultDB, "SQLite database file path")
	flag.Parse()

	backend := NewSQLiteBackend(*dbPath)
	defer backend.Close()

	core.RunCLI(backend, "Lite Sprint Task Tracker (SQLite) (tools/sprint/lite)", flag.Args())
}
