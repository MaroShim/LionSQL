package tx

import (
	"fmt"
	"strings"
	"time"
)

// Savepoint represents a transactional savepoint marker.
type Savepoint struct {
	Name            string
	DMLOffset       int
	CreatedAt       time.Time
	DeferredOffset  int
	RollbackOffset  int
}

// SavepointStack manages a LIFO stack of savepoints within a transaction.
type SavepointStack struct {
	savepoints []Savepoint
}

// NewSavepointStack creates an empty savepoint stack.
func NewSavepointStack() *SavepointStack {
	return &SavepointStack{
		savepoints: make([]Savepoint, 0, 4),
	}
}

// Push adds a new savepoint to the stack. If a savepoint with the same name
// already exists, standard SQL dictates that the older one is replaced/shadowed.
func (s *SavepointStack) Push(name string, dmlOffset int) Savepoint {
	return s.PushWithOffsets(name, dmlOffset, 0, 0)
}

// PushWithOffsets adds a savepoint with recorded offsets for deferred and rollback action slices.
func (s *SavepointStack) PushWithOffsets(name string, dmlOffset, deferredOffset, rollbackOffset int) Savepoint {
	cleanName := strings.ToLower(strings.TrimSpace(name))
	sp := Savepoint{
		Name:           cleanName,
		DMLOffset:      dmlOffset,
		CreatedAt:      time.Now(),
		DeferredOffset: deferredOffset,
		RollbackOffset: rollbackOffset,
	}

	// If already exists, remove it first to place at top of stack
	for i, existing := range s.savepoints {
		if existing.Name == cleanName {
			s.savepoints = append(s.savepoints[:i], s.savepoints[i+1:]...)
			break
		}
	}

	s.savepoints = append(s.savepoints, sp)
	return sp
}

// RollbackTo rolls back the stack to the named savepoint.
// All savepoints created after this savepoint are removed.
// The named savepoint itself is retained (per SQL standard).
func (s *SavepointStack) RollbackTo(name string) (*Savepoint, error) {
	cleanName := strings.ToLower(strings.TrimSpace(name))
	idx := -1
	for i := len(s.savepoints) - 1; i >= 0; i-- {
		if s.savepoints[i].Name == cleanName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("savepoint %q does not exist", name)
	}

	target := s.savepoints[idx]
	// Truncate all savepoints created after the target savepoint
	s.savepoints = s.savepoints[:idx+1]
	return &target, nil
}

// Release removes the named savepoint and all savepoints created after it.
func (s *SavepointStack) Release(name string) error {
	cleanName := strings.ToLower(strings.TrimSpace(name))
	idx := -1
	for i := len(s.savepoints) - 1; i >= 0; i-- {
		if s.savepoints[i].Name == cleanName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("savepoint %q does not exist", name)
	}

	// Release removes this savepoint and all subsequent savepoints
	s.savepoints = s.savepoints[:idx]
	return nil
}

// Count returns the number of active savepoints in the stack.
func (s *SavepointStack) Count() int {
	return len(s.savepoints)
}

// Clear empties the savepoint stack.
func (s *SavepointStack) Clear() {
	s.savepoints = s.savepoints[:0]
}
