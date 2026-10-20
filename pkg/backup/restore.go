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

// RestoreBackup restores tables, schemas, data, and catalog procedures from a tar.gz stream.
func RestoreBackup(r io.Reader, eng storage.Engine, cat *catalog.Catalog, opts RestoreOptions) (*RestoreResult, error) {
	startTime := time.Now()
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}

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

	for _, schema := range tables {
		if opts.DropExisting {
			_ = eng.DropTable(schema.Name)
		}

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

	// 3. Restore data records for each table
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

	// 4. Restore catalog objects (procedures, functions, triggers)
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
