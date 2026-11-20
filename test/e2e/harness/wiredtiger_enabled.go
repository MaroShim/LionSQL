//go:build wiredtiger

package harness

// IsWiredTigerAvailable returns true when compiled with -tags wiredtiger.
func IsWiredTigerAvailable() bool {
	return true
}
