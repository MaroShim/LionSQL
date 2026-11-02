package wire

import (
	"fmt"
	"strings"
)

// CommandTagFormatter formats the PostgreSQL command completion tag given the parsed statement words,
// rows affected by DML, and rows returned by query.
type CommandTagFormatter func(fields []string, rowsAffected int64, rowsCount int) string

// CommandTagRule defines a table entry for mapping SQL verb prefixes to PostgreSQL command completion tags.
type CommandTagRule struct {
	Verb      string
	Formatter CommandTagFormatter
}

// defaultCommandTagRules is the immutable lookup table for standard PostgreSQL command tags.
var defaultCommandTagRules = map[string]CommandTagFormatter{
	"INSERT": func(fields []string, rowsAffected int64, rowsCount int) string {
		return fmt.Sprintf("INSERT 0 %d", rowsAffected)
	},
	"DELETE": func(fields []string, rowsAffected int64, rowsCount int) string {
		return fmt.Sprintf("DELETE %d", rowsAffected)
	},
	"UPDATE": func(fields []string, rowsAffected int64, rowsCount int) string {
		return fmt.Sprintf("UPDATE %d", rowsAffected)
	},
	"SELECT": func(fields []string, rowsAffected int64, rowsCount int) string {
		cnt := rowsCount
		if cnt == 0 && rowsAffected > 0 {
			cnt = int(rowsAffected)
		}
		return fmt.Sprintf("SELECT %d", cnt)
	},
	"MOVE": func(fields []string, rowsAffected int64, rowsCount int) string {
		return fmt.Sprintf("MOVE %d", rowsAffected)
	},
	"FETCH": func(fields []string, rowsAffected int64, rowsCount int) string {
		return fmt.Sprintf("FETCH %d", rowsAffected)
	},
	"COPY": func(fields []string, rowsAffected int64, rowsCount int) string {
		return fmt.Sprintf("COPY %d", rowsAffected)
	},
	"BEGIN": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "BEGIN"
	},
	"START": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "BEGIN"
	},
	"COMMIT": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "COMMIT"
	},
	"ROLLBACK": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "ROLLBACK"
	},
	"SET": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "SET"
	},
	"SHOW": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "SHOW"
	},
	"CREATE": func(fields []string, rowsAffected int64, rowsCount int) string {
		if len(fields) > 1 {
			return fmt.Sprintf("CREATE %s", fields[1])
		}
		return "CREATE"
	},
	"DROP": func(fields []string, rowsAffected int64, rowsCount int) string {
		if len(fields) > 1 {
			return fmt.Sprintf("DROP %s", fields[1])
		}
		return "DROP"
	},
	"ALTER": func(fields []string, rowsAffected int64, rowsCount int) string {
		if len(fields) > 1 {
			return fmt.Sprintf("ALTER %s", fields[1])
		}
		return "ALTER"
	},
	"DECLARE": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "DECLARE CURSOR"
	},
	"CLOSE": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "CLOSE CURSOR"
	},
	"LOCK": func(fields []string, rowsAffected int64, rowsCount int) string {
		return "LOCK TABLE"
	},
}

// FormatPgCommandComplete resolves the completion tag for a query or command verb using the command tag table.
// Exported for testing and external callers.
func FormatPgCommandComplete(command string, rowsAffected int64, rowsCount int) string {
	return formatPgCommandComplete(command, rowsAffected, rowsCount)
}

// formatPgCommandComplete resolves the completion tag for a query or command verb using the command tag table.
func formatPgCommandComplete(command string, rowsAffected int64, rowsCount int) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "OK"
	}
	// Fast-path: check first word without strings.Fields allocations
	idx := strings.IndexAny(trimmed, " \t\r\n(;")
	firstWord := trimmed
	if idx != -1 {
		firstWord = trimmed[:idx]
	}
	upperWord := strings.ToUpper(firstWord)
	switch upperWord {
	case "EXEC", "EXECUTE", "CALL":
		return "EXEC"
	case "COMMIT":
		return "COMMIT"
	case "ROLLBACK":
		return "ROLLBACK"
	case "BEGIN":
		return "BEGIN"
	case "SELECT":
		cnt := rowsCount
		if cnt == 0 && rowsAffected > 0 {
			cnt = int(rowsAffected)
		}
		if cnt == 0 {
			return "SELECT 0"
		}
		if cnt == 1 {
			return "SELECT 1"
		}
		return fmt.Sprintf("SELECT %d", cnt)
	case "UPDATE":
		if rowsAffected == 1 {
			return "UPDATE 1"
		}
		return fmt.Sprintf("UPDATE %d", rowsAffected)
	case "INSERT":
		if rowsAffected == 1 {
			return "INSERT 0 1"
		}
		return fmt.Sprintf("INSERT 0 %d", rowsAffected)
	case "DELETE":
		if rowsAffected == 1 {
			return "DELETE 1"
		}
		return fmt.Sprintf("DELETE %d", rowsAffected)
	}

	upper := strings.ToUpper(trimmed)
	fields := strings.Fields(upper)
	if len(fields) == 0 {
		return "OK"
	}
	if formatter, ok := defaultCommandTagRules[fields[0]]; ok {
		return formatter(fields, rowsAffected, rowsCount)
	}
	return fields[0]
}
