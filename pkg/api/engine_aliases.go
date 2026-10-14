package api

import (
	"time"

	"sqlion/pkg/engine"
)

// Compatibility aliases for components that now live in pkg/engine.
type (
	MetricsCollector = engine.MetricsCollector
	SlowQueryLogger  = engine.SlowQueryLogger
	SlowQueryEntry   = engine.SlowQueryEntry
	GeneralLogger    = engine.GeneralLogger
	GeneralLogEntry  = engine.GeneralLogEntry
	SessionPool      = engine.SessionPool
	PoolConfig       = engine.PoolConfig
	PoolStats        = engine.PoolStats

	QueryResult      = engine.QueryResult
	RotatingLogWriter = engine.RotatingLogWriter
)

const (
	DefaultMaxLogFileSize     = engine.DefaultMaxLogFileSize
	MaxRotatedBackups         = engine.MaxRotatedBackups
	DefaultSlowQueryThreshold = engine.DefaultSlowQueryThreshold
	MaxSlowQueryCapacity      = engine.MaxSlowQueryCapacity
	MaxGeneralLogCapacity     = engine.MaxGeneralLogCapacity
)

var (
	ErrPoolClosed    = engine.ErrPoolClosed
	ErrPoolTimeout   = engine.ErrPoolTimeout
	ErrPoolExhausted = engine.ErrPoolExhausted

	NewMetricsCollector  = engine.NewMetricsCollector
	NewRotatingLogWriter = engine.NewRotatingLogWriter
	NewSlowQueryLogger   = engine.NewSlowQueryLogger
	NewGeneralLogger     = engine.NewGeneralLogger
	NewSessionPool       = engine.NewSessionPool
	DefaultPoolConfig    = engine.DefaultPoolConfig
	ParseTimeoutDuration = engine.ParseTimeoutDuration
)

func parseTimeoutDuration(s string) (time.Duration, error) {
	return engine.ParseTimeoutDuration(s)
}
