package engine

import (
	"encoding/json"
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
	transactionsConflict atomic.Int64
	pkBypassHits         atomic.Int64
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

// RecordTransaction increments commit, rollback, or conflict counters.
func (m *MetricsCollector) RecordTransaction(action string) {
	if strings.EqualFold(action, "commit") {
		m.transactionsCommit.Add(1)
	} else if strings.EqualFold(action, "rollback") {
		m.transactionsRollback.Add(1)
	} else if strings.EqualFold(action, "conflict") {
		m.transactionsConflict.Add(1)
	}
}

// RecordConflict increments the transaction conflict counter.
func (m *MetricsCollector) RecordConflict() {
	m.transactionsConflict.Add(1)
}

// RecordPKBypassHit increments the fast-path PK point lookup bypass counter.
func (m *MetricsCollector) RecordPKBypassHit() {
	m.pkBypassHits.Add(1)
}

// PKBypassHits returns total number of queries served via fast-path PK bypass.
func (m *MetricsCollector) PKBypassHits() int64 {
	return m.pkBypassHits.Load()
}

// PrometheusServerState carries server-level gauges that are not owned by MetricsCollector.
type PrometheusServerState struct {
	StartTime             time.Time
	ActiveConnections     int
	TableCount            int
	PlanCacheHits         int64
	PlanCacheMisses       int64
	StorageEngine         string // "memengine", "wiredtiger"
	MemoryLimitBytes      int64  // max memory budget
	ReplicationRole       string // "primary", "replica", "standalone"
	ReplicationLagBytes   int64
	ReplicationCurrentLSN int64
}

// FormatPrometheus serializes current server state into the Prometheus standard text-based format (0.0.4).
func (m *MetricsCollector) FormatPrometheus(st PrometheusServerState) string {
	var b strings.Builder

	// 1. Uptime
	uptime := time.Since(st.StartTime).Seconds()
	b.WriteString("# HELP sqlion_uptime_seconds Total time in seconds since SQLion server process started.\n")
	b.WriteString("# TYPE sqlion_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_uptime_seconds %.2f\n\n", uptime))

	// 2. Active connections
	activeConns := st.ActiveConnections
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

	// 4.1 PK point lookup bypass hits
	b.WriteString("# HELP sqlion_pk_bypass_hits_total Total number of simple PK lookups handled via zero-overhead storage bypass.\n")
	b.WriteString("# TYPE sqlion_pk_bypass_hits_total counter\n")
	b.WriteString(fmt.Sprintf("sqlion_pk_bypass_hits_total %d\n\n", m.pkBypassHits.Load()))

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
	b.WriteString(fmt.Sprintf("sqlion_transactions_total{type=\"rollback\"} %d\n", m.transactionsRollback.Load()))
	b.WriteString(fmt.Sprintf("sqlion_transactions_total{type=\"conflict\"} %d\n\n", m.transactionsConflict.Load()))

	// 7.1 Storage Engine Info
	storageEngine := st.StorageEngine
	if storageEngine == "" {
		storageEngine = "memengine"
	}
	b.WriteString("# HELP sqlion_storage_info Database storage engine metadata.\n")
	b.WriteString("# TYPE sqlion_storage_info gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_storage_info{engine=\"%s\"} 1\n\n", storageEngine))

	// 7.2 Replication Status
	repRole := strings.ToLower(st.ReplicationRole)
	if repRole == "" {
		repRole = "standalone"
	}
	b.WriteString("# HELP sqlion_replication_role HA availability group role.\n")
	b.WriteString("# TYPE sqlion_replication_role gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_replication_role{role=\"%s\"} 1\n", repRole))
	b.WriteString("# HELP sqlion_replication_lag_bytes Replica replication lag in bytes behind primary.\n")
	b.WriteString("# TYPE sqlion_replication_lag_bytes gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_replication_lag_bytes %d\n", st.ReplicationLagBytes))
	b.WriteString("# HELP sqlion_replication_current_lsn Current Log Sequence Number (LSN).\n")
	b.WriteString("# TYPE sqlion_replication_current_lsn counter\n")
	b.WriteString(fmt.Sprintf("sqlion_replication_current_lsn %d\n\n", st.ReplicationCurrentLSN))

	// 8. Total tables
	tblCount := st.TableCount
	b.WriteString("# HELP sqlion_tables_total Total number of user tables in the database engine.\n")
	b.WriteString("# TYPE sqlion_tables_total gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_tables_total %d\n\n", tblCount))

	// 9. Plan Cache Hit/Miss
	hits, misses := st.PlanCacheHits, st.PlanCacheMisses
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
	if st.MemoryLimitBytes > 0 {
		b.WriteString("# HELP sqlion_memory_limit_bytes Maximum memory budget configured for the process.\n")
		b.WriteString("# TYPE sqlion_memory_limit_bytes gauge\n")
		b.WriteString(fmt.Sprintf("sqlion_memory_limit_bytes %d\n", st.MemoryLimitBytes))
	}
	b.WriteString("# HELP sqlion_goroutines Number of currently running Go routines.\n")
	b.WriteString("# TYPE sqlion_goroutines gauge\n")
	b.WriteString(fmt.Sprintf("sqlion_goroutines %d\n", runtime.NumGoroutine()))

	return b.String()
}

