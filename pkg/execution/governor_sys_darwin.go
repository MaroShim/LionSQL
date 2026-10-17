//go:build darwin

package execution

import (
	"encoding/binary"
	"syscall"
)

// getPlatformPhysicalMemory detects physical RAM on macOS via sysctl hw.memsize.
func getPlatformPhysicalMemory() int64 {
	val, err := syscall.Sysctl("hw.memsize")
	if err == nil && len(val) >= 8 {
		mem := binary.LittleEndian.Uint64([]byte(val[:8]))
		if mem > 0 {
			return int64(mem)
		}
	}
	return 4 * 1024 * 1024 * 1024 // 4GB fallback
}
