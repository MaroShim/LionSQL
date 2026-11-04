package proc

import (
	"strings"
)

// QueryCharacteristics holds structural AST traits extracted from a query
// without relying on fragile raw string substring heuristics.
type QueryCharacteristics struct {
	StatementType  string   // "SELECT", "INSERT", "UPDATE", "DELETE", "DDL", "TX", "OTHER"
	TargetTables   []string // Unique referenced table names (e.g. "users", "sys.tables")
	HasReturning   bool     // Contains SQL RETURNING clause
	HasOutput      bool     // Contains T-SQL OUTPUT clause
	HasOnConflict  bool     // Contains ON CONFLICT clause
	HasJoin        bool     // References multiple tables via JOIN
	HasSubquery    bool     // References subqueries (e.g. nested SELECT)
	HasDerivedTable bool    // References derived table subquery: FROM (SELECT ...)
}

// ExtractQueryCharacteristics tokenizes the SQL query and extracts structural traits,
// ignoring string literals, SQL comments, and column aliases.
func ExtractQueryCharacteristics(sql string) QueryCharacteristics {
	l := NewLexer(sql)
	var qc QueryCharacteristics
	tableSet := make(map[string]bool)

	var prevTok Token
	var firstSignificant = true

	for {
		tok := l.NextToken()
		if tok.Type == TokenEOF {
			break
		}

		// 1. First statement keyword determination
		if firstSignificant && tok.Type != TokenSemicolon {
			firstSignificant = false
			upper := strings.ToUpper(tok.Literal)
			switch tok.Type {
			case TokenSelect:
				qc.StatementType = "SELECT"
			case TokenInsert:
				qc.StatementType = "INSERT"
			case TokenUpdate:
				qc.StatementType = "UPDATE"
			case TokenDelete:
				qc.StatementType = "DELETE"
			case TokenCreate, TokenAlter, TokenDrop:
				qc.StatementType = "DDL"
			case TokenBegin, TokenCommit, TokenRollback:
				qc.StatementType = "TX"
			default:
				if upper == "WITH" {
					qc.StatementType = "SELECT"
					qc.HasSubquery = true
				} else if upper == "EXEC" || upper == "CALL" {
					qc.StatementType = "EXEC"
				} else if upper == "TRUNCATE" {
					qc.StatementType = "DDL"
				} else {
					qc.StatementType = upper
				}
			}
		} else if !firstSignificant && tok.Type == TokenSelect {
			qc.HasSubquery = true
		}

		// 2. Syntax clauses
		if tok.Type == TokenReturning {
			qc.HasReturning = true
		}
		if tok.Type == TokenOutput {
			qc.HasOutput = true
		}
		if tok.Type == TokenConflict && prevTok.Type == TokenOn {
			qc.HasOnConflict = true
		}
		if tok.Type == TokenJoin {
			qc.HasJoin = true
		}

		// 3. Table references extraction: FROM, JOIN, INTO, UPDATE
		isStandaloneUpdate := tok.Type == TokenUpdate && strings.ToUpper(prevTok.Literal) != "DO" && !qc.HasOnConflict
		if tok.Type == TokenFrom || tok.Type == TokenJoin || tok.Type == TokenInto || isStandaloneUpdate {
			hasOpenParen, tbl := readTableIdentifier(l)
			if tok.Type == TokenFrom && hasOpenParen {
				qc.HasDerivedTable = true
			}
			if tbl != "" {
				norm := cleanTableRef(tbl)
				if norm != "" && !tableSet[norm] {
					tableSet[norm] = true
					qc.TargetTables = append(qc.TargetTables, norm)
				}
			}
		}

		prevTok = tok
	}

	return qc
}

func readTableIdentifier(l *Lexer) (bool, string) {
	hadParen := false
	// Skip opening parens in parenthesized FROM clauses: FROM ("tbl" JOIN ...)
	for {
		if l.ch == ' ' || l.ch == '\t' || l.ch == '\r' || l.ch == '\n' {
			l.readChar()
		} else if l.ch == '(' {
			hadParen = true
			l.readChar()
		} else {
			break
		}
	}

	tok := l.NextToken()
	if tok.Type == TokenEOF || tok.Type == TokenSelect || tok.Type == TokenWith {
		// Subquery or empty
		return hadParen, ""
	}

	if tok.Type == TokenDoubleColon {
		nextTok := l.NextToken()
		return hadParen, "::" + nextTok.Literal
	}

	if tok.Type != TokenIdent && tok.Type != TokenString && tok.Type < 100 {
		// Only consider identifiers, string-escaped idents, or unreserved keywords
		if tok.Literal == "" {
			return hadParen, ""
		}
	}

	var parts []string
	parts = append(parts, tok.Literal)

	for {
		// Check if next token is a dot '.'
		peekCh := l.ch
		for peekCh == ' ' || peekCh == '\t' || peekCh == '\r' || peekCh == '\n' {
			l.readChar()
			peekCh = l.ch
		}
		if peekCh == '.' {
			l.readChar() // consume '.'
			nextTok := l.NextToken()
			if nextTok.Type != TokenEOF && (nextTok.Type == TokenIdent || nextTok.Type == TokenString || nextTok.Type < 100) {
				parts = append(parts, nextTok.Literal)
			} else {
				break
			}
		} else {
			break
		}
	}

	return hadParen, strings.Join(parts, ".")
}

func cleanTableRef(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "[]\"'`()")
	// Split schema.table
	if strings.Contains(s, ".") {
		parts := strings.Split(s, ".")
		for i := range parts {
			parts[i] = strings.Trim(strings.TrimSpace(parts[i]), "[]\"'`()")
		}
		// If 3 parts (db.schema.table), keep schema.table
		if len(parts) == 3 {
			parts = parts[1:]
		}
		return strings.ToLower(strings.Join(parts, "."))
	}
	return strings.ToLower(s)
}
