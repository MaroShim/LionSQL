//go:build !darwin && !linux

package execution

// getPlatformPhysicalMemory returns a safe 4GB fallback for unsupported platforms.
func getPlatformPhysicalMemory() int64 {
	return 4 * 1024 * 1024 * 1024 // 4GB fallback
}
