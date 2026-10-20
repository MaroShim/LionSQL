package sqlutil

import (
	"strings"
)

// CanonicalIdent trims leading/trailing spaces, strips bounding brackets (`[...]`),
// double quotes (`"..."`), backticks (`` `...` ``), and single quotes (`'...'`),
// and converts the identifier to lowercase. This provides a normalized O(1) hash key
// for case-insensitive and quote-tolerant SQL identifiers.
func CanonicalIdent(s string) string {
	cleaned := CleanIdentifier(s)
	return strings.ToLower(cleaned)
}

// EqualFoldIdent compares two SQL identifiers for equality, ignoring bounding quotes/brackets
// and case differences (e.g. `[Users]` equals `"users"` and `USERS`).
func EqualFoldIdent(a, b string) bool {
	ca := CanonicalIdent(a)
	cb := CanonicalIdent(b)
	return ca == cb
}

// SliceContainsFold returns true if the slice contains target ignoring case and identifier quoting.
func SliceContainsFold(slice []string, target string) bool {
	normTarget := CanonicalIdent(target)
	for _, item := range slice {
		if CanonicalIdent(item) == normTarget {
			return true
		}
	}
	return false
}

// MapGetFold performs a case-insensitive, quote-tolerant lookup on a standard Go map.
// Order of resolution:
// 1. Exact key match (fast path O(1))
// 2. Canonical key match (O(N) fallback if keys in map are not canonicalized)
func MapGetFold[V any](m map[string]V, key string) (V, bool) {
	if len(m) == 0 {
		var zero V
		return zero, false
	}
	// Fast path: exact match
	if v, ok := m[key]; ok {
		return v, true
	}

	normKey := CanonicalIdent(key)
	// Try canonical directly if map is keyed canonically
	if v, ok := m[normKey]; ok {
		return v, true
	}

	// Fallback: iterate and compare canonically
	for k, v := range m {
		if CanonicalIdent(k) == normKey {
			return v, true
		}
	}

	var zero V
	return zero, false
}

// MapSetFold updates or inserts a value into a standard Go map case-insensitively.
// If an existing key matches canonically, its entry is updated under that existing key,
// preserving the original casing in the map and preventing duplicate case-variant keys.
// Otherwise, it is inserted under the provided key.
func MapSetFold[V any](m map[string]V, key string, val V) {
	if m == nil {
		return
	}
	// Check for existing key matching canonically
	normKey := CanonicalIdent(key)
	for k := range m {
		if CanonicalIdent(k) == normKey {
			m[k] = val
			return
		}
	}
	m[key] = val
}

// MapDeleteFold deletes an entry from a standard Go map case-insensitively and quote-tolerantly.
// Returns true if a key was found and removed.
func MapDeleteFold[V any](m map[string]V, key string) bool {
	if len(m) == 0 {
		return false
	}
	if _, ok := m[key]; ok {
		delete(m, key)
		return true
	}
	normKey := CanonicalIdent(key)
	for k := range m {
		if CanonicalIdent(k) == normKey {
			delete(m, k)
			return true
		}
	}
	return false
}

// MapFindKeyFold searches a standard Go map for an existing key matching target canonically,
// returning the actual key stored in the map.
func MapFindKeyFold[V any](m map[string]V, key string) (string, bool) {
	if len(m) == 0 {
		return "", false
	}
	if _, ok := m[key]; ok {
		return key, true
	}
	normKey := CanonicalIdent(key)
	for k := range m {
		if CanonicalIdent(k) == normKey {
			return k, true
		}
	}
	return "", false
}

// ciEntry holds the value and original casing for CIMap.
type ciEntry[V any] struct {
	originalKey string
	value       V
}

// CIMap is a high-performance, generic case-insensitive and quote-tolerant hash map.
// It stores normalized canonical identifiers as hash keys for O(1) operations,
// while preserving the original identifier casing for DDL/schema output.
type CIMap[V any] struct {
	entries map[string]ciEntry[V]
}

// NewCIMap creates a new initialized CIMap with an optional initial capacity.
func NewCIMap[V any](capacity ...int) *CIMap[V] {
	capVal := 0
	if len(capacity) > 0 && capacity[0] > 0 {
		capVal = capacity[0]
	}
	return &CIMap[V]{
		entries: make(map[string]ciEntry[V], capVal),
	}
}

// Len returns the number of items in the map.
func (m *CIMap[V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.entries)
}

// Get retrieves a value by key case-insensitively and quote-tolerantly in O(1) time.
func (m *CIMap[V]) Get(key string) (V, bool) {
	if m == nil || len(m.entries) == 0 {
		var zero V
		return zero, false
	}
	entry, ok := m.entries[CanonicalIdent(key)]
	if !ok {
		var zero V
		return zero, false
	}
	return entry.value, true
}

// Has returns true if the key exists in the map.
func (m *CIMap[V]) Has(key string) bool {
	_, ok := m.Get(key)
	return ok
}

// Set stores a key-value pair. If the key already exists case-insensitively,
// the value is updated while keeping the first recorded original casing.
func (m *CIMap[V]) Set(key string, val V) {
	if m == nil {
		return
	}
	if m.entries == nil {
		m.entries = make(map[string]ciEntry[V])
	}
	norm := CanonicalIdent(key)
	if existing, ok := m.entries[norm]; ok {
		m.entries[norm] = ciEntry[V]{
			originalKey: existing.originalKey,
			value:       val,
		}
		return
	}
	m.entries[norm] = ciEntry[V]{
		originalKey: CleanIdentifier(key),
		value:       val,
	}
}

// Delete removes an entry by key case-insensitively. Returns true if found.
func (m *CIMap[V]) Delete(key string) bool {
	if m == nil || len(m.entries) == 0 {
		return false
	}
	norm := CanonicalIdent(key)
	if _, ok := m.entries[norm]; ok {
		delete(m.entries, norm)
		return true
	}
	return false
}

// OriginalKey returns the original key as preserved upon insertion.
func (m *CIMap[V]) OriginalKey(key string) (string, bool) {
	if m == nil || len(m.entries) == 0 {
		return "", false
	}
	entry, ok := m.entries[CanonicalIdent(key)]
	if !ok {
		return "", false
	}
	return entry.originalKey, true
}

// Keys returns all original keys stored in the map.
func (m *CIMap[V]) Keys() []string {
	if m == nil || len(m.entries) == 0 {
		return nil
	}
	res := make([]string, 0, len(m.entries))
	for _, entry := range m.entries {
		res = append(res, entry.originalKey)
	}
	return res
}

// Values returns all values stored in the map.
func (m *CIMap[V]) Values() []V {
	if m == nil || len(m.entries) == 0 {
		return nil
	}
	res := make([]V, 0, len(m.entries))
	for _, entry := range m.entries {
		res = append(res, entry.value)
	}
	return res
}

// Clear removes all elements from the map.
func (m *CIMap[V]) Clear() {
	if m != nil && m.entries != nil {
		clear(m.entries)
	}
}
