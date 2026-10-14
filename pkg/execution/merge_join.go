package execution

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// MergeJoinIterator implements an O(M+N) single-pass Sort-Merge Join between two sorted relations.
// Both child iterators are expected to stream tuples ordered by their respective join keys.
// If either side contains duplicate keys, the right-side matches are buffered into a window
// to handle N:M duplicate key Cartesian products without resetting the entire stream.
type MergeJoinIterator struct {
	left        Iterator
	right       Iterator
	joinType    string // "INNER", "LEFT", "RIGHT", "FULL", "SEMI", "ANTI"
	leftKey     string
	rightKey    string
	colIndexMap *ColumnIndexMap

	// Current streaming state
	curLeft       Tuple
	curRight      Tuple
	leftEOF       bool
	rightEOF      bool
	opened        bool
	closed        bool
	closeOnce     sync.Once

	// Mark & Restore buffer for duplicate keys on the right relation
	bufferedRight []Tuple
	bufIdx        int

	leftColsCount  int
	rightColsCount int
}

// NewMergeJoinIterator creates a new Sort-Merge Join operator.
func NewMergeJoinIterator(left, right Iterator, joinType, leftKey, rightKey string) *MergeJoinIterator {
	jType := strings.ToUpper(strings.TrimSpace(joinType))
	if jType == "" {
		jType = "INNER"
	}
	// Strip "MERGE" if passed as "INNER MERGE" or "LEFT MERGE"
	jType = strings.TrimSpace(strings.ReplaceAll(jType, "MERGE", ""))
	if jType == "" {
		jType = "INNER"
	}

	return &MergeJoinIterator{
		left:     left,
		right:    right,
		joinType: jType,
		leftKey:  strings.TrimSpace(leftKey),
		rightKey: strings.TrimSpace(rightKey),
	}
}

func (m *MergeJoinIterator) Open(ctx context.Context) error {
	if m.left == nil || m.right == nil {
		return fmt.Errorf("merge join: child iterators cannot be nil")
	}

	// Defensive key orientation check: if right contains leftKey and not rightKey, swap.
	if rMap := m.right.ColIndexMap(); rMap != nil {
		rightCol := m.rightKey
		if idx := strings.Index(rightCol, "."); idx != -1 {
			rightCol = rightCol[idx+1:]
		}
		leftCol := m.leftKey
		if idx := strings.Index(leftCol, "."); idx != -1 {
			leftCol = leftCol[idx+1:]
		}

		_, hasRight := rMap.Index(m.rightKey)
		if !hasRight {
			_, hasRight = rMap.Index(rightCol)
		}
		_, hasLeft := rMap.Index(m.leftKey)
		if !hasLeft {
			_, hasLeft = rMap.Index(leftCol)
		}

		if hasLeft && !hasRight {
			m.leftKey, m.rightKey = m.rightKey, m.leftKey
		}
	}

	if err := m.left.Open(ctx); err != nil {
		return fmt.Errorf("merge join left open error: %w", err)
	}
	if err := m.right.Open(ctx); err != nil {
		_ = m.left.Close()
		return fmt.Errorf("merge join right open error: %w", err)
	}

	if m.joinType == "SEMI" || m.joinType == "ANTI" {
		m.colIndexMap = m.left.ColIndexMap()
	} else {
		m.colIndexMap = MergeColumnIndexMaps(m.left.ColIndexMap(), m.right.ColIndexMap())
	}

	if m.left.ColIndexMap() != nil {
		m.leftColsCount = len(m.left.ColIndexMap().Columns())
	} else {
		m.leftColsCount = len(m.left.Schema())
	}

	if m.right.ColIndexMap() != nil {
		m.rightColsCount = len(m.right.ColIndexMap().Columns())
	} else {
		m.rightColsCount = len(m.right.Schema())
	}

	// Fetch initial tuples
	lTuple, err := m.left.Next(ctx)
	if err != nil {
		if err == io.EOF {
			m.leftEOF = true
		} else {
			return err
		}
	} else {
		m.curLeft = lTuple
	}

	rTuple, err := m.right.Next(ctx)
	if err != nil {
		if err == io.EOF {
			m.rightEOF = true
		} else {
			return err
		}
	} else {
		m.curRight = rTuple
	}

	m.opened = true
	m.bufferedRight = nil
	m.bufIdx = 0
	return nil
}

