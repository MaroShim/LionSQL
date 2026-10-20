package engine

import (
	"fmt"
	"strings"

	"lionsql/pkg/sqlutil"
)

// dmlKind identifies the statement family recognized by the DML parser.
type dmlKind int

const (
	dmlInsert dmlKind = iota + 1
	dmlUpdate
	dmlDelete
)

// DMLStatement is the structured result of parsing a single INSERT / UPSERT /
// UPDATE / DELETE statement. String fields hold the raw clause text (trimmed,
// quoting preserved); callers apply cleanIdentifier to Table themselves.
type DMLStatement struct {
	kind   dmlKind
	Upsert bool

	Table   string // raw target table token
	Columns string // INSERT column list without the enclosing parentheses
	Output  string // OUTPUT clause body
	Values  string // text following VALUES (INSERT ... VALUES)
	Select  string // query text (INSERT ... SELECT), including optional parentheses
	Set     string // SET clause body (UPDATE)
	Where   string // WHERE clause body (UPDATE / DELETE)
}

// dmlScanner is a cursor over a statement. Quoted regions ('...', "...",
// [...]) and parenthesized groups are skipped atomically so that keywords
// inside literals or sub-expressions are never mistaken for clause keywords.
type dmlScanner struct {
	s   string
	pos int
}

func isDMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// skipSpace advances over whitespace and reports how many bytes were skipped.
func (sc *dmlScanner) skipSpace() int {
	start := sc.pos
	for sc.pos < len(sc.s) && isDMLSpace(sc.s[sc.pos]) {
		sc.pos++
	}
	return sc.pos - start
}

// keyword consumes kw (case-insensitive) when it sits at the cursor and is
// followed by a word boundary (end of input, whitespace, '(' or ';').
func (sc *dmlScanner) keyword(kw string) bool {
	end := sc.pos + len(kw)
	if end > len(sc.s) || !strings.EqualFold(sc.s[sc.pos:end], kw) {
		return false
	}
	if end < len(sc.s) {
		c := sc.s[end]
		if !isDMLSpace(c) && c != '(' && c != ';' {
			return false
		}
	}
	sc.pos = end
	return true
}

// skipQuoted returns the index just past the quoted region opening at i, or
// len(s) when it is unterminated. Doubled closing quotes are escapes.
func skipQuoted(s string, i int) int {
	closer := s[i]
	if closer == '[' {
		closer = ']'
	}
	for j := i + 1; j < len(s); j++ {
		if s[j] != closer {
			continue
		}
		if j+1 < len(s) && s[j+1] == closer {
			j++
			continue
		}
		return j + 1
	}
	return len(s)
}

// ident reads an identifier token: stops at whitespace, '(' or ';' outside
// quotes/brackets.
func (sc *dmlScanner) ident() string {
	start := sc.pos
	for sc.pos < len(sc.s) {
		c := sc.s[sc.pos]
		if c == '\'' || c == '"' || c == '[' {
			sc.pos = skipQuoted(sc.s, sc.pos)
			continue
		}
		if isDMLSpace(c) || c == '(' || c == ';' {
			break
		}
		sc.pos++
	}
	return sc.s[start:sc.pos]
}

// group reads a balanced parenthesized group at the cursor and returns its
// inner text.
func (sc *dmlScanner) group() (string, bool) {
	if sc.pos >= len(sc.s) || sc.s[sc.pos] != '(' {
		return "", false
	}
	depth := 0
	for i := sc.pos; i < len(sc.s); i++ {
		switch sc.s[i] {
		case '\'', '"', '[':
			i = skipQuoted(sc.s, i) - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				inner := sc.s[sc.pos+1 : i]
				sc.pos = i + 1
				return inner, true
			}
		}
	}
	return "", false
}

func (sc *dmlScanner) rest() string { return strings.TrimSpace(sc.s[sc.pos:]) }

// scanDMLHead recognizes the statement prefix `<VERB> [INTO|FROM] <table>`
// and leaves the cursor right after the table token.
func scanDMLHead(sql string) (*DMLStatement, *dmlScanner, bool) {
	sc := &dmlScanner{s: sql}
	st := &DMLStatement{}
	var optional string
	switch {
	case sc.keyword("INSERT"):
		st.kind, optional = dmlInsert, "INTO"
	case sc.keyword("UPSERT"):
		st.kind, st.Upsert, optional = dmlInsert, true, "INTO"
	case sc.keyword("UPDATE"):
		st.kind = dmlUpdate
	case sc.keyword("DELETE"):
		st.kind, optional = dmlDelete, "FROM"
	default:
		return nil, nil, false
	}
	if sc.skipSpace() == 0 {
		return nil, nil, false
	}
	if optional != "" {
		save := sc.pos
		if sc.keyword(optional) && sc.skipSpace() > 0 {
			if st.Table = sc.ident(); st.Table != "" {
				return st, sc, true
			}
		}
		sc.pos = save
	}
	if st.Table = sc.ident(); st.Table == "" {
		return nil, nil, false
	}
	return st, sc, true
}

