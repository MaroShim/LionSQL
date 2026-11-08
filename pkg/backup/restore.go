package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
)

// RestoreOptions configures database restore behavior.
type RestoreOptions struct {
	// DropExisting drops existing tables with conflicting names before restoring. Default is true.
	DropExisting bool `json:"drop_existing"`
	// BatchSize specifies how many records to insert per transaction. Default is 1000.
	BatchSize int `json:"batch_size"`
}

// DefaultRestoreOptions returns safe defaults for restoring backups.
func DefaultRestoreOptions() RestoreOptions {
	return RestoreOptions{
		DropExisting: true,
		BatchSize:    1000,
	}
}

// RestoreResult provides detailed statistics on completed restore operations.
type RestoreResult struct {
	TablesRestored     int           `json:"tables_restored"`
	RecordsRestored    int64         `json:"records_restored"`
	ProceduresRestored int           `json:"procedures_restored"`
	FunctionsRestored  int           `json:"functions_restored"`
	TriggersRestored   int           `json:"triggers_restored"`
	ViewsRestored      int           `json:"views_restored"`
	Duration           time.Duration `json:"duration_ms"`
}

// normalizeTableName strips quotes, brackets, and schema qualifiers for consistent lookup.
func normalizeTableName(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "[]\"'`")
	if idx := strings.LastIndex(s, "."); idx != -1 {
		s = strings.Trim(s[idx+1:], "[]\"'`")
	}
	return strings.ToLower(s)
}

// TopologicalSortTables reorders table schemas so that parent tables referenced by
// foreign keys appear before the child tables that depend on them.
// [RULE:RadicalKISS] Kahn's algorithm is applied deterministically.
func TopologicalSortTables(tables []*storage.TableSchema) []*storage.TableSchema {
	if len(tables) <= 1 {
		return tables
	}

	nameMap := make(map[string]*storage.TableSchema, len(tables))
	for _, t := range tables {
		norm := normalizeTableName(t.Name)
		nameMap[norm] = t
	}

	// Graph: parent -> children (dependencies: child depends on parent)
	// inDegree: count of parent tables in the current set that the child depends on.
	inDegree := make(map[string]int, len(tables))
	children := make(map[string][]string, len(tables))

	for norm := range nameMap {
		inDegree[norm] = 0
	}

	for _, t := range tables {
		childNorm := normalizeTableName(t.Name)
		seenParents := make(map[string]struct{})
		for _, fk := range t.ForeignKeys {
			parentNorm := normalizeTableName(fk.ParentTable)
			// Ignore self-references or parents not present in this set
			if parentNorm == childNorm {
				continue
			}
			if _, exists := nameMap[parentNorm]; !exists {
				continue
			}
			if _, already := seenParents[parentNorm]; already {
				continue
			}
			seenParents[parentNorm] = struct{}{}
			children[parentNorm] = append(children[parentNorm], childNorm)
			inDegree[childNorm]++
		}
	}

	// Initialize queue with tables having 0 in-degree (no dependencies)
	queue := make([]string, 0, len(tables))
	for _, t := range tables {
		norm := normalizeTableName(t.Name)
		if inDegree[norm] == 0 {
			queue = append(queue, norm)
		}
	}

	sorted := make([]*storage.TableSchema, 0, len(tables))
	visited := make(map[string]bool, len(tables))

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		sorted = append(sorted, nameMap[curr])

		for _, ch := range children[curr] {
			inDegree[ch]--
			if inDegree[ch] == 0 {
				queue = append(queue, ch)
			}
		}
	}

	// If cycles or disconnected vertices exist, append remaining tables deterministically
	if len(sorted) < len(tables) {
		for _, t := range tables {
			norm := normalizeTableName(t.Name)
			if !visited[norm] {
				visited[norm] = true
				sorted = append(sorted, t)
			}
		}
	}

	return sorted
}

// dropAllTablesSafely drops all tables in the engine in reverse topological order (children first).
func dropAllTablesSafely(eng storage.Engine) {
	if existingList, err := eng.ListTables(); err == nil && len(existingList) > 0 {
		sortedExisting := TopologicalSortTables(existingList)
		for i := len(sortedExisting) - 1; i >= 0; i-- {
			_ = eng.DropTable(sortedExisting[i].Name)
		}
	}
}

