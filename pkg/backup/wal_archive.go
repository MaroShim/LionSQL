package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/storage/memengine"
)

// ArchivedSegment records metadata for an archived WAL log segment.
type ArchivedSegment struct {
	Path         string    `json:"path"`
	Filename     string    `json:"filename"`
	MinLSN       int64     `json:"min_lsn"`
	MaxLSN       int64     `json:"max_lsn"`
	MinTimestamp time.Time `json:"min_timestamp"`
	MaxTimestamp time.Time `json:"max_timestamp"`
	RecordCount  int64     `json:"record_count"`
	SizeBytes    int64     `json:"size_bytes"`
}

// ArchiveManifest tracks all archived WAL segments.
type ArchiveManifest struct {
	Version      string            `json:"version"`
	LastArchived time.Time         `json:"last_archived"`
	Segments     []ArchivedSegment `json:"segments"`
}

// WALArchiver manages continuous WAL segment archiving and discovery.
type WALArchiver struct {
	mu         sync.Mutex
	archiveDir string
}

// NewWALArchiver initializes or opens a WAL archiver repository in archiveDir.
func NewWALArchiver(archiveDir string) (*WALArchiver, error) {
	cleanDir := filepath.Clean(archiveDir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL archive directory %s: %w", cleanDir, err)
	}
	return &WALArchiver{
		archiveDir: cleanDir,
	}, nil
}

// ArchiveSegment parses, verifies, and atomically stores a WAL segment in the archive directory.
func (a *WALArchiver) ArchiveSegment(walPath string) (*ArchivedSegment, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cleanSrc := filepath.Clean(walPath)
	srcFile, err := os.Open(cleanSrc)
	if err != nil {
		return nil, fmt.Errorf("failed to open source WAL file %s: %w", cleanSrc, err)
	}
	stat, err := srcFile.Stat()
	_ = srcFile.Close()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		return nil, fmt.Errorf("cannot archive empty WAL file")
	}

	// 1. Scan WAL file to extract LSN and Timestamp boundaries
	var minLSN, maxLSN int64
	var minTS, maxTS int64
	var recCount int64

	_, err = memengine.ReplayWAL(cleanSrc, func(rec memengine.WALRecord) error {
		if recCount == 0 {
			minLSN = rec.LSN
			minTS = rec.Timestamp
		}
		if rec.LSN < minLSN {
			minLSN = rec.LSN
		}
		if rec.LSN > maxLSN {
			maxLSN = rec.LSN
		}
		if rec.Timestamp < minTS {
			minTS = rec.Timestamp
		}
		if rec.Timestamp > maxTS {
			maxTS = rec.Timestamp
		}
		recCount++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan WAL file %s: %w", cleanSrc, err)
	}

	if recCount == 0 {
		return nil, fmt.Errorf("source WAL file contains no valid committed transactions")
	}

	// 2. Format deterministic segment filename: wal_<minLSN>_<maxLSN>_<maxTS>.wal
	segmentName := fmt.Sprintf("wal_%016d_%016d_%d.wal", minLSN, maxLSN, maxTS)
	targetPath := filepath.Join(a.archiveDir, segmentName)
	tmpPath := filepath.Join(a.archiveDir, fmt.Sprintf(".tmp_%s_%d", segmentName, time.Now().UnixNano()))

	// 3. Atomically copy to archiveDir
	src, err := os.Open(cleanSrc)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary archive file: %w", err)
	}

	written, err := io.Copy(dst, src)
	_ = dst.Sync()
	_ = dst.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to copy WAL segment data: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to commit archive segment: %w", err)
	}

	seg := ArchivedSegment{
		Path:         targetPath,
		Filename:     segmentName,
		MinLSN:       minLSN,
		MaxLSN:       maxLSN,
		MinTimestamp: time.Unix(0, minTS).UTC(),
		MaxTimestamp: time.Unix(0, maxTS).UTC(),
		RecordCount:  recCount,
		SizeBytes:    written,
	}

	// 4. Update archive manifest atomically
	_ = a.updateManifestLocked(seg)

	return &seg, nil
}

// RotateAndArchive forces sync on the active WAL writer, archives the current file, and resets it.
func (a *WALArchiver) RotateAndArchive(writer *memengine.WALWriter, walPath string) (*ArchivedSegment, error) {
	if writer != nil {
		if err := writer.Sync(); err != nil {
			return nil, fmt.Errorf("failed to sync WAL writer before rotation: %w", err)
		}
	}

	seg, err := a.ArchiveSegment(walPath)
	if err != nil {
		return nil, err
	}

	if writer != nil {
		if err := writer.Truncate(); err != nil {
			return nil, fmt.Errorf("failed to reset active WAL file after archive: %w", err)
		}
	}

	return seg, nil
}

// ListSegments returns all archived WAL segments ordered by MinLSN ascending.
func (a *WALArchiver) ListSegments() ([]ArchivedSegment, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	entries, err := os.ReadDir(a.archiveDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read archive directory: %w", err)
	}

	var segments []ArchivedSegment
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wal") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		filePath := filepath.Join(a.archiveDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Inspect segment
		var minLSN, maxLSN int64
		var minTS, maxTS int64
		var recCount int64

		_, err = memengine.ReplayWAL(filePath, func(rec memengine.WALRecord) error {
			if recCount == 0 {
				minLSN = rec.LSN
				minTS = rec.Timestamp
			}
			if rec.LSN < minLSN {
				minLSN = rec.LSN
			}
			if rec.LSN > maxLSN {
				maxLSN = rec.LSN
			}
			if rec.Timestamp < minTS {
				minTS = rec.Timestamp
			}
			if rec.Timestamp > maxTS {
				maxTS = rec.Timestamp
			}
			recCount++
			return nil
		})
		if err != nil || recCount == 0 {
			continue
		}

		segments = append(segments, ArchivedSegment{
			Path:         filePath,
			Filename:     entry.Name(),
			MinLSN:       minLSN,
			MaxLSN:       maxLSN,
			MinTimestamp: time.Unix(0, minTS).UTC(),
			MaxTimestamp: time.Unix(0, maxTS).UTC(),
			RecordCount:  recCount,
			SizeBytes:    info.Size(),
		})
	}

	sort.Slice(segments, func(i, j int) bool {
		if segments[i].MinLSN != segments[j].MinLSN {
			return segments[i].MinLSN < segments[j].MinLSN
		}
		return segments[i].MinTimestamp.Before(segments[j].MinTimestamp)
	})

	return segments, nil
}

func (a *WALArchiver) updateManifestLocked(newSeg ArchivedSegment) error {
	manifestPath := filepath.Join(a.archiveDir, "archive_manifest.json")
	var manifest ArchiveManifest

	data, err := os.ReadFile(manifestPath)
	if err == nil {
		_ = json.Unmarshal(data, &manifest)
	}

	manifest.Version = "0.7.0"
	manifest.LastArchived = time.Now().UTC()

	// Append or update segment
	found := false
	for i, s := range manifest.Segments {
		if s.Filename == newSeg.Filename {
			manifest.Segments[i] = newSeg
			found = true
			break
		}
	}
	if !found {
		manifest.Segments = append(manifest.Segments, newSeg)
	}

	sort.Slice(manifest.Segments, func(i, j int) bool {
		return manifest.Segments[i].MinLSN < manifest.Segments[j].MinLSN
	})

	outBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	tmpManifest := filepath.Join(a.archiveDir, fmt.Sprintf(".manifest_%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmpManifest, outBytes, 0644); err != nil {
		return err
	}
	return os.Rename(tmpManifest, manifestPath)
}
