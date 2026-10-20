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
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/config"
	"lionsql/pkg/storage"
)

// Manifest contains metadata about a backup archive.
type Manifest struct {
	Version         string         `json:"version"`
	CreatedAt       time.Time      `json:"created_at"`
	TablesCount     int            `json:"tables_count"`
	TotalRecords    int64          `json:"total_records"`
	ProceduresCount int            `json:"procedures_count"`
	FunctionsCount  int            `json:"functions_count"`
	TriggersCount   int            `json:"triggers_count"`
	ViewsCount      int            `json:"views_count"`
	Tables          []TableSummary `json:"tables"`
}

// TableSummary records the record count of an individual table.
type TableSummary struct {
	Name    string `json:"name"`
	Records int64  `json:"records"`
}

// CreateBackup writes a point-in-time snapshot archive of the database into w (.tar.gz format).
// It runs non-blockingly concurrently with active queries by opening a read-only transaction (MVCC/OCC snapshot).
func CreateBackup(w io.Writer, eng storage.Engine, cat *catalog.Catalog) (*Manifest, error) {
	startTime := time.Now().UTC()

	// 1. Establish read-only snapshot session
	sess, err := eng.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to open backup session: %w", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("failed to begin snapshot transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 2. Fetch schema definitions
	tables, err := eng.ListTables()
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}

	// 3. Prepare temp directory for table data streaming
	tmpDir, err := os.MkdirTemp("", "lionsql-backup-data-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create backup temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	manifest := &Manifest{
		Version:      config.Version,
		CreatedAt:    startTime,
		TablesCount:  len(tables),
		Tables:       make([]TableSummary, 0, len(tables)),
	}

	type tempTableFile struct {
		name    string
		path    string
		records int64
	}
	tempFiles := make([]tempTableFile, 0, len(tables))

	// 4. Dump records for each table into temporary jsonl files
	for _, tbl := range tables {
		cur, err := tx.Scan(tbl.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to scan table %s: %w", tbl.Name, err)
		}

		tblPath := filepath.Join(tmpDir, fmt.Sprintf("%s.jsonl", tbl.Name))
		f, err := os.OpenFile(tblPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			_ = cur.Close()
			return nil, fmt.Errorf("failed to create temp table dump for %s: %w", tbl.Name, err)
		}

		writer := bufio.NewWriterSize(f, 64*1024)
		var recCount int64

		for cur.Next() {
			rec, err := cur.Record()
			if err != nil {
				_ = f.Close()
				_ = cur.Close()
				return nil, fmt.Errorf("error reading record from %s: %w", tbl.Name, err)
			}
			data, err := json.Marshal(rec)
			if err != nil {
				_ = f.Close()
				_ = cur.Close()
				return nil, fmt.Errorf("failed to marshal record in %s: %w", tbl.Name, err)
			}
			if _, err := writer.Write(data); err != nil {
				_ = f.Close()
				_ = cur.Close()
				return nil, fmt.Errorf("failed to write record in %s: %w", tbl.Name, err)
			}
			if err := writer.WriteByte('\n'); err != nil {
				_ = f.Close()
				_ = cur.Close()
				return nil, fmt.Errorf("failed to write newline in %s: %w", tbl.Name, err)
			}
			recCount++
		}
		_ = cur.Close()

		if err := writer.Flush(); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("failed to flush table %s: %w", tbl.Name, err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("failed to close temp file for %s: %w", tbl.Name, err)
		}

		manifest.TotalRecords += recCount
		manifest.Tables = append(manifest.Tables, TableSummary{
			Name:    tbl.Name,
			Records: recCount,
		})
		tempFiles = append(tempFiles, tempTableFile{
			name:    tbl.Name,
			path:    tblPath,
			records: recCount,
		})
	}

	// 5. Catalog objects
	var procs []*catalog.Procedure
	var funcs []*catalog.Function
	var trigs []*catalog.Trigger
	var views []*catalog.View
	if cat != nil {
		procs = cat.ListProcedures()
		funcs = cat.ListFunctions()
		trigs = cat.ListTriggers()
		views = cat.ListViews()
	}
	manifest.ProceduresCount = len(procs)
	manifest.FunctionsCount = len(funcs)
	manifest.TriggersCount = len(trigs)
	manifest.ViewsCount = len(views)

	// 6. Build tar.gz archive
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	// Helper to write a byte slice as a tar entry
	writeTarBytes := func(name string, content []byte) error {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(content)),
			ModTime:  startTime,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(content)
		return err
	}

	// Write manifest.json
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	if err := writeTarBytes("manifest.json", manifestBytes); err != nil {
		return nil, fmt.Errorf("failed to write manifest to archive: %w", err)
	}

	// Write schemas.json
	schemasBytes, err := json.MarshalIndent(tables, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schemas: %w", err)
	}
	if err := writeTarBytes("schemas.json", schemasBytes); err != nil {
		return nil, fmt.Errorf("failed to write schemas to archive: %w", err)
	}

	// Write catalog objects
	if procsBytes, err := json.MarshalIndent(procs, "", "  "); err == nil {
		if err := writeTarBytes("catalog/procedures.json", procsBytes); err != nil {
			return nil, fmt.Errorf("failed to write procedures to archive: %w", err)
		}
	}
	if funcsBytes, err := json.MarshalIndent(funcs, "", "  "); err == nil {
		if err := writeTarBytes("catalog/functions.json", funcsBytes); err != nil {
			return nil, fmt.Errorf("failed to write functions to archive: %w", err)
		}
	}
	if trigsBytes, err := json.MarshalIndent(trigs, "", "  "); err == nil {
		if err := writeTarBytes("catalog/triggers.json", trigsBytes); err != nil {
			return nil, fmt.Errorf("failed to write triggers to archive: %w", err)
		}
	}
	if viewsBytes, err := json.MarshalIndent(views, "", "  "); err == nil {
		if err := writeTarBytes("catalog/views.json", viewsBytes); err != nil {
			return nil, fmt.Errorf("failed to write views to archive: %w", err)
		}
	}

	// Write table data files
	for _, tf := range tempFiles {
		fi, err := os.Stat(tf.path)
		if err != nil {
			return nil, fmt.Errorf("failed to stat temp file %s: %w", tf.path, err)
		}

		hdr := &tar.Header{
			Name:     fmt.Sprintf("data/%s.jsonl", tf.name),
			Mode:     0644,
			Size:     fi.Size(),
			ModTime:  startTime,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("failed to write tar header for data/%s.jsonl: %w", tf.name, err)
		}

		f, err := os.Open(tf.path)
		if err != nil {
			return nil, fmt.Errorf("failed to open temp table dump for %s: %w", tf.name, err)
		}
		_, copyErr := io.Copy(tw, f)
		_ = f.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("failed to copy data for %s: %w", tf.name, copyErr)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize tar archive: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize gzip stream: %w", err)
	}

	return manifest, nil
}

// CreateBackupFile creates a snapshot backup and writes it atomically to the target file.
func CreateBackupFile(targetPath string, eng storage.Engine, cat *catalog.Catalog) (*Manifest, error) {
	cleanPath := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "lionsql-backup-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary backup file: %w", err)
	}
	tmpName := tmpFile.Name()

	cleanup := true
	defer func() {
		if cleanup {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	manifest, err := CreateBackup(tmpFile, eng, cat)
	if err != nil {
		return nil, err
	}

	if err := tmpFile.Sync(); err != nil {
		return nil, fmt.Errorf("failed to sync backup file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close backup file: %w", err)
	}

	if err := os.Rename(tmpName, cleanPath); err != nil {
		return nil, fmt.Errorf("failed to commit backup file: %w", err)
	}

	cleanup = false
	return manifest, nil
}
