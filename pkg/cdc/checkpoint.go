package cdc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CheckpointEntry represents a single consumer's checkpoint state.
type CheckpointEntry struct {
	ConsumerID string    `json:"consumer_id"`
	Table      string    `json:"table"`
	LastLSN    int64     `json:"last_lsn"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CheckpointStore specifies the persistence interface for CDC consumer progress checkpoints.
type CheckpointStore interface {
	SaveCheckpoint(consumerID, table string, lsn int64) error
	GetCheckpoint(consumerID, table string) (int64, error)
	ListCheckpoints() ([]CheckpointEntry, error)
	Close() error
}

// FileCheckpointStore persists checkpoints to disk in atomic JSON format ([RULE:AtomicSaves]).
type FileCheckpointStore struct {
	mu       sync.RWMutex
	filePath string
	entries  map[string]CheckpointEntry // key: "consumerID:table"
	closed   bool
}

// NewFileCheckpointStore creates or loads a file-backed CheckpointStore.
func NewFileCheckpointStore(dir string, filename string) (*FileCheckpointStore, error) {
	if filename == "" {
		filename = "cdc_checkpoints.json"
	}
	cleanDir := filepath.Clean(dir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	targetPath := filepath.Join(cleanDir, filename)
	store := &FileCheckpointStore{
		filePath: targetPath,
		entries:  make(map[string]CheckpointEntry),
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load checkpoints: %w", err)
	}

	return store, nil
}

func makeCheckpointKey(consumerID, table string) string {
	return fmt.Sprintf("%s:%s", consumerID, table)
}

func (s *FileCheckpointStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var list []CheckpointEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	for _, e := range list {
		key := makeCheckpointKey(e.ConsumerID, e.Table)
		s.entries[key] = e
	}
	return nil
}

// SaveCheckpoint updates or creates a consumer checkpoint and flushes atomically to disk.
func (s *FileCheckpointStore) SaveCheckpoint(consumerID, table string, lsn int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("checkpoint store is closed")
	}

	key := makeCheckpointKey(consumerID, table)
	s.entries[key] = CheckpointEntry{
		ConsumerID: consumerID,
		Table:      table,
		LastLSN:    lsn,
		UpdatedAt:  time.Now(),
	}

	return s.persistLocked()
}

// GetCheckpoint retrieves the last committed LSN for a given consumer and table.
func (s *FileCheckpointStore) GetCheckpoint(consumerID, table string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := makeCheckpointKey(consumerID, table)
	if entry, ok := s.entries[key]; ok {
		return entry.LastLSN, nil
	}
	return 0, nil
}

// ListCheckpoints returns all stored checkpoint entries.
func (s *FileCheckpointStore) ListCheckpoints() ([]CheckpointEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]CheckpointEntry, 0, len(s.entries))
	for _, e := range s.entries {
		res = append(res, e)
	}
	return res, nil
}

func (s *FileCheckpointStore) persistLocked() error {
	list := make([]CheckpointEntry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, e)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoints: %w", err)
	}
	data = append(data, '\n')

	cleanPath := filepath.Clean(s.filePath)
	tmpPath := fmt.Sprintf("%s.tmp.%d", cleanPath, time.Now().UnixNano())

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp checkpoint: %w", err)
	}

	if err := os.Rename(tmpPath, cleanPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename atomic checkpoint file: %w", err)
	}

	return nil
}

// Close flushes and shuts down the store.
func (s *FileCheckpointStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// MemoryCheckpointStore provides an in-memory implementation for testing or ephemeral sessions.
type MemoryCheckpointStore struct {
	mu      sync.RWMutex
	entries map[string]CheckpointEntry
	closed  bool
}

// NewMemoryCheckpointStore initializes an in-memory checkpoint store.
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{
		entries: make(map[string]CheckpointEntry),
	}
}

func (m *MemoryCheckpointStore) SaveCheckpoint(consumerID, table string, lsn int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("checkpoint store is closed")
	}
	key := makeCheckpointKey(consumerID, table)
	m.entries[key] = CheckpointEntry{
		ConsumerID: consumerID,
		Table:      table,
		LastLSN:    lsn,
		UpdatedAt:  time.Now(),
	}
	return nil
}

func (m *MemoryCheckpointStore) GetCheckpoint(consumerID, table string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := makeCheckpointKey(consumerID, table)
	if entry, ok := m.entries[key]; ok {
		return entry.LastLSN, nil
	}
	return 0, nil
}

func (m *MemoryCheckpointStore) ListCheckpoints() ([]CheckpointEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]CheckpointEntry, 0, len(m.entries))
	for _, e := range m.entries {
		res = append(res, e)
	}
	return res, nil
}

func (m *MemoryCheckpointStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
