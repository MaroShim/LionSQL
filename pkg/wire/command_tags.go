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
	upper := strings.ToUpper(strings.TrimSpace(command))
	fields := strings.Fields(upper)
	if len(fields) == 0 {
		return "OK"
	}
	firstWord := fields[0]
	if formatter, ok := defaultCommandTagRules[firstWord]; ok {
		return formatter(fields, rowsAffected, rowsCount)
	}
	return firstWord
}
