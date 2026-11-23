//go:build !windows

package wiredtiger

// EnsureWiredTigerDLL is a no-op on non-Windows platforms.
func EnsureWiredTigerDLL() error {
	return nil
}

// getResolvedWiredTigerDLL returns empty string on non-Windows platforms.
func getResolvedWiredTigerDLL() string {
	return ""
}
