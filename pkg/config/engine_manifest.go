package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EngineManifestFileName is the canonical marker file persisted at the root of each data directory.
const EngineManifestFileName = "lionsql.engine.json"

// ErrEngineMismatch is returned when an explicitly requested storage engine conflicts
// with the engine manifest already initialized in the target data directory.
var ErrEngineMismatch = errors.New("data directory storage engine mismatch")

// EngineManifest records the storage backend and codec identity of an initialized data directory.
type EngineManifest struct {
	Engine            string `json:"engine"`              // "wiredtiger" or "memory"
	Disk              string `json:"disk"`                // "wiredtiger" or "compact"
	Memory            string `json:"memory"`              // "compact" or "performance"
	TupleCodecVersion int    `json:"tuple_codec_version"` // 2 (Invariant 13 V2 Tuple Codec)
	CreatedAt         string `json:"created_at"`
	CreatedByVersion  string `json:"created_by_version"`
}

// NormalizeDiskBackend maps aliases like "wt", "mem", "memory", "none", "off" to canonical "wiredtiger" or "compact".
func NormalizeDiskBackend(disk string) string {
	switch strings.ToLower(strings.TrimSpace(disk)) {
	case "wiredtiger", "wt":
		return "wiredtiger"
	case "compact", "memory", "mem", "none", "off":
		return "compact"
	default:
		return strings.ToLower(strings.TrimSpace(disk))
	}
}

func diskToCanonicalEngine(disk string) string {
	if NormalizeDiskBackend(disk) == "wiredtiger" {
		return "wiredtiger"
	}
	return "memory"
}

// ReadEngineManifest reads <dataDir>/lionsql.engine.json if present.
// If the manifest file does not exist, it returns (nil, nil).
func ReadEngineManifest(dataDir string) (*EngineManifest, error) {
	cleanDir := filepath.Clean(strings.TrimSpace(dataDir))
	if cleanDir == "" || cleanDir == "." {
		return nil, nil
	}
	manifestPath := filepath.Join(cleanDir, EngineManifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read engine manifest %q: %w", manifestPath, err)
	}
	var m EngineManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse engine manifest %q: %w", manifestPath, err)
	}
	m.Disk = NormalizeDiskBackend(m.Disk)
	if m.Disk == "" {
		m.Disk = NormalizeDiskBackend(m.Engine)
	}
	if m.Engine == "" {
		m.Engine = diskToCanonicalEngine(m.Disk)
	}
	if m.Memory == "" {
		m.Memory = DefaultMemory()
	}
	if m.TupleCodecVersion == 0 {
		m.TupleCodecVersion = 2
	}
	return &m, nil
}

// detectLegacyEngineSignature inspects an existing data directory that predates lionsql.engine.json
// to infer whether it was initialized with WiredTiger or In-Memory (compact AOF/RDB).
func detectLegacyEngineSignature(dataDir string) *EngineManifest {
	cleanDir := filepath.Clean(strings.TrimSpace(dataDir))
	if cleanDir == "" || cleanDir == "." {
		return nil
	}

	wtMarkers := []string{"WiredTiger", "WiredTiger.wt", "WiredTiger.turtle"}
	for _, marker := range wtMarkers {
		if fi, err := os.Stat(filepath.Join(cleanDir, marker)); err == nil && !fi.IsDir() {
			return &EngineManifest{
				Engine:            "wiredtiger",
				Disk:              "wiredtiger",
				Memory:            DefaultMemory(),
				TupleCodecVersion: 2,
				CreatedAt:         time.Now().UTC().Format(time.RFC3339),
				CreatedByVersion:  Version,
			}
		}
	}

	memMarkers := []string{"wal.aof", "tables.mpk"}
	for _, marker := range memMarkers {
		if fi, err := os.Stat(filepath.Join(cleanDir, marker)); err == nil && !fi.IsDir() {
			return &EngineManifest{
				Engine:            "memory",
				Disk:              "compact",
				Memory:            DefaultMemory(),
				TupleCodecVersion: 2,
				CreatedAt:         time.Now().UTC().Format(time.RFC3339),
				CreatedByVersion:  Version,
			}
		}
	}

	return nil
}