// dmlHead reports the kind and raw table token of a DML statement without
// parsing the remaining clauses (used for routing and RBAC checks).
func dmlHead(sql string) (dmlKind, string, bool) {
	st, _, ok := scanDMLHead(sql)
	if !ok {
		return 0, "", false
	}
	return st.kind, st.Table, true
}

// ParseDMLCommand parses an INSERT / UPSERT / UPDATE / DELETE statement in a
// single pass over the text.
func ParseDMLCommand(sql string) (*DMLStatement, error) {
	sql = strings.TrimRight(strings.TrimSpace(sql), ";")
	st, sc, ok := scanDMLHead(sql)
	if !ok {
		return nil, fmt.Errorf("not a DML statement")
	}
	switch st.kind {
	case dmlInsert:
		return st, parseInsertBody(st, sc)
	case dmlUpdate:
		return st, parseUpdateBody(st, sc)
	default:
		return st, parseDeleteBody(st, sc)
	}
}

// splitOutputWhere splits `[OUTPUT <out>] [WHERE <cond>]` (as it appears after
// the target / SET clause) at top-level keywords. head is the text before the
// first clause keyword.
func splitOutputWhere(body string) (head, output, where string) {
	whereIdx := sqlutil.FindTopLevelKeyword(body, "WHERE")
	outIdx := sqlutil.FindTopLevelKeyword(body, "OUTPUT")
	if outIdx != -1 && whereIdx != -1 && outIdx > whereIdx {
		outIdx = -1 // OUTPUT only counts before WHERE
	}
	end := len(body)
	if whereIdx != -1 {
		where = strings.TrimSpace(body[whereIdx+len("WHERE"):])
		end = whereIdx
	}
	if outIdx != -1 {
		output = strings.TrimSpace(body[outIdx+len("OUTPUT") : end])
		end = outIdx
	}
	return strings.TrimSpace(body[:end]), output, where
}

func parseInsertBody(st *DMLStatement, sc *dmlScanner) error {
	sc.skipSpace()
	if sc.pos < len(sc.s) && sc.s[sc.pos] == '(' {
		save := sc.pos
		inner, ok := sc.group()
		trimmed := strings.TrimSpace(inner)
		if ok && !(len(trimmed) >= 6 && strings.EqualFold(trimmed[:6], "SELECT")) {
			st.Columns = inner
		} else {
			sc.pos = save // "(SELECT ...)" is the source query, not a column list
		}
	}
	rest := sc.rest()

	if len(rest) >= 6 && strings.EqualFold(rest[:6], "OUTPUT") && (len(rest) == 6 || isDMLSpace(rest[6])) {
		body := rest[6:]
		cut := len(body)
		for _, kw := range []string{"VALUES", "SELECT"} {
			if i := sqlutil.FindTopLevelKeyword(body, kw); i != -1 && i < cut {
				cut = i
			}
		}
		if cut == len(body) {
			if i := strings.Index(body, "(SELECT"); i != -1 {
				cut = i
			}
		}
		st.Output = strings.TrimSpace(body[:cut])
		rest = strings.TrimSpace(body[cut:])
	}

	rs := &dmlScanner{s: rest}
	if rs.keyword("VALUES") {
		rs.skipSpace()
		st.Values = rs.s[rs.pos:]
		return nil
	}
	probe := rest
	if strings.HasPrefix(probe, "(") {
		probe = strings.TrimSpace(probe[1:])
	}
	if len(probe) > 6 && strings.EqualFold(probe[:6], "SELECT") && (isDMLSpace(probe[6]) || probe[6] == '(' || probe[6] == '*') {
		st.Select = rest
		return nil
	}
	return fmt.Errorf("invalid INSERT INTO syntax")
}

func parseUpdateBody(st *DMLStatement, sc *dmlScanner) error {
	if sc.skipSpace() == 0 || !sc.keyword("SET") {
		return fmt.Errorf("invalid UPDATE syntax")
	}
	st.Set, st.Output, st.Where = splitOutputWhere(sc.s[sc.pos:])
	return nil
}

func parseDeleteBody(st *DMLStatement, sc *dmlScanner) error {
	head, out, where := splitOutputWhere(sc.s[sc.pos:])
	if head != "" {
		return fmt.Errorf("invalid DELETE syntax")
	}
	st.Output, st.Where = out, where
	return nil
}
