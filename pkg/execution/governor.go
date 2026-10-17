package execution

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

// DefaultWorkMem defines the default memory budget per query execution operator (64MB).
const DefaultWorkMem int64 = 64 * 1024 * 1024

// ExecutorOptions controls memory budgets and scratch space for query operators.
type ExecutorOptions struct {
	WorkMem int64          `json:"work_mem"`
	TempDir string         `json:"temp_dir"`
	Arena   *storage.Arena `json:"-"`
}

// DefaultExecutorOptions returns production default execution options.
func DefaultExecutorOptions() ExecutorOptions {
	return ExecutorOptions{
		WorkMem: DefaultWorkMem,
		TempDir: os.TempDir(),
	}
}

// EstimateTupleSize estimates the heap memory footprint of a single Tuple in bytes.
func EstimateTupleSize(t Tuple) int64 {
	if t.IsNil() {
		return 0
	}
	// Base slice header (24 bytes) + Index pointer (8 bytes) = 32 bytes
	size := int64(32)
	for _, v := range t.Values {
		// Interface{} overhead (16 bytes)
		size += 16
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case int, int64, uint, uint64, float64:
			size += 8
		case int32, uint32, float32:
			size += 4
		case int16, uint16:
			size += 2
		case int8, uint8, bool:
			size += 1
		case string:
			size += int64(len(val)) + 16 // String payload + StringHeader
		case time.Time:
			size += 24
		case []byte:
			size += int64(len(val)) + 24 // Slice payload + SliceHeader
		default:
			size += 16
		}
	}
	return size
}

// ParseMemSize parses human-readable memory string (e.g. "64MB", "128kB", "1GB", "67108864") into bytes.
func ParseMemSize(s string) (int64, error) {
	trimmed := strings.TrimSpace(s)
	trimmed = strings.Trim(trimmed, "'\"")
	if trimmed == "" {
		return DefaultWorkMem, nil
	}

	upper := strings.ToUpper(trimmed)
	multiplier := int64(1)

	switch {
	case strings.HasSuffix(upper, "GB") || strings.HasSuffix(upper, "G"):
		multiplier = 1024 * 1024 * 1024
		upper = strings.TrimSuffix(strings.TrimSuffix(upper, "GB"), "G")
	case strings.HasSuffix(upper, "MB") || strings.HasSuffix(upper, "M"):
		multiplier = 1024 * 1024
		upper = strings.TrimSuffix(strings.TrimSuffix(upper, "MB"), "M")
	case strings.HasSuffix(upper, "KB") || strings.HasSuffix(upper, "K"):
		multiplier = 1024
		upper = strings.TrimSuffix(strings.TrimSuffix(upper, "KB"), "K")
	case strings.HasSuffix(upper, "B"):
		multiplier = 1
		upper = strings.TrimSuffix(upper, "B")
	}

	numStr := strings.TrimSpace(upper)
	val, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		if fVal, fErr := strconv.ParseFloat(numStr, 64); fErr == nil {
			return int64(fVal * float64(multiplier)), nil
		}
		return 0, fmt.Errorf("invalid memory size %q: %w", s, err)
	}

	return val * multiplier, nil
}

// FormatMemSize formats bytes into a clean, human-readable string (e.g. "64MB", "128kB").
func FormatMemSize(bytes int64) string {
	if bytes >= 1024*1024*1024 && bytes%(1024*1024*1024) == 0 {
		return fmt.Sprintf("%dGB", bytes/(1024*1024*1024))
	}
	if bytes >= 1024*1024 && bytes%(1024*1024) == 0 {
		return fmt.Sprintf("%dMB", bytes/(1024*1024))
	}
	if bytes >= 1024 && bytes%1024 == 0 {
		return fmt.Sprintf("%dkB", bytes/1024)
	}
	return fmt.Sprintf("%dB", bytes)
}

// DefaultMaxMemoryRatio is the default ratio (70%) of total physical RAM allocated to LionSQL.
const DefaultMaxMemoryRatio float64 = 0.70

// GetPhysicalMemory returns the host or container total physical RAM in bytes.
func GetPhysicalMemory() int64 {
	return getPlatformPhysicalMemory()
}

// GetDefaultMaxMemory returns 70% of detected physical RAM.
func GetDefaultMaxMemory() int64 {
	mem := GetPhysicalMemory()
	if mem <= 0 {
		return 4 * 1024 * 1024 * 1024
	}
	return int64(float64(mem) * DefaultMaxMemoryRatio)
}

// SetGlobalMemoryLimit configures the Go runtime soft heap memory limit (debug.SetMemoryLimit).
// It returns the previously configured memory limit.
func SetGlobalMemoryLimit(bytes int64) int64 {
	if bytes <= 0 {
		return debug.SetMemoryLimit(-1)
	}
	return debug.SetMemoryLimit(bytes)
}

// GetGlobalMemoryLimit returns the currently configured Go runtime soft heap memory limit.
func GetGlobalMemoryLimit() int64 {
	return debug.SetMemoryLimit(-1)
}
