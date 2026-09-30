package api

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsCollector gathers high-frequency server and engine metrics with zero lock contention.
type MetricsCollector struct {
	queriesTotal         atomic.Int64
	queryErrorsTotal     atomic.Int64
	queryDurationNanos   atomic.Int64
	slowQueriesTotal     atomic.Int64
	transactionsCommit   atomic.Int64
	transactionsRollback atomic.Int64
	commandCounts        sync.Map // string (e.g. "SELECT") -> *atomic.Int64
}

// NewMetricsCollector initializes a new atomic MetricsCollector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// RecordQuery updates query counters and cumulative execution time.
func (m *MetricsCollector) RecordQuery(command string, duration time.Duration, isErr bool, isSlow bool) {
	m.queriesTotal.Add(1)
	m.queryDurationNanos.Add(duration.Nanoseconds())
	if isErr {
		m.queryErrorsTotal.Add(1)
	}
	if isSlow {
		m.slowQueriesTotal.Add(1)
	}

	cmdUpper := strings.ToUpper(strings.TrimSpace(command))
	if cmdUpper == "" {
		cmdUpper = "UNKNOWN"
	}
	actual, _ := m.commandCounts.LoadOrStore(cmdUpper, new(atomic.Int64))
	actual.(*atomic.Int64).Add(1)
}

// RecordTransaction increments commit or rollback counters.
func (m *MetricsCollector) RecordTransaction(action string) {
	if strings.EqualFold(action, "commit") {
		m.transactionsCommit.Add(1)
	} else if strings.EqualFold(action, "rollback") {
		m.transactionsRollback.Add(1)
	}
}

// FormatPrometheus serializes current server state into the Prometheus standard text-based format (0.0.4).
func (m *MetricsCollector) FormatPrometheus(s *Server) string {
	var b strings.Builder

	// 1. Uptime
	uptime := time.Since(s.startTime).Seconds()
	b.WriteString("# HELP sqlion_uptime_seconds Total time in seconds since SQLion server process started.\n")
	b.WriteString("# TYPE sqlion_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_uptime_seconds %.2f\n\n", uptime))

	// 2. Active connections
	s.sessMu.RLock()
	activeConns := len(s.sessions)
	s.sessMu.RUnlock()
	b.WriteString("# HELP sqlion_active_connections Current number of active client sessions.\n")
	b.WriteString("# TYPE sqlion_active_connections gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_active_connections %d\n\n", activeConns))

	// 3. Total queries
	b.WriteString("# HELP sqlion_queries_total Total number of SQL statements processed.\n")
	b.WriteString("# TYPE sqlion_queries_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_queries_total %d\n", m.queriesTotal.Load()))

	// Command breakdown
	type cmdStat struct {
		cmd   string
		count int64
	}
	var cmds []cmdStat
	m.commandCounts.Range(func(key, val any) bool {
		cmds = append(cmds, cmdStat{
			cmd:   key.(string),
			count: val.(*atomic.Int64).Load(),
		})
		return true
	})
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].cmd < cmds[j].cmd })
	for _, c := range cmds {
		b.WriteString(fmt.Sprintf("sqlion_queries_total{command=\"%s\"} %d\n", c.cmd, c.count))
	}
	b.WriteString("\n")

	// 4. Query errors
	b.WriteString("# HELP sqlion_query_errors_total Total number of SQL queries that resulted in an error.\n")
	b.WriteString("# TYPE sqlion_query_errors_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_query_errors_total %d\n\n", m.queryErrorsTotal.Load()))

	// 5. Total query duration
	durSec := float64(m.queryDurationNanos.Load()) / 1e9
	b.WriteString("# HELP sqlion_query_duration_seconds_total Total cumulative duration of executed queries in seconds.\n")
	b.WriteString("# TYPE sqlion_query_duration_seconds_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_query_duration_seconds_total %.6f\n\n", durSec))

	// 6. Slow queries
	b.WriteString("# HELP sqlion_slow_queries_total Total number of queries exceeding the slow query threshold.\n")
	b.WriteString("# TYPE sqlion_slow_queries_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_slow_queries_total %d\n\n", m.slowQueriesTotal.Load()))

	// 7. Transactions
	b.WriteString("# HELP sqlion_transactions_total Total number of completed transactions.\n")
	b.WriteString("# TYPE sqlion_transactions_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_transactions_total{type=\"commit\"} %d\n", m.transactionsCommit.Load()))
	b.WriteString(fmt.Sprintf("sqlion_transactions_total{type=\"rollback\"} %d\n\n", m.transactionsRollback.Load()))

	// 8. Total tables
	tblCount := 0
	if s.engine != nil {
		if tbls, err := s.engine.ListTables(); err == nil {
			tblCount = len(tbls)
		}
	}
	b.WriteString("# HELP sqlion_tables_total Total number of user tables in the database engine.\n")
	b.WriteString("# TYPE sqlion_tables_total gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_tables_total %d\n\n", tblCount))

	// 9. Plan Cache Hit/Miss
	var hits, misses int64
	if s.planCache != nil {
		hits, misses = s.planCache.Stats()
	}
	b.WriteString("# HELP sqlion_plan_cache_hits_total Total number of stored procedure execution plan cache hits.\n")
	b.WriteString("# TYPE sqlion_plan_cache_hits_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_plan_cache_hits_total %d\n", hits))
	b.WriteString("# HELP sqlion_plan_cache_misses_total Total number of stored procedure execution plan cache misses.\n")
	b.WriteString("# TYPE sqlion_plan_cache_misses_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_plan_cache_misses_total %d\n\n", misses))

	// 10. Memory and Goroutine statistics
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b.WriteString("# HELP sqlion_memory_alloc_bytes Bytes of allocated heap objects currently in use.\n")
	b.WriteString("# TYPE sqlion_memory_alloc_bytes gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_memory_alloc_bytes %d\n", mem.Alloc))
	b.WriteString("# HELP sqlion_memory_sys_bytes Total bytes of memory obtained from the OS.\n")
	b.WriteString("# TYPE sqlion_memory_sys_bytes gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_memory_sys_bytes %d\n", mem.Sys))
	b.WriteString("# HELP sqlion_memory_heap_alloc_bytes Bytes of allocated heap memory.\n")
	b.WriteString("# TYPE sqlion_memory_heap_alloc_bytes gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_memory_heap_alloc_bytes %d\n", mem.HeapAlloc))
	b.WriteString("# HELP sqlion_goroutines Number of currently running Go routines.\n")
	b.WriteString("# TYPE sqlion_goroutines gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_goroutines %d\n", runtime.NumGoroutine()))

	return b.String()
}
