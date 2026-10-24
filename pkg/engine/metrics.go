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

// TransactionsTotal returns total number of committed and rolled back transactions.
func (m *MetricsCollector) TransactionsTotal() int64 {
	return m.transactionsCommit.Load() + m.transactionsRollback.Load()
}

// QueriesTotal returns total number of queries recorded.
func (m *MetricsCollector) QueriesTotal() int64 {
	return m.queriesTotal.Load()
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
	b.WriteString("# HELP lionsql_uptime_seconds Total time in seconds since LionSQL server process started.\n")
	b.WriteString("# TYPE lionsql_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_uptime_seconds %.2f\n\n", uptime))

	// 2. Active connections
	activeConns := st.ActiveConnections
	b.WriteString("# HELP lionsql_active_connections Current number of active client sessions.\n")
	b.WriteString("# TYPE lionsql_active_connections gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_active_connections %d\n\n", activeConns))

	// 3. Total queries
	b.WriteString("# HELP lionsql_queries_total Total number of SQL statements processed.\n")
	b.WriteString("# TYPE lionsql_queries_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_queries_total %d\n", m.queriesTotal.Load()))

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
		b.WriteString(fmt.Sprintf("lionsql_queries_total{command=\"%s\"} %d\n", c.cmd, c.count))
	}
	b.WriteString("\n")

	// 4. Query errors
	b.WriteString("# HELP lionsql_query_errors_total Total number of SQL queries that resulted in an error.\n")
	b.WriteString("# TYPE lionsql_query_errors_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_query_errors_total %d\n\n", m.queryErrorsTotal.Load()))

	// 4.1 PK point lookup bypass hits
	b.WriteString("# HELP lionsql_pk_bypass_hits_total Total number of simple PK lookups handled via zero-overhead storage bypass.\n")
	b.WriteString("# TYPE lionsql_pk_bypass_hits_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_pk_bypass_hits_total %d\n\n", m.pkBypassHits.Load()))

	// 5. Total query duration
	durSec := float64(m.queryDurationNanos.Load()) / 1e9
	b.WriteString("# HELP lionsql_query_duration_seconds_total Total cumulative duration of executed queries in seconds.\n")
	b.WriteString("# TYPE lionsql_query_duration_seconds_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_query_duration_seconds_total %.6f\n\n", durSec))

	// 6. Slow queries
	b.WriteString("# HELP lionsql_slow_queries_total Total number of queries exceeding the slow query threshold.\n")
	b.WriteString("# TYPE lionsql_slow_queries_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_slow_queries_total %d\n\n", m.slowQueriesTotal.Load()))

	// 7. Transactions
	b.WriteString("# HELP lionsql_transactions_total Total number of completed transactions.\n")
	b.WriteString("# TYPE lionsql_transactions_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_transactions_total{type=\"commit\"} %d\n", m.transactionsCommit.Load()))
	b.WriteString(fmt.Sprintf("lionsql_transactions_total{type=\"rollback\"} %d\n", m.transactionsRollback.Load()))
	b.WriteString(fmt.Sprintf("lionsql_transactions_total{type=\"conflict\"} %d\n\n", m.transactionsConflict.Load()))

	// 7.1 Storage Engine Info
	storageEngine := st.StorageEngine
	if storageEngine == "" {
		storageEngine = "memengine"
	}
	b.WriteString("# HELP lionsql_storage_info Database storage engine metadata.\n")
	b.WriteString("# TYPE lionsql_storage_info gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_storage_info{engine=\"%s\"} 1\n\n", storageEngine))

	// 7.2 Replication Status
	repRole := strings.ToLower(st.ReplicationRole)
	if repRole == "" {
		repRole = "standalone"
	}
	b.WriteString("# HELP lionsql_replication_role HA availability group role.\n")
	b.WriteString("# TYPE lionsql_replication_role gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_replication_role{role=\"%s\"} 1\n", repRole))
	b.WriteString("# HELP lionsql_replication_lag_bytes Replica replication lag in bytes behind primary.\n")
	b.WriteString("# TYPE lionsql_replication_lag_bytes gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_replication_lag_bytes %d\n", st.ReplicationLagBytes))
	b.WriteString("# HELP lionsql_replication_current_lsn Current Log Sequence Number (LSN).\n")
	b.WriteString("# TYPE lionsql_replication_current_lsn counter\n")
	b.WriteString(fmt.Sprintf("lionsql_replication_current_lsn %d\n\n", st.ReplicationCurrentLSN))

	// 8. Total tables
	tblCount := st.TableCount
	b.WriteString("# HELP lionsql_tables_total Total number of user tables in the database engine.\n")
	b.WriteString("# TYPE lionsql_tables_total gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_tables_total %d\n\n", tblCount))

	// 9. Plan Cache Hit/Miss
	hits, misses := st.PlanCacheHits, st.PlanCacheMisses
	b.WriteString("# HELP lionsql_plan_cache_hits_total Total number of stored procedure execution plan cache hits.\n")
	b.WriteString("# TYPE lionsql_plan_cache_hits_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_plan_cache_hits_total %d\n", hits))
	b.WriteString("# HELP lionsql_plan_cache_misses_total Total number of stored procedure execution plan cache misses.\n")
	b.WriteString("# TYPE lionsql_plan_cache_misses_total counter\n")
	b.WriteString(fmt.Sprintf("lionsql_plan_cache_misses_total %d\n\n", misses))

	// 10. Memory and Goroutine statistics
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b.WriteString("# HELP lionsql_memory_alloc_bytes Bytes of allocated heap objects currently in use.\n")
	b.WriteString("# TYPE lionsql_memory_alloc_bytes gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_memory_alloc_bytes %d\n", mem.Alloc))
	b.WriteString("# HELP lionsql_memory_sys_bytes Total bytes of memory obtained from the OS.\n")
	b.WriteString("# TYPE lionsql_memory_sys_bytes gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_memory_sys_bytes %d\n", mem.Sys))
	b.WriteString("# HELP lionsql_memory_heap_alloc_bytes Bytes of allocated heap memory.\n")
	b.WriteString("# TYPE lionsql_memory_heap_alloc_bytes gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_memory_heap_alloc_bytes %d\n", mem.HeapAlloc))
	if st.MemoryLimitBytes > 0 {
		b.WriteString("# HELP lionsql_memory_limit_bytes Maximum memory budget configured for the process.\n")
		b.WriteString("# TYPE lionsql_memory_limit_bytes gauge\n")
		b.WriteString(fmt.Sprintf("lionsql_memory_limit_bytes %d\n", st.MemoryLimitBytes))
	}
	b.WriteString("# HELP lionsql_goroutines Number of currently running Go routines.\n")
	b.WriteString("# TYPE lionsql_goroutines gauge\n")
	b.WriteString(fmt.Sprintf("lionsql_goroutines %d\n", runtime.NumGoroutine()))

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
		makeGaugeDouble("lionsql.uptime_seconds", "Total time in seconds since LionSQL server process started", "s", uptime),
		makeGaugeInt("lionsql.active_connections", "Current number of active client sessions", "1", int64(st.ActiveConnections)),
		makeSumInt("lionsql.queries_total", "Total number of SQL statements processed", "1", m.queriesTotal.Load()),
		makeSumInt("lionsql.query_errors_total", "Total number of SQL queries that resulted in an error", "1", m.queryErrorsTotal.Load()),
		makeGaugeDouble("lionsql.query_duration_seconds_total", "Total cumulative duration of executed queries in seconds", "s", float64(m.queryDurationNanos.Load())/1e9),
		makeSumInt("lionsql.slow_queries_total", "Total number of queries exceeding the slow query threshold", "1", m.slowQueriesTotal.Load()),
		{
			"name":        "lionsql.transactions_total",
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
		makeGaugeInt("lionsql.tables_total", "Total number of user tables in the database engine", "1", int64(st.TableCount)),
		makeSumInt("lionsql.plan_cache_hits_total", "Total number of stored procedure execution plan cache hits", "1", st.PlanCacheHits),
		makeSumInt("lionsql.plan_cache_misses_total", "Total number of stored procedure execution plan cache misses", "1", st.PlanCacheMisses),
		makeGaugeInt("lionsql.memory_alloc_bytes", "Bytes of allocated heap objects currently in use", "By", int64(mem.Alloc)),
		makeGaugeInt("lionsql.memory_sys_bytes", "Total bytes of memory obtained from the OS", "By", int64(mem.Sys)),
		makeGaugeInt("lionsql.memory_heap_alloc_bytes", "Bytes of allocated heap memory", "By", int64(mem.HeapAlloc)),
		makeGaugeInt("lionsql.goroutines", "Number of currently running Go routines", "1", int64(runtime.NumGoroutine())),
		makeGaugeInt("lionsql.replication_lag_bytes", "Replica replication lag in bytes behind primary", "By", st.ReplicationLagBytes),
		makeSumInt("lionsql.replication_current_lsn", "Current Log Sequence Number (LSN)", "1", st.ReplicationCurrentLSN),
	}

	if st.MemoryLimitBytes > 0 {
		metrics = append(metrics, makeGaugeInt("lionsql.memory_limit_bytes", "Maximum memory budget configured for the process", "By", st.MemoryLimitBytes))
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
							"name":    "lionsql.engine",
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