// WriteEngineManifestAtomic writes <dataDir>/lionsql.engine.json atomically via a temporary swap file ([RULE:AtomicSaves]).
func WriteEngineManifestAtomic(dataDir string, m *EngineManifest) error {
	cleanDir := filepath.Clean(strings.TrimSpace(dataDir))
	if cleanDir == "" {
		return fmt.Errorf("data directory path is empty")
	}
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory %q: %w", cleanDir, err)
	}

	manifestPath := filepath.Join(cleanDir, EngineManifestFileName)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal engine manifest: %w", err)
	}
	data = append(data, '\n')

	tmpPath := manifestPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary engine manifest %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, manifestPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically rename engine manifest %q: %w", manifestPath, err)
	}
	return nil
}

// ResolveOrWriteEngineManifest resolves the effective (disk, memory) engine configuration for dataDir:
//  1. If an engine manifest (or legacy engine file signature) exists in dataDir:
//     - When explicitEngineSet is true and the requested disk backend differs from the manifest's disk backend,
//       it returns ErrEngineMismatch (Fail-Closed).
//     - When explicitEngineSet is false, it automatically adopts the manifest's disk and memory settings.
//  2. If dataDir is fresh (no manifest and no legacy files), it writes a new lionsql.engine.json atomically.
func ResolveOrWriteEngineManifest(dataDir string, requestedDisk string, requestedMemory string, explicitEngineSet bool, explicitMemorySet bool) (*EngineManifest, error) {
	normDisk := NormalizeDiskBackend(requestedDisk)
	if normDisk == "" {
		normDisk = DefaultDisk()
	}
	normMem := strings.ToLower(strings.TrimSpace(requestedMemory))
	if normMem == "" {
		normMem = DefaultMemory()
	}

	existing, err := ReadEngineManifest(dataDir)
	if err != nil {
		return nil, err
	}

	isBackfill := false
	if existing == nil {
		if legacy := detectLegacyEngineSignature(dataDir); legacy != nil {
			existing = legacy
			isBackfill = true
		}
	}

	if existing != nil {
		if explicitEngineSet && existing.Disk != normDisk {
			return nil, fmt.Errorf("%w: data directory %q was initialized with engine %q (disk=%s), but %q (disk=%s) was explicitly requested; use a different -dir or migrate via backup/restore",
				ErrEngineMismatch, dataDir, existing.Engine, existing.Disk, diskToCanonicalEngine(normDisk), normDisk)
		}
		if explicitMemorySet && existing.Memory != "" && existing.Memory != normMem {
			existing.Memory = normMem
			isBackfill = true
		}
		if isBackfill {
			if err := WriteEngineManifestAtomic(dataDir, existing); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}

	created := &EngineManifest{
		Engine:            diskToCanonicalEngine(normDisk),
		Disk:              normDisk,
		Memory:            normMem,
		TupleCodecVersion: 2,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
		CreatedByVersion:  Version,
	}
	if err := WriteEngineManifestAtomic(dataDir, created); err != nil {
		return nil, err
	}
	return created, nil
}

// ResolveExistingEngineManifest inspects dataDir without creating files if the directory is fresh.
// If dataDir already contains lionsql.engine.json (or legacy engine markers), it returns the resolved manifest
// and validates against explicitEngineSet. If dataDir is fresh/uninitialized, it returns (nil, nil).
func ResolveExistingEngineManifest(dataDir string, requestedDisk string, explicitEngineSet bool) (*EngineManifest, error) {
	normDisk := NormalizeDiskBackend(requestedDisk)
	if normDisk == "" {
		normDisk = DefaultDisk()
	}

	existing, err := ReadEngineManifest(dataDir)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		existing = detectLegacyEngineSignature(dataDir)
	}
	if existing != nil {
		if explicitEngineSet && existing.Disk != normDisk {
			return nil, fmt.Errorf("%w: data directory %q was initialized with engine %q (disk=%s), but %q (disk=%s) was explicitly requested; use a different -dir or migrate via backup/restore",
				ErrEngineMismatch, dataDir, existing.Engine, existing.Disk, diskToCanonicalEngine(normDisk), normDisk)
		}
		return existing, nil
	}
	return nil, nil
}
