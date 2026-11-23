package catalog

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrSequenceExists    = errors.New("sequence already exists")
	ErrSequenceNotFound  = errors.New("sequence not found")
	ErrSequenceExhausted = errors.New("sequence exceeded maximum value")
)

// SequenceDef represents a persistent sequence object.
type SequenceDef struct {
	Name      string    `json:"name"`
	StartWith int64     `json:"start_with"`
	Increment int64     `json:"increment"`
	MinValue  int64     `json:"min_value"`
	MaxValue  int64     `json:"max_value"`
	Cycle     bool      `json:"cycle"`
	Current   int64     `json:"current"`
	HasNext   bool      `json:"has_next"` // true once NEXTVAL / NEXT VALUE FOR has been called
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	mu sync.Mutex `json:"-"`
}

// Next advances the sequence and returns the next value atomically.
func (s *SequenceDef) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.HasNext {
		s.HasNext = true
		s.Current = s.StartWith
		s.UpdatedAt = time.Now()
		return s.Current, nil
	}

	// Check overflow/bounds
	var nextVal int64
	if s.Increment > 0 {
		if s.Current > s.MaxValue-s.Increment {
			if s.Cycle {
				nextVal = s.MinValue
			} else {
				return 0, fmt.Errorf("%w: sequence %q reached maximum value %d", ErrSequenceExhausted, s.Name, s.MaxValue)
			}
		} else {
			nextVal = s.Current + s.Increment
		}
	} else if s.Increment < 0 {
		if s.Current < s.MinValue-s.Increment {
			if s.Cycle {
				nextVal = s.MaxValue
			} else {
				return 0, fmt.Errorf("%w: sequence %q reached minimum value %d", ErrSequenceExhausted, s.Name, s.MinValue)
			}
		} else {
			nextVal = s.Current + s.Increment
		}
	}

	s.Current = nextVal
	s.UpdatedAt = time.Now()
	return s.Current, nil
}

// Restart resets the current value of the sequence to a new restart value.
func (s *SequenceDef) Restart(val int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if val < s.MinValue || val > s.MaxValue {
		return fmt.Errorf("restart value %d is out of bounds [%d, %d]", val, s.MinValue, s.MaxValue)
	}
	s.Current = val
	s.StartWith = val
	s.HasNext = false
	s.UpdatedAt = time.Now()
	return nil
}

// RegisterSequence registers a sequence definition in the catalog.
// Returns ErrSequenceExists if a sequence with the same name already exists.
func (c *Catalog) RegisterSequence(seq *SequenceDef) error {
	if seq == nil || seq.Name == "" {
		return errors.New("invalid sequence definition")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(seq.Name)
	if c.sequences == nil {
		c.sequences = make(map[string]*SequenceDef)
	}
	if _, exists := c.sequences[key]; exists {
		return fmt.Errorf("%w: relation %q already exists", ErrSequenceExists, seq.Name)
	}
	c.sequences[key] = seq
	return nil
}

// GetSequence retrieves a sequence definition by name.
func (c *Catalog) GetSequence(name string) (*SequenceDef, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.sequences == nil {
		return nil, fmt.Errorf("%w: %s", ErrSequenceNotFound, name)
	}
	key := strings.ToLower(name)
	seq, exists := c.sequences[key]
	if !exists || seq == nil {
		return nil, fmt.Errorf("%w: %s", ErrSequenceNotFound, name)
	}
	return seq, nil
}

// DropSequence removes a sequence from the catalog.
func (c *Catalog) DropSequence(name string, ifExists bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sequences == nil {
		if ifExists {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrSequenceNotFound, name)
	}
	key := strings.ToLower(name)
	if _, exists := c.sequences[key]; !exists {
		if ifExists {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrSequenceNotFound, name)
	}
	delete(c.sequences, key)
	return nil
}

// ListSequences returns all registered sequences.
func (c *Catalog) ListSequences() []*SequenceDef {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*SequenceDef, 0, len(c.sequences))
	for _, s := range c.sequences {
		result = append(result, s)
	}
	return result
}
