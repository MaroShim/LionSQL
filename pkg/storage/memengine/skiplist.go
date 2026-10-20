package memengine

import (
	"fmt"
	"math/rand/v2"
	"sync"

	"lionsql/pkg/storage"
)

const (
	maxSkipListLevel = 16
	skipListP        = 0.5
)

// IndexItem associates a primary key reference and optional included covering columns with an index entry.
type IndexItem struct {
	PK            string          `json:"pk"`
	IncludeValues []storage.Value `json:"include_values,omitempty"`
}

// SkipListNode represents an individual element in the SkipList.
type SkipListNode struct {
	Key     storage.Value
	Items   []IndexItem
	forward []*SkipListNode
}

// SkipList is a high-performance, concurrent, probabilistic sorted index structure.
// It provides O(log N) point search, insertion, and deletion, with O(1) sequential range iteration.
type SkipList struct {
	mu     sync.RWMutex
	head   *SkipListNode
	level  int
	length int
	unique bool
}

// NewSkipList creates an initialized SkipList for index indexing.
func NewSkipList(unique bool) *SkipList {
	head := &SkipListNode{
		forward: make([]*SkipListNode, maxSkipListLevel),
	}
	return &SkipList{
		head:   head,
		level:  1,
		unique: unique,
	}
}

func (sl *SkipList) randomLevel() int {
	lvl := 1
	for lvl < maxSkipListLevel && rand.Float64() < skipListP {
		lvl++
	}
	return lvl
}

// Insert adds a primary key (pk) reference associated with the specified index key.
// Enforces uniqueness constraint if the SkipList was created with unique = true.
// Optional incVals specifies non-key covering values for covering index scans.
func (sl *SkipList) Insert(key storage.Value, pk string, incVals ...[]storage.Value) error {
	var inc []storage.Value
	if len(incVals) > 0 {
		inc = incVals[0]
	}

	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := make([]*SkipListNode, maxSkipListLevel)
	curr := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for curr.forward[i] != nil && curr.forward[i].Key.Compare(key) < 0 {
			curr = curr.forward[i]
		}
		update[i] = curr
	}

	curr = curr.forward[0]

	// Key already exists in SkipList
	if curr != nil && curr.Key.Equal(key) {
		if sl.unique && !key.IsNull && len(curr.Items) > 0 {
			for i := range curr.Items {
				if curr.Items[i].PK == pk {
					curr.Items[i].IncludeValues = inc
					return nil // Already indexed for this exact PK
				}
			}
			return fmt.Errorf("%w: duplicate key %q", storage.ErrUniqueViolation, key.String())
		}

		for i := range curr.Items {
			if curr.Items[i].PK == pk {
				curr.Items[i].IncludeValues = inc
				return nil
			}
		}
		curr.Items = append(curr.Items, IndexItem{PK: pk, IncludeValues: inc})
		return nil
	}

	// Insert new node
	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			update[i] = sl.head
		}
		sl.level = lvl
	}

	newNode := &SkipListNode{
		Key:     key,
		Items:   []IndexItem{{PK: pk, IncludeValues: inc}},
		forward: make([]*SkipListNode, lvl),
	}

	for i := 0; i < lvl; i++ {
		newNode.forward[i] = update[i].forward[i]
		update[i].forward[i] = newNode
	}
	sl.length++
	return nil
}

// Delete removes a primary key (pk) reference for the given index key.
// If no PKs remain for the key, the node is pruned from the SkipList.
func (sl *SkipList) Delete(key storage.Value, pk string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := make([]*SkipListNode, maxSkipListLevel)
	curr := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for curr.forward[i] != nil && curr.forward[i].Key.Compare(key) < 0 {
			curr = curr.forward[i]
		}
		update[i] = curr
	}

	curr = curr.forward[0]
	if curr == nil || !curr.Key.Equal(key) {
		return
	}

	// Filter out target pk
	filtered := make([]IndexItem, 0, len(curr.Items))
	for _, it := range curr.Items {
		if it.PK != pk {
			filtered = append(filtered, it)
		}
	}

	if len(filtered) > 0 {
		curr.Items = filtered
		return
	}

	// Unlink node across all levels
	for i := 0; i < sl.level; i++ {
		if update[i].forward[i] != curr {
			break
		}
		update[i].forward[i] = curr.forward[i]
	}

	// Adjust active maximum level
	for sl.level > 1 && sl.head.forward[sl.level-1] == nil {
		sl.level--
	}
	sl.length--
}