func (m *MergeJoinIterator) advanceLeft(ctx context.Context) error {
	nextLeft, err := m.left.Next(ctx)
	if err != nil {
		if err == io.EOF {
			m.leftEOF = true
			m.curLeft = Tuple{}
			return nil
		}
		return err
	}
	m.curLeft = nextLeft
	return nil
}

func (m *MergeJoinIterator) advanceRight(ctx context.Context) error {
	nextRight, err := m.right.Next(ctx)
	if err != nil {
		if err == io.EOF {
			m.rightEOF = true
			m.curRight = Tuple{}
			return nil
		}
		return err
	}
	m.curRight = nextRight
	return nil
}

func (m *MergeJoinIterator) Next(ctx context.Context) (Tuple, error) {
	if !m.opened || m.closed {
		return Tuple{}, io.EOF
	}

	for {
		select {
		case <-ctx.Done():
			return Tuple{}, ctx.Err()
		default:
		}

		// 1. Replay buffered Right matches for the current Left tuple
		if len(m.bufferedRight) > 0 {
			if m.bufIdx < len(m.bufferedRight) {
				rTuple := m.bufferedRight[m.bufIdx]
				m.bufIdx++

				if m.joinType == "SEMI" {
					// SEMI join: emit left row once and advance left immediately
					m.bufferedRight = nil
					m.bufIdx = 0
					t := m.curLeft
					if err := m.advanceLeft(ctx); err != nil {
						return Tuple{}, err
					}
					return t, nil
				}
				if m.joinType == "ANTI" {
					// ANTI join: matched left row is suppressed entirely
					m.bufferedRight = nil
					m.bufIdx = 0
					if err := m.advanceLeft(ctx); err != nil {
						return Tuple{}, err
					}
					continue
				}

				return mergeTuples(m.curLeft, rTuple, m.colIndexMap), nil
			}

			// Finished current buffer for m.curLeft. Advance Left.
			prevLeftKey, _ := lookupVal(m.curLeft, m.leftKey)
			if err := m.advanceLeft(ctx); err != nil {
				return Tuple{}, err
			}

			// If the newly advanced Left tuple has the exact same key as prevLeftKey, replay buffer!
			if !m.leftEOF {
				newLeftKey, found := lookupVal(m.curLeft, m.leftKey)
				if found && newLeftKey != nil && CompareValues(newLeftKey, prevLeftKey) == 0 {
					m.bufIdx = 0
					continue
				}
			}

			// Left key changed or Left reached EOF: buffer is fully drained
			m.bufferedRight = nil
			m.bufIdx = 0
		}

		// 2. Both streams exhausted?
		if m.leftEOF && m.rightEOF {
			return Tuple{}, io.EOF
		}

		// 3. Left stream is exhausted, Right stream remains
		if m.leftEOF {
			if m.joinType == "RIGHT" || m.joinType == "FULL" {
				rTuple := m.curRight
				if err := m.advanceRight(ctx); err != nil {
					return Tuple{}, err
				}
				return mergeNullsWithTuple(m.leftColsCount, rTuple, m.colIndexMap), nil
			}
			return Tuple{}, io.EOF
		}

		// 4. Right stream is exhausted, Left stream remains
		if m.rightEOF {
			if m.joinType == "LEFT" || m.joinType == "FULL" || m.joinType == "ANTI" {
				lTuple := m.curLeft
				if err := m.advanceLeft(ctx); err != nil {
					return Tuple{}, err
				}
				if m.joinType == "ANTI" {
					return lTuple, nil
				}
				return mergeTuplesWithNulls(lTuple, m.rightColsCount, m.colIndexMap), nil
			}
			return Tuple{}, io.EOF
		}

		// 5. Both streams have active tuples: compare keys
		vLeft, foundLeft := lookupVal(m.curLeft, m.leftKey)
		vRight, foundRight := lookupVal(m.curRight, m.rightKey)

		// SQL equality rules: NULL never equals NULL in join predicates
		if !foundLeft || vLeft == nil {
			lTuple := m.curLeft
			if err := m.advanceLeft(ctx); err != nil {
				return Tuple{}, err
			}
			if m.joinType == "LEFT" || m.joinType == "FULL" || m.joinType == "ANTI" {
				if m.joinType == "ANTI" {
					return lTuple, nil
				}
				return mergeTuplesWithNulls(lTuple, m.rightColsCount, m.colIndexMap), nil
			}
			continue
		}

		if !foundRight || vRight == nil {
			rTuple := m.curRight
			if err := m.advanceRight(ctx); err != nil {
				return Tuple{}, err
			}
			if m.joinType == "RIGHT" || m.joinType == "FULL" {
				return mergeNullsWithTuple(m.leftColsCount, rTuple, m.colIndexMap), nil
			}
			continue
		}

		cmp := CompareValues(vLeft, vRight)
		if cmp < 0 {
			// curLeft < curRight: Left has no match on Right
			lTuple := m.curLeft
			if err := m.advanceLeft(ctx); err != nil {
				return Tuple{}, err
			}
			if m.joinType == "LEFT" || m.joinType == "FULL" || m.joinType == "ANTI" {
				if m.joinType == "ANTI" {
					return lTuple, nil
				}
				return mergeTuplesWithNulls(lTuple, m.rightColsCount, m.colIndexMap), nil
			}
			continue
		} else if cmp > 0 {
			// curLeft > curRight: Right has no match on Left
			rTuple := m.curRight
			if err := m.advanceRight(ctx); err != nil {
				return Tuple{}, err
			}
			if m.joinType == "RIGHT" || m.joinType == "FULL" {
				return mergeNullsWithTuple(m.leftColsCount, rTuple, m.colIndexMap), nil
			}
			continue
		} else {
			// cmp == 0: MATCH!
			// Buffer all contiguous Right tuples that share this identical key.
			m.bufferedRight = []Tuple{m.curRight}
			targetKey := vRight

			for {
				nextRight, err := m.right.Next(ctx)
				if err != nil {
					if err == io.EOF {
						m.rightEOF = true
						m.curRight = Tuple{}
						break
					}
					return Tuple{}, err
				}
				nrKey, found := lookupVal(nextRight, m.rightKey)
				if found && nrKey != nil && CompareValues(nrKey, targetKey) == 0 {
					m.bufferedRight = append(m.bufferedRight, nextRight)
				} else {
					m.curRight = nextRight
					break
				}
			}

			m.bufIdx = 0
			// Loop continues to replayer in Section 1
		}
	}
}

func (m *MergeJoinIterator) Close() error {
	var firstErr error
	m.closeOnce.Do(func() {
		m.closed = true
		if m.left != nil {
			if err := m.left.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if m.right != nil {
			if err := m.right.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

func (m *MergeJoinIterator) Schema() []string {
	if m.joinType == "SEMI" || m.joinType == "ANTI" {
		if m.left != nil {
			return m.left.Schema()
		}
		return nil
	}
	var cols []string
	if m.left != nil {
		cols = append(cols, m.left.Schema()...)
	}
	if m.right != nil {
		cols = append(cols, m.right.Schema()...)
	}
	return cols
}

func (m *MergeJoinIterator) ColIndexMap() *ColumnIndexMap {
	if m.joinType == "SEMI" || m.joinType == "ANTI" {
		if m.left != nil {
			return m.left.ColIndexMap()
		}
		return nil
	}
	if m.colIndexMap == nil && m.left != nil && m.right != nil {
		m.colIndexMap = MergeColumnIndexMaps(m.left.ColIndexMap(), m.right.ColIndexMap())
	}
	return m.colIndexMap
}
