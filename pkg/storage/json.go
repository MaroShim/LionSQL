package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

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

	var root any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
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

	var root any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
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

// JSONValueByPath extracts a scalar value from JSON string at specified path (e.g. "$.user.name" or "$.tags[0]").
// Per SQL standard, if target element is an object or array, JSON_VALUE returns nil.
func JSONValueByPath(doc string, path string) (any, bool) {
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

	var root any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
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
