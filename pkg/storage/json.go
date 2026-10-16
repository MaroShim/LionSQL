package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// jsonDOMCache stores parsed JSON roots keyed by document string.
// Bounded to 1024 entries with FIFO eviction to strictly bound memory (Rule 23).
type jsonDOMCache struct {
	mu    sync.RWMutex
	items map[string]any
	keys  []string
	cap   int
}

var globalDOMCache = jsonDOMCache{
	items: make(map[string]any, 256),
	keys:  make([]string, 0, 256),
	cap:   1024,
}

func (c *jsonDOMCache) get(doc string) (any, bool) {
	c.mu.RLock()
	val, ok := c.items[doc]
	c.mu.RUnlock()
	return val, ok
}

func (c *jsonDOMCache) put(doc string, root any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[doc]; ok {
		return
	}
	if len(c.items) >= c.cap {
		evictCount := c.cap / 4
		for i := 0; i < evictCount && i < len(c.keys); i++ {
			delete(c.items, c.keys[i])
		}
		c.keys = c.keys[evictCount:]
	}
	c.items[doc] = root
	c.keys = append(c.keys, doc)
}

func getParsedJSONDOM(doc string) (any, error) {
	if root, ok := globalDOMCache.get(doc); ok {
		return root, nil
	}
	var root any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
		return nil, err
	}
	globalDOMCache.put(doc, root)
	return root, nil
}

func isWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n'
}

// ScanJSONTopLevelKey scans the raw JSON string doc for key at top-level object depth without building an AST/DOM.
// If unquote is true, string values have quotes stripped and escape sequences resolved.
// Returns (value, true) if found and cleanly extracted, or ("", false) if not found or if full DOM parsing is needed.
func ScanJSONTopLevelKey(doc string, key string, unquote bool) (string, bool) {
	doc = strings.TrimSpace(doc)
	if len(doc) < 2 || doc[0] != '{' || doc[len(doc)-1] != '}' {
		return "", false
	}

	i := 1
	n := len(doc) - 1

	for i < n {
		for i < n && isWhitespace(doc[i]) {
			i++
		}
		if i >= n || doc[i] == '}' {
			break
		}
		if doc[i] != '"' {
			return "", false
		}
		i++
		keyStart := i
		hasEscapes := false
		for i < n && doc[i] != '"' {
			if doc[i] == '\\' {
				hasEscapes = true
				i += 2
			} else {
				i++
			}
		}
		if i >= n {
			return "", false
		}
		rawKey := doc[keyStart:i]
		i++ // skip closing quote

		keyMatches := (!hasEscapes && rawKey == key)
		if hasEscapes {
			if unesc, err := strconv.Unquote(`"` + doc[keyStart-1:i] + `"`); err == nil && unesc == key {
				keyMatches = true
			}
		}

		for i < n && isWhitespace(doc[i]) {
			i++
		}
		if i >= n || doc[i] != ':' {
			return "", false
		}
		i++ // skip ':'
		for i < n && isWhitespace(doc[i]) {
			i++
		}
		if i >= n {
			return "", false
		}

		valStart := i
		if doc[i] == '"' {
			i++
			valStrStart := i
			hasValEscapes := false
			for i < n && doc[i] != '"' {
				if doc[i] == '\\' {
					hasValEscapes = true
					i += 2
				} else {
					i++
				}
			}
			if i >= n {
				return "", false
			}
			valStrEnd := i
			i++ // skip closing quote
			if keyMatches {
				if unquote {
					if hasValEscapes {
						if s, err := strconv.Unquote(`"` + doc[valStrStart-1:i] + `"`); err == nil {
							return s, true
						}
					}
					return doc[valStrStart:valStrEnd], true
				}
				return doc[valStart:i], true
			}
		} else if doc[i] == '{' || doc[i] == '[' {
			openChar := doc[i]
			closeChar := byte('}')
			if openChar == '[' {
				closeChar = ']'
			}
			depth := 1
			inStr := false
			escaped := false
			i++
			for i < n && depth > 0 {
				ch := doc[i]
				if inStr {
					if escaped {
						escaped = false
					} else if ch == '\\' {
						escaped = true
					} else if ch == '"' {
						inStr = false
					}
				} else {
					if ch == '"' {
						inStr = true
					} else if ch == openChar {
						depth++
					} else if ch == closeChar {
						depth--
					}
				}
				i++
			}
			if depth != 0 {
				return "", false
			}
			if keyMatches {
				return doc[valStart:i], true
			}
		} else {
			// Scalar (number, bool, null)
			for i < n && doc[i] != ',' && doc[i] != '}' && !isWhitespace(doc[i]) {
				i++
			}
			if keyMatches {
				return doc[valStart:i], true
			}
		}

		for i < n && isWhitespace(doc[i]) {
			i++
		}
		if i < n && doc[i] == ',' {
			i++
		} else if i < n && doc[i] == '}' {
			break
		} else {
			return "", false
		}
	}

	return "", false
}

