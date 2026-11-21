//go:build !windows

package wiredtiger

// EnsureWiredTigerDLL is a no-op on non-Windows platforms.
func EnsureWiredTigerDLL() error {
	return nil
}