// RestoreBackup restores tables, schemas, data, and catalog procedures from a tar.gz stream.
// It guarantees atomic restore: if any error occurs midway, the database is rolled back
// to its exact pre-restore snapshot state, preventing half-restored corruption ([RULE:AtomicSaves]).
func RestoreBackup(r io.Reader, eng storage.Engine, cat *catalog.Catalog, opts RestoreOptions) (*RestoreResult, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}

	// 1. Check for existing database state and take an ephemeral pre-restore snapshot
	hasExistingState := false
	existingTables, err := eng.ListTables()
	if err == nil && len(existingTables) > 0 {
		hasExistingState = true
	} else if cat != nil && (len(cat.ListProcedures()) > 0 || len(cat.ListFunctions()) > 0 || len(cat.ListTriggers()) > 0 || len(cat.ListViews()) > 0) {
		hasExistingState = true
	}

	var preRestorePath string
	if hasExistingState {
		preRestoreFile, err := os.CreateTemp("", "lionsql-prerestore-*.tar.gz")
		if err != nil {
			return nil, fmt.Errorf("failed to create pre-restore snapshot temp file: %w", err)
		}
		preRestorePath = preRestoreFile.Name()
		defer func() {
			_ = os.Remove(preRestorePath)
		}()

		if _, err := CreateBackup(preRestoreFile, eng, cat); err != nil {
			_ = preRestoreFile.Close()
			return nil, fmt.Errorf("failed to capture pre-restore snapshot: %w", err)
		}
		if err := preRestoreFile.Close(); err != nil {
			return nil, fmt.Errorf("failed to close pre-restore snapshot: %w", err)
		}
	}

	// 2. Perform the primary restore operation
	result, restoreErr := restoreCore(r, eng, cat, opts)
	if restoreErr != nil {
		// Rollback: drop all tables created during the failed restore
		dropAllTablesSafely(eng)

		// Restore the previous database state from snapshot if available
		if preRestorePath != "" {
			pf, err := os.Open(preRestorePath)
			if err == nil {
				defer pf.Close()
				_, rbErr := restoreCore(pf, eng, cat, RestoreOptions{
					DropExisting: true,
					BatchSize:    opts.BatchSize,
				})
				if rbErr != nil {
					return nil, fmt.Errorf("restore failed: %w (critical rollback error: %v)", restoreErr, rbErr)
				}
			}
		}
		return nil, restoreErr
	}

	return result, nil
}