// IsValidJSON reports whether s is a valid JSON-encoded string.
func IsValidJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return json.Valid([]byte(s))
}

// ExtractJSON implements -> operator: returns target element as formatted JSON string.
func ExtractJSON(doc string, keyOrIndex any) (string, bool) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return "", false
	}

	if keyStr, isStr := keyOrIndex.(string); isStr {
		trimmedKey := strings.Trim(keyStr, "'\"`")
		if raw, ok := ScanJSONTopLevelKey(doc, trimmedKey, false); ok {
			if !strings.HasPrefix(raw, "{") && !strings.HasPrefix(raw, "[") {
				return raw, true
			}
		}
	}

	root, err := getParsedJSONDOM(doc)
	if err != nil {
		return "", false
	}

	elem, ok := accessElement(root, keyOrIndex)
	if !ok || elem == nil {
		return "", false
	}

	b, err := json.Marshal(elem)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ExtractJSONText implements ->> operator: returns target element as unquoted text string.
func ExtractJSONText(doc string, keyOrIndex any) (string, bool) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return "", false
	}

	if keyStr, isStr := keyOrIndex.(string); isStr {
		trimmedKey := strings.Trim(keyStr, "'\"`")
		if raw, ok := ScanJSONTopLevelKey(doc, trimmedKey, true); ok {
			if !strings.HasPrefix(raw, "{") && !strings.HasPrefix(raw, "[") {
				return raw, true
			}
		}
	}

	root, err := getParsedJSONDOM(doc)
	if err != nil {
		return "", false
	}

	elem, ok := accessElement(root, keyOrIndex)
	if !ok || elem == nil {
		return "", false
	}

	switch v := elem.(type) {
	case string:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

func accessElement(root any, keyOrIndex any) (any, bool) {
	switch r := root.(type) {
	case map[string]any:
		key := fmt.Sprintf("%v", keyOrIndex)
		key = strings.Trim(key, "'\"`")
		val, ok := r[key]
		return val, ok

	case []any:
		var idx int
		switch k := keyOrIndex.(type) {
		case int:
			idx = k
		case int64:
			idx = int(k)
		case float64:
			idx = int(k)
		case string:
			k = strings.Trim(k, "'\"`")
			n, err := strconv.Atoi(k)
			if err != nil {
				return nil, false
			}
			idx = n
		default:
			return nil, false
		}

		if idx < 0 || idx >= len(r) {
			return nil, false
		}
		return r[idx], true

	default:
		return nil, false
	}
}

func parseSimpleTopLevelPath(path string) (string, bool) {
	p := strings.TrimSpace(path)
	pUpper := strings.ToUpper(p)
	if strings.HasPrefix(pUpper, "LAX ") {
		p = strings.TrimSpace(p[4:])
	} else if strings.HasPrefix(pUpper, "STRICT ") {
		p = strings.TrimSpace(p[7:])
	}
	p = strings.Trim(p, "'\"`")
	if strings.HasPrefix(p, "$") {
		p = p[1:]
	}
	p = strings.TrimPrefix(p, ".")
	if p == "" || strings.ContainsAny(p, ".[]") {
		return "", false
	}
	return p, true
}

// JSONValueByPath extracts a scalar value from JSON string at specified path (e.g. "$.user.name" or "$.tags[0]").
// Per SQL standard, if target element is an object or array, JSON_VALUE returns nil.
func JSONValueByPath(doc string, path string) (any, bool) {
	if topKey, ok := parseSimpleTopLevelPath(path); ok {
		if rawVal, found := ScanJSONTopLevelKey(doc, topKey, true); found {
			if rawVal == "null" {
				return nil, false
			}
			if strings.HasPrefix(rawVal, "{") || strings.HasPrefix(rawVal, "[") {
				return nil, false
			}
			if rawVal == "true" {
				return true, true
			}
			if rawVal == "false" {
				return false, true
			}
			if n, err := strconv.ParseInt(rawVal, 10, 64); err == nil {
				return n, true
			}
			if f, err := strconv.ParseFloat(rawVal, 64); err == nil {
				return f, true
			}
			return rawVal, true
		}
	}

	target, ok := navigatePath(doc, path)
	if !ok || target == nil {
		return nil, false
	}

	switch v := target.(type) {
	case map[string]any, []any:
		// JSON_VALUE only returns scalars
		return nil, false
	case string:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
		return v, true
	case bool:
		return v, true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

// JSONQueryByPath extracts an object or array from JSON string at specified path (e.g. "$.tags").
// Per SQL standard, if target element is a scalar, JSON_QUERY returns nil.
func JSONQueryByPath(doc string, path string) (string, bool) {
	if topKey, ok := parseSimpleTopLevelPath(path); ok {
		if rawVal, found := ScanJSONTopLevelKey(doc, topKey, false); found {
			if strings.HasPrefix(rawVal, "{") || strings.HasPrefix(rawVal, "[") {
				return rawVal, true
			}
			return "", false
		}
	}

	target, ok := navigatePath(doc, path)
	if !ok || target == nil {
		return "", false
	}

	switch v := target.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(b), true
	default:
		// JSON_QUERY only returns objects and arrays
		return "", false
	}
}

func navigatePath(doc string, path string) (any, bool) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return nil, false
	}

	root, err := getParsedJSONDOM(doc)
	if err != nil {
		return nil, false
	}

	p := strings.TrimSpace(path)
	pUpper := strings.ToUpper(p)
	if strings.HasPrefix(pUpper, "LAX ") {
		p = strings.TrimSpace(p[4:])
	} else if strings.HasPrefix(pUpper, "STRICT ") {
		p = strings.TrimSpace(p[7:])
	}

	p = strings.Trim(p, "'\"`")
	if strings.HasPrefix(p, "$") {
		p = p[1:]
	}
	p = strings.TrimPrefix(p, ".")

	if p == "" {
		return root, true
	}

	segments := parsePathSegments(p)

	curr := root
	for _, seg := range segments {
		if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
			idxStr := seg[1 : len(seg)-1]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, false
			}
			slice, ok := curr.([]any)
			if !ok || idx < 0 || idx >= len(slice) {
				return nil, false
			}
			curr = slice[idx]
		} else {
			m, ok := curr.(map[string]any)
			if !ok {
				return nil, false
			}
			val, exists := m[seg]
			if !exists {
				return nil, false
			}
			curr = val
		}
	}

	return curr, true
}

func parsePathSegments(p string) []string {
	var segments []string
	var cur strings.Builder

	for i := 0; i < len(p); i++ {
		ch := p[i]
		switch ch {
		case '.':
			if cur.Len() > 0 {
				segments = append(segments, cur.String())
				cur.Reset()
			}
		case '[':
			if cur.Len() > 0 {
				segments = append(segments, cur.String())
				cur.Reset()
			}
			cur.WriteByte(ch)
		case ']':
			cur.WriteByte(ch)
			segments = append(segments, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

// ExtractJSONIndexKey extracts an index key value from a JSON document at the specified path.
func ExtractJSONIndexKey(docStr string, path string) Value {
	if docStr == "" {
		return NullValue(TypeVarChar)
	}
	val, ok := JSONValueByPath(docStr, path)
	if !ok || val == nil {
		return NullValue(TypeVarChar)
	}
	switch v := val.(type) {
	case string:
		return StringValue(v)
	case int64:
		return IntValue(v)
	case int:
		return IntValue(int64(v))
	case float64:
		if v == float64(int64(v)) {
			return IntValue(int64(v))
		}
		return FloatValue(v)
	case bool:
		return BoolValue(v)
	default:
		return StringValue(fmt.Sprintf("%v", v))
	}
}