// Search looks up exact matching PKs for the specified key.
func (sl *SkipList) Search(key storage.Value) ([]string, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	curr := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for curr.forward[i] != nil && curr.forward[i].Key.Compare(key) < 0 {
			curr = curr.forward[i]
		}
	}

	curr = curr.forward[0]
	if curr != nil && curr.Key.Equal(key) {
		result := make([]string, len(curr.Items))
		for i, it := range curr.Items {
			result[i] = it.PK
		}
		return result, true
	}
	return nil, false
}

// ScanRange returns all PKs whose indexed keys fall within [low, high] inclusive.
// If low is nil, the scan starts from the smallest key.
// If high is nil, the scan continues to the largest key.
func (sl *SkipList) ScanRange(low, high *storage.Value) []string {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var result []string
	curr := sl.head

	if low != nil {
		for i := sl.level - 1; i >= 0; i-- {
			for curr.forward[i] != nil && curr.forward[i].Key.Compare(*low) < 0 {
				curr = curr.forward[i]
			}
		}
		curr = curr.forward[0]
	} else {
		curr = curr.forward[0]
	}

	for curr != nil {
		if high != nil && curr.Key.Compare(*high) > 0 {
			break
		}
		for _, item := range curr.Items {
			result = append(result, item.PK)
		}
		curr = curr.forward[0]
	}

	return result
}

// SkipListEntry represents a key-PK pair from the index, along with optional included covering columns.
type SkipListEntry struct {
	Key           storage.Value
	PK            string
	IncludeValues []storage.Value
}

// ScanRangeEntries returns all (Key, PK, IncludeValues) entries whose indexed keys fall within [low, high] inclusive.
func (sl *SkipList) ScanRangeEntries(low, high *storage.Value) []SkipListEntry {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var result []SkipListEntry
	curr := sl.head

	if low != nil {
		for i := sl.level - 1; i >= 0; i-- {
			for curr.forward[i] != nil && curr.forward[i].Key.Compare(*low) < 0 {
				curr = curr.forward[i]
			}
		}
		curr = curr.forward[0]
	} else {
		curr = curr.forward[0]
	}

	for curr != nil {
		if high != nil && curr.Key.Compare(*high) > 0 {
			break
		}
		for _, item := range curr.Items {
			result = append(result, SkipListEntry{
				Key:           curr.Key,
				PK:            item.PK,
				IncludeValues: item.IncludeValues,
			})
		}
		curr = curr.forward[0]
	}

	return result
}

// Len returns the number of distinct keys in the SkipList.
func (sl *SkipList) Len() int {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.length
}

// ToEntries serializes the SkipList into an ordered slice of IndexEntry for snapshot persistence.
func (sl *SkipList) ToEntries() []IndexEntry {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	entries := make([]IndexEntry, 0, sl.length)
	curr := sl.head.forward[0]
	for curr != nil {
		pks := make([]string, len(curr.Items))
		var incVals [][]storage.Value
		hasInc := false
		for i, it := range curr.Items {
			pks[i] = it.PK
			if len(it.IncludeValues) > 0 {
				hasInc = true
			}
		}
		if hasInc {
			incVals = make([][]storage.Value, len(curr.Items))
			for i, it := range curr.Items {
				incVals[i] = it.IncludeValues
			}
		}
		entries = append(entries, IndexEntry{
			Key:           curr.Key,
			PKs:           pks,
			IncludeValues: incVals,
		})
		curr = curr.forward[0]
	}
	return entries
}

// FromEntries populates the SkipList from a slice of IndexEntry (used during snapshot restoration).
func (sl *SkipList) FromEntries(entries []IndexEntry) error {
	for _, entry := range entries {
		for i, pk := range entry.PKs {
			var inc []storage.Value
			if i < len(entry.IncludeValues) {
				inc = entry.IncludeValues[i]
			}
			if err := sl.Insert(entry.Key, pk, inc); err != nil {
				return err
			}
		}
	}
	return nil
}
