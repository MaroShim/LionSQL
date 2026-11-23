package tx

import (
	"strings"
	"sync"

	"lionsql/pkg/storage"
)

// ShadowCatalog maintains a copy-on-write overlay of schema changes
// executed within a transaction to isolate uncommitted DDL from concurrent sessions,
// completely eliminating Dirty DDL Read anomalies.
type ShadowCatalog struct {
	mu            sync.RWMutex
	droppedTables map[string]bool
	createdTables map[string]*storage.TableSchema
	alteredTables map[string]*storage.TableSchema
}

// NewShadowCatalog creates an empty shadow catalog overlay.
func NewShadowCatalog() *ShadowCatalog {
	return &ShadowCatalog{
		droppedTables: make(map[string]bool),
		createdTables: make(map[string]*storage.TableSchema),
		alteredTables: make(map[string]*storage.TableSchema),
	}
}

// IsTableDropped returns true if the table has been marked dropped within this transaction.
func (s *ShadowCatalog) IsTableDropped(tableName string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.droppedTables[strings.ToLower(tableName)]
}

// GetTable returns the shadowed schema if created or altered within this transaction.
// If the table was marked as dropped, it returns nil, false.
func (s *ShadowCatalog) GetTable(tableName string) (*storage.TableSchema, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	lower := strings.ToLower(tableName)
	if s.droppedTables[lower] {
		return nil, false
	}
	if sch, ok := s.alteredTables[lower]; ok {
		return sch, true
	}
	if sch, ok := s.createdTables[lower]; ok {
		return sch, true
	}
	return nil, false
}

// MarkTableDropped marks a table as dropped in this shadow catalog.
func (s *ShadowCatalog) MarkTableDropped(tableName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lower := strings.ToLower(tableName)
	s.droppedTables[lower] = true
	delete(s.createdTables, lower)
	delete(s.alteredTables, lower)
}

// UnmarkTableDropped removes a dropped table marker (e.g. when rolling back to a savepoint).
func (s *ShadowCatalog) UnmarkTableDropped(tableName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.droppedTables, strings.ToLower(tableName))
}

// MarkTableCreated marks a newly created table in this shadow catalog.
func (s *ShadowCatalog) MarkTableCreated(sch *storage.TableSchema) {
	if s == nil || sch == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lower := strings.ToLower(sch.Name)
	delete(s.droppedTables, lower)
	cloned := sch.Clone()
	s.createdTables[lower] = &cloned
}

// MarkTableAltered marks an altered table schema in this shadow catalog.
func (s *ShadowCatalog) MarkTableAltered(sch *storage.TableSchema) {
	if s == nil || sch == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lower := strings.ToLower(sch.Name)
	delete(s.droppedTables, lower)
	cloned := sch.Clone()
	s.alteredTables[lower] = &cloned
}

// Clear discards all overlay entries in this shadow catalog.
func (s *ShadowCatalog) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.droppedTables)
	clear(s.createdTables)
	clear(s.alteredTables)
}
