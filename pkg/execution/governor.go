package execution

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

// DefaultWorkMem defines the default memory budget per query execution operator (64MB).
const DefaultWorkMem int64 = 64 * 1024 * 1024

// DefaultGCPercent defines the default Go runtime GC percentage (200).
// Tuning GOGC to 200 relaxes frequent, CPU-wasting GC cycles during in-memory OLTP workloads,
// while SetGlobalMemoryLimit (GOMEMLIMIT) bounds overall heap memory to prevent OOM.
const DefaultGCPercent = 200

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
	if bytes >= 1024*1024 {
		return fmt.Sprintf("%dMB", bytes/(1024*1024))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%dkB", bytes/1024)
	}
	return fmt.Sprintf("%dB", bytes)
}

// FormatMemMB formats bytes into a clean MB representation (e.g. "11468MB", "4096MB").
func FormatMemMB(bytes int64) string {
	mb := bytes / (1024 * 1024)
	if mb <= 0 && bytes > 0 {
		mb = 1
	}
	return fmt.Sprintf("%dMB", mb)
}

// DefaultMaxMemoryRatio is the default upper-bound ratio (65%) of total physical RAM allocated to LionSQL.
const DefaultMaxMemoryRatio float64 = 0.65

// MinOSReservedMemory is the memory buffer reserved for host OS / container overhead (1GB).
const MinOSReservedMemory int64 = 1024 * 1024 * 1024

// MinMemoryRatioFloor is the minimum floor ratio (50%) to guarantee workable database headroom on low-memory systems.
const MinMemoryRatioFloor float64 = 0.50

// GetPhysicalMemory returns the host or container total physical RAM in bytes.
func GetPhysicalMemory() int64 {
	return getPlatformPhysicalMemory()
}

// CalculateDefaultMaxMemory computes the dynamic global memory budget given total physical RAM in bytes:
// max(Total * 0.50, min(Total * 0.65, Total - 1GB))
func CalculateDefaultMaxMemory(totalMem int64) int64 {
	if totalMem <= 0 {
		return 4 * 1024 * 1024 * 1024
	}
	upper := int64(float64(totalMem) * DefaultMaxMemoryRatio)
	minusReserve := totalMem - MinOSReservedMemory
	budget := upper
	if minusReserve < budget {
		budget = minusReserve
	}
	floor := int64(float64(totalMem) * MinMemoryRatioFloor)
	if budget < floor {
		budget = floor
	}
	return budget
}

// GetDefaultMaxMemory returns dynamic global memory budget calculated from detected physical RAM:
// max(Total * 0.50, min(Total * 0.65, Total - 1GB))
func GetDefaultMaxMemory() int64 {
	return CalculateDefaultMaxMemory(GetPhysicalMemory())
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

// SetGlobalGCPercent configures the Go runtime GC target percentage (debug.SetGCPercent).
// If percent <= 0, it falls back to DefaultGCPercent (200).
// If the environment variable GOGC is explicitly set, it respects the user's environment variable.
func SetGlobalGCPercent(percent int) int {
	if percent <= 0 {
		percent = DefaultGCPercent
	}
	if env := strings.TrimSpace(os.Getenv("GOGC")); env != "" {
		if p, err := strconv.Atoi(env); err == nil && p > 0 {
			percent = p
		}
	}
	return debug.SetGCPercent(percent)
}

// InitRuntimeTuning configures the Go runtime soft memory limit and GC target percentage.
// By default, GOGC is set to DefaultGCPercent (200) to minimize unnecessary GC cycles during OLTP spikes,
// while relying on SetGlobalMemoryLimit (GOMEMLIMIT) to bound total heap consumption.
func InitRuntimeTuning(maxMemoryBytes int64, gcPercent int) {
	SetGlobalGCPercent(gcPercent)
	if maxMemoryBytes <= 0 {
		maxMemoryBytes = GetDefaultMaxMemory()
	}
	SetGlobalMemoryLimit(maxMemoryBytes)
}
