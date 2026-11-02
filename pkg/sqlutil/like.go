package sqlutil

import (
	"strings"
	"unicode"
)

// MatchLike evaluates standard SQL LIKE matching where:
// '%' matches zero or more characters.
// '_' matches exactly one character.
// If caseInsensitive is true, matching is case-insensitive.
func MatchLike(pattern, str string, caseInsensitive bool) bool {
	if pattern == "%" {
		return true
	}
	if pattern == "" {
		return str == ""
	}
	if !strings.ContainsAny(pattern, "%_") {
		if caseInsensitive {
			return strings.EqualFold(str, pattern)
		}
		return str == pattern
	}

	pRunes := []rune(pattern)
	sRunes := []rune(str)

	pLen := len(pRunes)
	sLen := len(sRunes)

	pIdx := 0
	sIdx := 0
	starIdx := -1
	sMatch := 0

	for sIdx < sLen {
		if pIdx < pLen && (pRunes[pIdx] == '_' || matchRune(pRunes[pIdx], sRunes[sIdx], caseInsensitive)) {
			pIdx++
			sIdx++
		} else if pIdx < pLen && pRunes[pIdx] == '%' {
			starIdx = pIdx
			sMatch = sIdx
			pIdx++
		} else if starIdx != -1 {
			pIdx = starIdx + 1
			sMatch++
			sIdx = sMatch
		} else {
			return false
		}
	}

	for pIdx < pLen && pRunes[pIdx] == '%' {
		pIdx++
	}

	return pIdx == pLen
}

func matchRune(p, s rune, caseInsensitive bool) bool {
	if p == s {
		return true
	}
	if caseInsensitive {
		return unicode.ToLower(p) == unicode.ToLower(s) || unicode.ToUpper(p) == unicode.ToUpper(s)
	}
	return false
}
