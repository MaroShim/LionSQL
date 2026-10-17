//go:build linux

package execution

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// getPlatformPhysicalMemory detects physical RAM or container cgroup memory limits on Linux.
func getPlatformPhysicalMemory() int64 {
	// 1. Check cgroups v2 memory.max
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "max" && s != "" {
			if limit, err := strconv.ParseInt(s, 10, 64); err == nil && limit > 0 {
				return limit
			}
		}
	}

	// 2. Check cgroups v1 memory.limit_in_bytes
	if data, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		s := strings.TrimSpace(string(data))
		if limit, err := strconv.ParseInt(s, 10, 64); err == nil && limit > 0 && limit < 1<<62 {
			return limit
		}
	}

	// 3. Check /proc/meminfo MemTotal
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil && kb > 0 {
						return kb * 1024
					}
				}
				break
			}
		}
	}

	return 4 * 1024 * 1024 * 1024 // 4GB fallback
}
