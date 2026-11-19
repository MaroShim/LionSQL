package sqlutil

import "strings"

// IsCaseSensitiveCollation returns true if the collation specifies case-sensitive or binary comparison.
// SQL Server collations use _CS (Case-Sensitive), _BIN, or _BIN2 suffixes (e.g. Latin1_General_CS_AS, Latin1_General_BIN).
// PostgreSQL / POSIX collations use "C", "POSIX", or similar case-sensitive collations.
// Default SQL Server collations (_CI: Case-Insensitive) return false.
func IsCaseSensitiveCollation(collate string) bool {
	upper := strings.ToUpper(strings.Trim(collate, "'\"[]` \t\r\n"))
	if upper == "" {
		return false
	}
	if upper == "C" || upper == "POSIX" {
		return true
	}
	if strings.Contains(upper, "_CS_") || strings.HasSuffix(upper, "_CS") {
		return true
	}
	if strings.Contains(upper, "_BIN") {
		return true
	}
	return false
}
