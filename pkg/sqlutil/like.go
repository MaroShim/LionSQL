package sqlutil

import (
	"strings"
	"unicode"
)

// MatchLike evaluates standard SQL LIKE matching where:
// '%' matches zero or more characters.
// '_' matches exactly one character.
// '[...]' matches any character in the bracket set/range (or literal if escaped like '[_]' or '[%]').
// '[^...]' matches any character not in the bracket set/range.
// If caseInsensitive is true, matching is case-insensitive.
func MatchLike(pattern, str string, caseInsensitive bool) bool {
	if pattern == "%" {
		return true
	}
	if pattern == "" {
		return str == ""
	}
	if !strings.ContainsAny(pattern, "%_[") {
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
		if pIdx < pLen {
			if pRunes[pIdx] == '[' {
				matched, nextPIdx, isBracket := matchBracket(pRunes, pIdx, pLen, sRunes[sIdx], caseInsensitive)
				if isBracket {
					if matched {
						pIdx = nextPIdx
						sIdx++
						continue
					}
					goto backtrack
				}
			}
			if pRunes[pIdx] == '_' || matchRune(pRunes[pIdx], sRunes[sIdx], caseInsensitive) {
				pIdx++
				sIdx++
				continue
			} else if pRunes[pIdx] == '%' {
				starIdx = pIdx
				sMatch = sIdx
				pIdx++
				continue
			}
		}

backtrack:
		if starIdx != -1 {
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

func matchBracket(pRunes []rune, pIdx, pLen int, ch rune, caseInsensitive bool) (bool, int, bool) {
	if pIdx >= pLen || pRunes[pIdx] != '[' {
		return false, pIdx, false
	}
	endIdx := -1
	for i := pIdx + 1; i < pLen; i++ {
		if pRunes[i] == ']' {
			endIdx = i
			break
		}
	}
	if endIdx == -1 {
		return false, pIdx, false
	}

	start := pIdx + 1
	negate := false
	if start < endIdx && pRunes[start] == '^' {
		negate = true
		start++
	}

	matched := false
	for i := start; i < endIdx; i++ {
		if i+2 < endIdx && pRunes[i+1] == '-' {
			rStart := pRunes[i]
			rEnd := pRunes[i+2]
			c := ch
			if caseInsensitive {
				rStart = unicode.ToLower(rStart)
				rEnd = unicode.ToLower(rEnd)
				c = unicode.ToLower(c)
			}
			if rStart > rEnd {
				rStart, rEnd = rEnd, rStart
			}
			if c >= rStart && c <= rEnd {
				matched = true
				break
			}
			i += 2
		} else {
			if matchRune(pRunes[i], ch, caseInsensitive) {
				matched = true
				break
			}
		}
	}

	if negate {
		matched = !matched
	}
	return matched, endIdx + 1, true
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
