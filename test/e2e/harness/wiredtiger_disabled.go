//go:build !wiredtiger

package harness

// IsWiredTigerAvailable returns false when compiled without wiredtiger tag.
func IsWiredTigerAvailable() bool {
	return false
}