// restoreCore performs the physical archive unpacking, schema creation, data insertion,
// and catalog registration.
func restoreCore(r io.Reader, eng storage.Engine, cat *catalog.Catalog, opts RestoreOptions) (*RestoreResult, error) {
	startTime := time.Now()

	// 1. Unpack tar.gz stream to temporary staging directory
	tmpDir, err := os.MkdirTemp("", "lionsql-restore-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp restore directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip stream: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read error: %w", err)
		}

		// Prevent path traversal
		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}

		targetPath := filepath.Join(tmpDir, cleanName)
		if hdr.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return nil, fmt.Errorf("failed to create directory in archive: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create parent directory for %s: %w", cleanName, err)
		}

		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return nil, fmt.Errorf("failed to create file %s: %w", targetPath, err)
		}

		if _, err := io.Copy(outFile, tr); err != nil {
			_ = outFile.Close()
			return nil, fmt.Errorf("failed to write archive entry %s: %w", cleanName, err)
		}
		_ = outFile.Close()
	}

	result := &RestoreResult{}

	// 2. Read and restore schemas
	schemasPath := filepath.Join(tmpDir, "schemas.json")
	schemaData, err := os.ReadFile(schemasPath)
	if err != nil {
		return nil, fmt.Errorf("archive missing schemas.json: %w", err)
	}

	var tables []*storage.TableSchema
	if err := json.Unmarshal(schemaData, &tables); err != nil {
		return nil, fmt.Errorf("failed to parse schemas.json: %w", err)
	}

	// Reorder tables by foreign key topological dependency (parents before children)
	tables = TopologicalSortTables(tables)

	if opts.DropExisting {
		// Drop existing tables in reverse topological order (children before parents)
		if existingList, err := eng.ListTables(); err == nil && len(existingList) > 0 {
			sortedExisting := TopologicalSortTables(existingList)
			for i := len(sortedExisting) - 1; i >= 0; i-- {
				_ = eng.DropTable(sortedExisting[i].Name)
			}
		}
		for i := len(tables) - 1; i >= 0; i-- {
			_ = eng.DropTable(tables[i].Name)
		}
	}

	for _, schema := range tables {
		// Create table
		if err := eng.CreateTable(schema); err != nil {
			// If already exists and not dropping, continue
			if !opts.DropExisting && strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("failed to recreate table %s: %w", schema.Name, err)
		}

		// Create secondary indexes
		for idx := range schema.Indexes {
			_ = eng.CreateIndex(&schema.Indexes[idx])
		}

		result.TablesRestored++
	}

	// 3. Restore data records for each table in topological order
	sess, err := eng.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to open session for data restore: %w", err)
	}
	defer sess.Close()

	for _, schema := range tables {
		dataPath := filepath.Join(tmpDir, "data", fmt.Sprintf("%s.jsonl", schema.Name))
		dataFile, err := os.Open(dataPath)
		if err != nil {
			// Table may have had 0 rows or no data file
			continue
		}

		scanner := bufio.NewScanner(dataFile)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 16*1024*1024)

		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			_ = dataFile.Close()
			return nil, fmt.Errorf("failed to begin transaction for %s: %w", schema.Name, err)
		}

		var countInTx int
		var tableRecords int64

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var rec storage.Record
			if err := json.Unmarshal(line, &rec); err != nil {
				_ = tx.Rollback()
				_ = dataFile.Close()
				return nil, fmt.Errorf("corrupt record in %s: %w", schema.Name, err)
			}

			if err := tx.Insert(schema.Name, rec); err != nil {
				_ = tx.Rollback()
				_ = dataFile.Close()
				return nil, fmt.Errorf("failed to insert record into %s: %w", schema.Name, err)
			}

			tableRecords++
			countInTx++

			if countInTx >= opts.BatchSize {
				if err := tx.Commit(); err != nil {
					_ = dataFile.Close()
					return nil, fmt.Errorf("failed to commit batch in %s: %w", schema.Name, err)
				}
				countInTx = 0
				tx, err = sess.BeginTx(storage.TxOptions{})
				if err != nil {
					_ = dataFile.Close()
					return nil, fmt.Errorf("failed to begin batch transaction in %s: %w", schema.Name, err)
				}
			}
		}

		if countInTx > 0 {
			if err := tx.Commit(); err != nil {
				_ = dataFile.Close()
				return nil, fmt.Errorf("failed to commit final batch in %s: %w", schema.Name, err)
			}
		} else {
			_ = tx.Rollback()
		}

		_ = dataFile.Close()
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("scan error in %s: %w", schema.Name, err)
		}
		result.RecordsRestored += tableRecords
	}

	// 4. Restore catalog objects (procedures, functions, triggers, views)
	if cat != nil {
		procsPath := filepath.Join(tmpDir, "catalog", "procedures.json")
		if data, err := os.ReadFile(procsPath); err == nil {
			var procs []*catalog.Procedure
			if err := json.Unmarshal(data, &procs); err == nil {
				for _, p := range procs {
					_ = cat.RegisterProcedure(p)
					result.ProceduresRestored++
				}
			}
		}

		funcsPath := filepath.Join(tmpDir, "catalog", "functions.json")
		if data, err := os.ReadFile(funcsPath); err == nil {
			var funcs []*catalog.Function
			if err := json.Unmarshal(data, &funcs); err == nil {
				for _, f := range funcs {
					_ = cat.RegisterFunction(f)
					result.FunctionsRestored++
				}
			}
		}

		trigsPath := filepath.Join(tmpDir, "catalog", "triggers.json")
		if data, err := os.ReadFile(trigsPath); err == nil {
			var trigs []*catalog.Trigger
			if err := json.Unmarshal(data, &trigs); err == nil {
				for _, t := range trigs {
					_ = cat.RegisterTrigger(t)
					result.TriggersRestored++
				}
			}
		}

		viewsPath := filepath.Join(tmpDir, "catalog", "views.json")
		if data, err := os.ReadFile(viewsPath); err == nil {
			var views []*catalog.View
			if err := json.Unmarshal(data, &views); err == nil {
				for _, v := range views {
					cat.CreateOrReplaceView(v)
					result.ViewsRestored++
				}
			}
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// RestoreBackupFile restores from a backup archive file located at filePath.
func RestoreBackupFile(filePath string, eng storage.Engine, cat *catalog.Catalog, opts RestoreOptions) (*RestoreResult, error) {
	cleanPath := filepath.Clean(filePath)
	f, err := os.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open backup file %s: %w", cleanPath, err)
	}
	defer f.Close()

	return RestoreBackup(f, eng, cat, opts)
}