// FormatOpenTelemetry formats the current engine metrics as standard OTLP/JSON.
func (m *MetricsCollector) FormatOpenTelemetry(st PrometheusServerState) ([]byte, error) {
	nowUnixNano := time.Now().UnixNano()
	uptime := time.Since(st.StartTime).Seconds()

	storageEngine := st.StorageEngine
	if storageEngine == "" {
		storageEngine = "memengine"
	}
	repRole := strings.ToLower(st.ReplicationRole)
	if repRole == "" {
		repRole = "standalone"
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	makeGaugeInt := func(name, desc, unit string, val int64) map[string]any {
		return map[string]any{
			"name":        name,
			"description": desc,
			"unit":        unit,
			"gauge": map[string]any{
				"dataPoints": []map[string]any{
					{
						"timeUnixNano": nowUnixNano,
						"asInt":        val,
					},
				},
			},
		}
	}

	makeGaugeDouble := func(name, desc, unit string, val float64) map[string]any {
		return map[string]any{
			"name":        name,
			"description": desc,
			"unit":        unit,
			"gauge": map[string]any{
				"dataPoints": []map[string]any{
					{
						"timeUnixNano": nowUnixNano,
						"asDouble":     val,
					},
				},
			},
		}
	}

	makeSumInt := func(name, desc, unit string, val int64) map[string]any {
		return map[string]any{
			"name":        name,
			"description": desc,
			"unit":        unit,
			"sum": map[string]any{
				"isMonotonic":            true,
				"aggregationTemporality": 2, // Cumulative
				"dataPoints": []map[string]any{
					{
						"timeUnixNano": nowUnixNano,
						"asInt":        val,
					},
				},
			},
		}
	}

	metrics := []map[string]any{
		makeGaugeDouble("sqlion.uptime_seconds", "Total time in seconds since SQLion server process started", "s", uptime),
		makeGaugeInt("sqlion.active_connections", "Current number of active client sessions", "1", int64(st.ActiveConnections)),
		makeSumInt("sqlion.queries_total", "Total number of SQL statements processed", "1", m.queriesTotal.Load()),
		makeSumInt("sqlion.query_errors_total", "Total number of SQL queries that resulted in an error", "1", m.queryErrorsTotal.Load()),
		makeGaugeDouble("sqlion.query_duration_seconds_total", "Total cumulative duration of executed queries in seconds", "s", float64(m.queryDurationNanos.Load())/1e9),
		makeSumInt("sqlion.slow_queries_total", "Total number of queries exceeding the slow query threshold", "1", m.slowQueriesTotal.Load()),
		{
			"name":        "sqlion.transactions_total",
			"description": "Total number of completed transactions",
			"unit":        "1",
			"sum": map[string]any{
				"isMonotonic":            true,
				"aggregationTemporality": 2,
				"dataPoints": []map[string]any{
					{
						"timeUnixNano": nowUnixNano,
						"asInt":        m.transactionsCommit.Load(),
						"attributes":   []map[string]any{{"key": "type", "value": map[string]any{"stringValue": "commit"}}},
					},
					{
						"timeUnixNano": nowUnixNano,
						"asInt":        m.transactionsRollback.Load(),
						"attributes":   []map[string]any{{"key": "type", "value": map[string]any{"stringValue": "rollback"}}},
					},
					{
						"timeUnixNano": nowUnixNano,
						"asInt":        m.transactionsConflict.Load(),
						"attributes":   []map[string]any{{"key": "type", "value": map[string]any{"stringValue": "conflict"}}},
					},
				},
			},
		},
		makeGaugeInt("sqlion.tables_total", "Total number of user tables in the database engine", "1", int64(st.TableCount)),
		makeSumInt("sqlion.plan_cache_hits_total", "Total number of stored procedure execution plan cache hits", "1", st.PlanCacheHits),
		makeSumInt("sqlion.plan_cache_misses_total", "Total number of stored procedure execution plan cache misses", "1", st.PlanCacheMisses),
		makeGaugeInt("sqlion.memory_alloc_bytes", "Bytes of allocated heap objects currently in use", "By", int64(mem.Alloc)),
		makeGaugeInt("sqlion.memory_sys_bytes", "Total bytes of memory obtained from the OS", "By", int64(mem.Sys)),
		makeGaugeInt("sqlion.memory_heap_alloc_bytes", "Bytes of allocated heap memory", "By", int64(mem.HeapAlloc)),
		makeGaugeInt("sqlion.goroutines", "Number of currently running Go routines", "1", int64(runtime.NumGoroutine())),
		makeGaugeInt("sqlion.replication_lag_bytes", "Replica replication lag in bytes behind primary", "By", st.ReplicationLagBytes),
		makeSumInt("sqlion.replication_current_lsn", "Current Log Sequence Number (LSN)", "1", st.ReplicationCurrentLSN),
	}

	if st.MemoryLimitBytes > 0 {
		metrics = append(metrics, makeGaugeInt("sqlion.memory_limit_bytes", "Maximum memory budget configured for the process", "By", st.MemoryLimitBytes))
	}

	otlp := map[string]any{
		"resourceMetrics": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{"key": "service.name", "value": map[string]any{"stringValue": "lionsql"}},
						{"key": "service.version", "value": map[string]any{"stringValue": "0.23.0"}},
						{"key": "storage.engine", "value": map[string]any{"stringValue": storageEngine}},
						{"key": "replication.role", "value": map[string]any{"stringValue": repRole}},
					},
				},
				"scopeMetrics": []map[string]any{
					{
						"scope": map[string]any{
							"name":    "sqlion.engine",
							"version": "0.23.0",
						},
						"metrics": metrics,
					},
				},
			},
		},
	}

	return json.MarshalIndent(otlp, "", "  ")
}
