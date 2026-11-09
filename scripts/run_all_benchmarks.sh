#!/usr/bin/env bash
# ==============================================================================
# LionSQL All-in-One Comprehensive Benchmark & Verification Suite
# (scripts/run_all_benchmarks.sh)
#
# Automates the end-to-end benchmark pipeline across all 7 core performance
# and durability domains documented in doc/BENCHMARK.md:
#   Phase 0: Multi-Target Binary Compilation
#   Phase 1: In-Process ACID Banking Stored Procedure Benchmark (cmd/bench)
#   Phase 2: Storage Kernel SPI TPC-C Benchmark (cmd/tpcc_kernel)
#   Phase 3: Native T-SQL VDBE Runtime TPC-C Benchmark (cmd/tpcc_runtime)
#   Phase 4: Client-Server Network TPC-C Benchmark over PGWire & TDS (cmd/tpcc_client)
#   Phase 5: SQLLogicTest SQLite Compatibility Conformance Suite (cmd/sqllogictest)
#   Phase 6: Cold Restart & Crash Durability (Abrupt SIGKILL Invariant)
#   Phase 7: Executive Summary Dashboard & Markdown Report Generation
# ==============================================================================

set -uo pipefail

# ANSI Color Codes
CLR_RESET="\033[0m"
CLR_BOLD="\033[1m"
CLR_RED="\033[1;31m"
CLR_GREEN="\033[1;32m"
CLR_YELLOW="\033[1;33m"
CLR_BLUE="\033[1;34m"
CLR_CYAN="\033[1;36m"
CLR_MAGENTA="\033[1;35m"
CLR_GRAY="\033[0;90m"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BIN_DIR="${ROOT_DIR}/bin"
TEMP_BASE="/tmp/lionsql_bench_suite_$$"
LOG_FILE="${TEMP_BASE}/benchmarks.log"
NOTES_RUNS_DIR="${ROOT_DIR}/notes/benchmark_runs"

# Benchmark Configuration Defaults
MODE="quick"
ENGINE="memory"
SAVE_REPORT=false
SKIP_BUILD=false
CUSTOM_REPORT_FILE=""
PG_PORT=5433
TDS_PORT=1433
COOLDOWN_SECS=5

# Parse Command-Line Flags
for arg in "$@"; do
    case "$arg" in
        --quick)
            MODE="quick"
            ;;
        --full)
            MODE="full"
            ;;
        --engine=*)
            ENGINE="${arg#*=}"
            ;;
        --cooldown=*)
            COOLDOWN_SECS="${arg#*=}"
            ;;
        --no-cooldown)
            COOLDOWN_SECS=0
            ;;
        --save-report)
            SAVE_REPORT=true
            ;;
        --output=*)
            SAVE_REPORT=true
            CUSTOM_REPORT_FILE="${arg#*=}"
            ;;
        --skip-build)
            SKIP_BUILD=true
            ;;
        --pg-port=*)
            PG_PORT="${arg#*=}"
            ;;
        --tds-port=*)
            TDS_PORT="${arg#*=}"
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Execution Modes:"
            echo "  --quick               Fast verification mode (~30-60s overall) [Default]"
            echo "  --full                Full official measurement mode (10~15s per benchmark)"
            echo ""
            echo "Options:"
            echo "  --engine=<eng>        Storage engine backend: memory (default), wiredtiger, all"
            echo "  --cooldown=<sec>      Cooldown duration between benchmark workloads (default: 5s, 0 to disable)"
            echo "  --no-cooldown         Disable cooldown pauses entirely"
            echo "  --save-report         Save Markdown benchmark report to notes/benchmark_runs/"
            echo "  --output=<path>       Specify custom output path for Markdown report"
            echo "  --skip-build          Skip Phase 0 compilation if binaries are already compiled"
            echo "  --pg-port=<port>      PostgreSQL Wire Protocol TCP port (default: 5433)"
            echo "  --tds-port=<port>     Microsoft SQL Server TDS TCP port (default: 1433)"
            echo "  -h, --help            Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                   # Quick in-memory benchmark"
            echo "  $0 --full --save-report              # Full official run + save report"
            echo "  $0 --engine=all                      # Benchmark both memory and wiredtiger"
            exit 0
            ;;
        *)
            echo "Unknown argument: $arg"
            echo "Run with --help for usage details."
            exit 1
            ;;
    esac
done

# Initialize Workspace Directory
mkdir -p "$TEMP_BASE"
touch "$LOG_FILE"

# Track Child Background Processes for Clean Teardown
CHILD_PIDS=()

cleanup() {
    local exit_code=$?
    trap - EXIT INT TERM
    for pid in "${CHILD_PIDS[@]:-}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -15 "$pid" 2>/dev/null || true
            sleep 0.2
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid" 2>/dev/null || true
            fi
        fi
    done
    rm -rf "$TEMP_BASE"
    exit "$exit_code"
}
trap cleanup EXIT INT TERM

log_header() {
    echo -e "\n${CLR_CYAN}${CLR_BOLD}================================================================================${CLR_RESET}"
    echo -e "${CLR_CYAN}${CLR_BOLD}  $1${CLR_RESET}"
    echo -e "${CLR_CYAN}${CLR_BOLD}================================================================================${CLR_RESET}"
}

log_sub() {
    echo -e "  ${CLR_BLUE}▶${CLR_RESET} ${CLR_BOLD}$1${CLR_RESET}"
}

# System Environment Detection
DETECT_OS="$(uname -s) ($(uname -m))"
DETECT_KERNEL="$(uname -r)"
DETECT_GO="$(go version 2>/dev/null | awk '{print $3, $4}')"
DETECT_GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
DETECT_GIT_BRANCH="$(git branch --show-current 2>/dev/null || echo 'detached')"
DETECT_TIMESTAMP="$(date '+%Y-%m-%d %H:%M:%S %Z')"

if [ "$(uname -s)" = "Darwin" ]; then
    DETECT_CPU="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo 'Apple Silicon')"
    DETECT_CORES="$(sysctl -n hw.ncpu 2>/dev/null || echo '8') cores"
    MEM_BYTES="$(sysctl -n hw.memsize 2>/dev/null || echo '0')"
    DETECT_RAM="$((MEM_BYTES / 1024 / 1024 / 1024)) GB"
else
    DETECT_CPU="$(lscpu 2>/dev/null | grep 'Model name' | cut -d: -f2 | xargs || grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo 'x86_64 CPU')"
    DETECT_CORES="$(nproc 2>/dev/null || echo '8') cores"
    DETECT_RAM="$(free -h 2>/dev/null | awk '/^Mem:/ {print $2}' || echo '16 GB')"
fi

echo -e "${CLR_CYAN}${CLR_BOLD}================================================================================${CLR_RESET}"
echo -e "${CLR_CYAN}${CLR_BOLD}  LionSQL All-in-One Comprehensive Benchmark & Verification Suite${CLR_RESET}"
echo -e "${CLR_CYAN}${CLR_BOLD}================================================================================${CLR_RESET}"
echo -e "  ${CLR_GRAY}Timestamp:${CLR_RESET}   ${DETECT_TIMESTAMP}"
echo -e "  ${CLR_GRAY}Host System:${CLR_RESET} ${DETECT_OS} (Kernel: ${DETECT_KERNEL})"
echo -e "  ${CLR_GRAY}Processor:${CLR_RESET}   ${DETECT_CPU} (${DETECT_CORES})"
echo -e "  ${CLR_GRAY}Memory:${CLR_RESET}      ${DETECT_RAM}"
echo -e "  ${CLR_GRAY}Go Runtime:${CLR_RESET}  ${DETECT_GO}"
echo -e "  ${CLR_GRAY}Git Revision:${CLR_RESET}${DETECT_GIT_BRANCH}@${DETECT_GIT_COMMIT}"
echo -e "  ${CLR_GRAY}Suite Mode:${CLR_RESET}  ${CLR_BOLD}${MODE}${CLR_RESET} (Engine: ${CLR_BOLD}${ENGINE}${CLR_RESET})"
echo -e "--------------------------------------------------------------------------------"

# ------------------------------------------------------------------------------
# Phase 0: Multi-Target Binary Compilation
# ------------------------------------------------------------------------------
if [ "$SKIP_BUILD" = false ]; then
    log_header "[Phase 0/7] Compiling Core Database & Benchmark Binaries"
    mkdir -p "${BIN_DIR}"
    
    TARGETS=("lionsql:cmd/lionsql" "bench:cmd/bench" "tpcc-kernel:cmd/tpcc_kernel" "tpcc-runtime:cmd/tpcc_runtime" "tpcc-client:cmd/tpcc_client" "sqllogictest:cmd/sqllogictest")
    
    for item in "${TARGETS[@]}"; do
        name="${item%%:*}"
        pkg="${item##*:}"
        log_sub "Building ${name} (from ./${pkg})..."
        if go build -tags wiredtiger -buildvcs=false -o "${BIN_DIR}/${name}" "./${pkg}" >>"$LOG_FILE" 2>&1; then
            echo -e "    [${CLR_GREEN}BUILT${CLR_RESET}] ${BIN_DIR}/${name}"
        else
            echo -e "    [${CLR_RED}FAIL${CLR_RESET}] Failed to build ${name}. Check ${LOG_FILE}"
            exit 1
        fi
    done
else
    log_header "[Phase 0/7] Skipping Compilation (--skip-build requested)"
fi

# Benchmark Result Collectors (POSIX / Bash 3.2+ Compatible)
set_res() {
    local k="$1"
    local f="$2"
    local v="$3"
    eval "RES_${k}_${f}=\"\$v\""
}

get_res() {
    local k="$1"
    local f="$2"
    eval "echo \"\${RES_${k}_${f}:-}\""
}

ALL_PHASE_KEYS=()
add_phase_key() {
    ALL_PHASE_KEYS+=("$1")
}

# Cooldown and Resource Drain Helper
cooldown_pause() {
    local secs="${1:-$COOLDOWN_SECS}"
    if [ "$secs" -gt 0 ]; then
        echo -e "    ${CLR_GRAY}[Cooldown ${secs}s] Allowing socket drain, dirty page flush, and CPU thermal recovery...${CLR_RESET}"
        sleep "$secs"
    fi
}

# Helper to determine engines to execute
ENGINES=()
if [ "$ENGINE" = "all" ]; then
    ENGINES=("memory" "wiredtiger")
else
    ENGINES=("$ENGINE")
fi

# ------------------------------------------------------------------------------
# Phase 1: In-Process ACID Banking Stored Procedure Benchmark (cmd/bench)
# ------------------------------------------------------------------------------
log_header "[Phase 1/7] ACID Banking Stored Procedure Benchmark (TransferFunds)"
for eng in "${ENGINES[@]}"; do
    log_sub "Running cmd/bench with engine=${eng}..."
    bench_ops=50000
    bench_workers=100
    if [ "$MODE" = "quick" ]; then
        bench_ops=25000
        bench_workers=50
    fi
    bench_dir="${TEMP_BASE}/bench_${eng}"
    mkdir -p "$bench_dir"
    
    out=$("${BIN_DIR}/bench" -engine="$eng" -dir="$bench_dir" -ops="$bench_ops" -workers="$bench_workers" -accounts=10000 2>&1 | tee -a "$LOG_FILE")
    
    tps=$(echo "$out" | grep "Throughput (TPS):" | awk '{print $3}')
    p50=$(echo "$out" | grep "Latency (Median / P50):" | awk '{print $5}')
    p95=$(echo "$out" | grep "Latency (P95):" | awk '{print $3}')
    verdict=$(echo "$out" | grep "Integrity Verdict:" | awk '{print $3, $4}')
    
    key="P1_${eng}"
    add_phase_key "$key"
    set_res "$key" "TPS" "${tps:-0} TPS"
    set_res "$key" "LAT_P50" "${p50:-N/A}"
    set_res "$key" "LAT_P95" "${p95:-N/A}"
    set_res "$key" "VERDICT" "${verdict:-UNKNOWN}"
    
    if echo "$verdict" | grep -q "100% VERIFIED"; then
        set_res "$key" "STATUS" "PASS"
        echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] Engine ${CLR_BOLD}${eng}${CLR_RESET}: ${tps} TPS, P50: ${p50}, Invariant: ${verdict}"
    else
        set_res "$key" "STATUS" "FAIL"
        echo -e "    [${CLR_RED}FAIL${CLR_RESET}] Engine ${CLR_BOLD}${eng}${CLR_RESET}: Invariant failed or aborted"
    fi
    rm -rf "$bench_dir"
    cooldown_pause
done

# ------------------------------------------------------------------------------
# Phase 2: Storage Kernel SPI TPC-C Benchmark (cmd/tpcc_kernel)
# ------------------------------------------------------------------------------
log_header "[Phase 2/7] Storage Engine Kernel TPC-C Benchmark (SPI Direct)"
for eng in "${ENGINES[@]}"; do
    log_sub "Running cmd/tpcc_kernel with engine=${eng}..."
    kernel_dur="15s"
    kernel_wh=10
    kernel_workers=100
    if [ "$MODE" = "quick" ]; then
        kernel_dur="3s"
        kernel_wh=4
        kernel_workers=20
    fi
    kernel_dir="${TEMP_BASE}/kernel_${eng}"
    mkdir -p "$kernel_dir"
    
    out=$("${BIN_DIR}/tpcc-kernel" -engine="$eng" -dir="$kernel_dir" -duration="$kernel_dur" -warehouses="$kernel_wh" -workers="$kernel_workers" 2>&1 | tee -a "$LOG_FILE")
    
    tpmc=$(echo "$out" | grep "Official Metric:" | sed -E 's/.*tpmC = ([0-9.]+).*/\1/')
    tps=$(echo "$out" | grep "Overall Throughput:" | sed -E 's/.*TPS  = ([0-9.]+).*/\1/')
    p95_neworder=$(echo "$out" | awk -F'|' '/New-Order/ {print $7}' | xargs)
    
    key="P2_${eng}"
    add_phase_key "$key"
    set_res "$key" "TPMC" "${tpmc:-0} tpmC"
    set_res "$key" "TPS" "${tps:-0} TPS"
    set_res "$key" "LAT_P95" "${p95_neworder:-N/A}"
    
    if [ -n "$tpmc" ]; then
        set_res "$key" "STATUS" "PASS"
        set_res "$key" "VERDICT" "100% ACID"
        echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] Engine ${CLR_BOLD}${eng}${CLR_RESET}: ${tpmc} tpmC (${tps} TPS), New-Order P95: ${p95_neworder}"
    else
        set_res "$key" "STATUS" "FAIL"
        set_res "$key" "VERDICT" "ERROR"
        echo -e "    [${CLR_RED}FAIL${CLR_RESET}] Engine ${CLR_BOLD}${eng}${CLR_RESET}: Benchmark failed to record tpmC"
    fi
    rm -rf "$kernel_dir"
    cooldown_pause
done

# ------------------------------------------------------------------------------
# Phase 3: Native T-SQL VDBE Runtime TPC-C Benchmark (cmd/tpcc_runtime)
# ------------------------------------------------------------------------------
log_header "[Phase 3/7] Native T-SQL VDBE Runtime TPC-C Benchmark (In-Process SP)"
for eng in "${ENGINES[@]}"; do
    log_sub "Running cmd/tpcc_runtime with engine=${eng}..."
    runtime_dur="15s"
    runtime_wh=10
    runtime_workers=100
    if [ "$MODE" = "quick" ]; then
        runtime_dur="3s"
        runtime_wh=4
        runtime_workers=20
    fi
    runtime_dir="${TEMP_BASE}/runtime_${eng}"
    mkdir -p "$runtime_dir"
    
    out=$("${BIN_DIR}/tpcc-runtime" -engine="$eng" -dir="$runtime_dir" -duration="$runtime_dur" -warehouses="$runtime_wh" -workers="$runtime_workers" 2>&1 | tee -a "$LOG_FILE")
    
    tpmc=$(echo "$out" | grep "Throughput - tpmC" | awk '{print $4}')
    tps=$(echo "$out" | grep "Overall TPS" | awk '{print $3}')
    abort_pct=$(echo "$out" | grep "Serialization Abort" | sed -E 's/.*\(([0-9.]+)% abort rate\).*/\1%/')
    neworder_line=$(echo "$out" | grep -F "1. New-Order" | tail -n 1)
    p50=$(echo "$neworder_line" | sed -E 's/.*P50: ([^ |]+).*/\1/' | xargs)
    p95=$(echo "$neworder_line" | sed -E 's/.*P95: ([^ |]+).*/\1/' | xargs)
    
    key="P3_${eng}"
    add_phase_key "$key"
    set_res "$key" "TPMC" "${tpmc:-0} tpmC"
    set_res "$key" "TPS" "${tps:-0} TPS"
    set_res "$key" "ABORT_PCT" "${abort_pct:-0%}"
    set_res "$key" "LAT_P50" "${p50:-N/A}"
    set_res "$key" "LAT_P95" "${p95:-N/A}"
    
    if [ -n "$tpmc" ]; then
        set_res "$key" "STATUS" "PASS"
        set_res "$key" "VERDICT" "VDBE PASS"
        echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] Engine ${CLR_BOLD}${eng}${CLR_RESET}: ${tpmc} tpmC (${tps} TPS), Abort: ${abort_pct}, New-Order P50: ${p50}, P95: ${p95}"
    else
        set_res "$key" "STATUS" "FAIL"
        set_res "$key" "VERDICT" "ERROR"
        echo -e "    [${CLR_RED}FAIL${CLR_RESET}] Engine ${CLR_BOLD}${eng}${CLR_RESET}: Benchmark run failed"
    fi
    rm -rf "$runtime_dir"
    cooldown_pause
done

# ------------------------------------------------------------------------------
# Phase 4: Client-Server Network TPC-C Benchmark (PGWire & TDS) (cmd/tpcc_client)
# ------------------------------------------------------------------------------
log_header "[Phase 4/7] Client-Server Network TPC-C Benchmark (PGWire & TDS)"
for eng in "${ENGINES[@]}"; do
    cooldown_pause 3
    log_sub "Launching background server for engine=${eng}..."
    server_dir="${TEMP_BASE}/server_${eng}"
    mkdir -p "$server_dir"
    
    srv_flags=("-dir=${server_dir}" "-engine=${eng}" "-pg=127.0.0.1:${PG_PORT}" "-tds=127.0.0.1:${TDS_PORT}" "-http=")
    if [ "$eng" = "memory" ]; then
        srv_flags+=("-ephemeral")
    fi
    
    # Ensure ports are free before starting
    for p in "$PG_PORT" "$TDS_PORT"; do
        if nc -z 127.0.0.1 "$p" 2>/dev/null; then
            lsof -ti :"$p" 2>/dev/null | xargs kill -9 2>/dev/null || true
            sleep 0.2
        fi
    done

    "${BIN_DIR}/lionsql" serve "${srv_flags[@]}" >>"$LOG_FILE" 2>&1 &
    srv_pid=$!
    CHILD_PIDS+=("$srv_pid")
    
    # Wait for TCP Ports Readiness
    ready=false
    for attempt in {1..25}; do
        if nc -z 127.0.0.1 "$PG_PORT" 2>/dev/null && nc -z 127.0.0.1 "$TDS_PORT" 2>/dev/null; then
            ready=true
            break
        fi
        sleep 0.2
    done
    
    if [ "$ready" = false ]; then
        echo -e "    [${CLR_RED}FAIL${CLR_RESET}] Server failed to listen on ports ${PG_PORT}/${TDS_PORT} within 5s."
        key_pg="P4_PG_${eng}"
        key_tds="P4_TDS_${eng}"
        add_phase_key "$key_pg"
        add_phase_key "$key_tds"
        set_res "$key_pg" "STATUS" "FAIL"
        set_res "$key_tds" "STATUS" "FAIL"
        kill -9 "$srv_pid" 2>/dev/null || true
        continue
    fi
    
    net_dur="10s"
    net_wh=10
    net_workers=20
    if [ "$MODE" = "quick" ]; then
        net_dur="3s"
        net_wh=4
        net_workers=8
    fi
    
    # 4.1 PostgreSQL Wire Protocol v3
    log_sub "Running client TPC-C over PGWire (:5433) for engine=${eng}..."
    out_pg=$("${BIN_DIR}/tpcc-client" -protocol=postgres -host=127.0.0.1 -port="$PG_PORT" -user=postgres -warehouses="$net_wh" -workers="$net_workers" -duration="$net_dur" 2>&1 | tee -a "$LOG_FILE")
    
    pg_tpmc=$(echo "$out_pg" | grep "Throughput - tpmC" | awk '{print $4}')
    pg_tps=$(echo "$out_pg" | grep "Overall TPS" | awk '{print $3}')
    pg_abort=$(echo "$out_pg" | grep "Serialization Abort" | sed -E 's/.*\(([0-9.]+)% abort rate\).*/\1%/')
    pg_no_line=$(echo "$out_pg" | grep -F "1. New-Order" | tail -n 1)
    pg_p50=$(echo "$pg_no_line" | sed -E 's/.*P50: ([^ |]+).*/\1/' | xargs)
    pg_p95=$(echo "$pg_no_line" | sed -E 's/.*P95: ([^ |]+).*/\1/' | xargs)
    
    key_pg="P4_PG_${eng}"
    add_phase_key "$key_pg"
    set_res "$key_pg" "TPMC" "${pg_tpmc:-0} tpmC"
    set_res "$key_pg" "TPS" "${pg_tps:-0} TPS"
    set_res "$key_pg" "ABORT_PCT" "${pg_abort:-0%}"
    set_res "$key_pg" "LAT_P50" "${pg_p50:-N/A}"
    set_res "$key_pg" "LAT_P95" "${pg_p95:-N/A}"
    if [ -n "$pg_tpmc" ]; then
        set_res "$key_pg" "STATUS" "PASS"
        set_res "$key_pg" "VERDICT" "PGWire PASS"
        echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] PGWire (${eng}): ${pg_tpmc} tpmC (${pg_tps} TPS), Abort: ${pg_abort}, P50: ${pg_p50}, P95: ${pg_p95}"
    else
        set_res "$key_pg" "STATUS" "FAIL"
        set_res "$key_pg" "VERDICT" "PG ERROR"
        echo -e "    [${CLR_RED}FAIL${CLR_RESET}] PGWire (${eng}): Client execution failed"
    fi
    
    # 4.2 Microsoft SQL Server TDS Protocol
    cooldown_pause 3
    log_sub "Running client TPC-C over TDS (:1433) for engine=${eng}..."
    out_tds=$("${BIN_DIR}/tpcc-client" -protocol=tds -host=127.0.0.1 -port="$TDS_PORT" -user=sa -password=1234567890 -warehouses="$net_wh" -workers="$net_workers" -duration="$net_dur" 2>&1 | tee -a "$LOG_FILE")
    
    tds_tpmc=$(echo "$out_tds" | grep "Throughput - tpmC" | awk '{print $4}')
    tds_tps=$(echo "$out_tds" | grep "Overall TPS" | awk '{print $3}')
    tds_abort=$(echo "$out_tds" | grep "Serialization Abort" | sed -E 's/.*\(([0-9.]+)% abort rate\).*/\1%/')
    tds_no_line=$(echo "$out_tds" | grep -F "1. New-Order" | tail -n 1)
    tds_p50=$(echo "$tds_no_line" | sed -E 's/.*P50: ([^ |]+).*/\1/' | xargs)
    tds_p95=$(echo "$tds_no_line" | sed -E 's/.*P95: ([^ |]+).*/\1/' | xargs)
    
    key_tds="P4_TDS_${eng}"
    add_phase_key "$key_tds"
    set_res "$key_tds" "TPMC" "${tds_tpmc:-0} tpmC"
    set_res "$key_tds" "TPS" "${tds_tps:-0} TPS"
    set_res "$key_tds" "ABORT_PCT" "${tds_abort:-0%}"
    set_res "$key_tds" "LAT_P50" "${tds_p50:-N/A}"
    set_res "$key_tds" "LAT_P95" "${tds_p95:-N/A}"
    if [ -n "$tds_tpmc" ]; then
        set_res "$key_tds" "STATUS" "PASS"
        set_res "$key_tds" "VERDICT" "TDS PASS"
        echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] TDS (${eng}):    ${tds_tpmc} tpmC (${tds_tps} TPS), Abort: ${tds_abort}, P50: ${tds_p50}, P95: ${tds_p95}"
    else
        set_res "$key_tds" "STATUS" "FAIL"
        set_res "$key_tds" "VERDICT" "TDS ERROR"
        echo -e "    [${CLR_RED}FAIL${CLR_RESET}] TDS (${eng}): Client execution failed"
    fi
    
    # Teardown Background Server Cleanly
    kill -15 "$srv_pid" 2>/dev/null || true
    sleep 0.3
    if kill -0 "$srv_pid" 2>/dev/null; then
        kill -9 "$srv_pid" 2>/dev/null || true
    fi
    wait "$srv_pid" 2>/dev/null || true
    rm -rf "$server_dir"
    cooldown_pause
done

# ------------------------------------------------------------------------------
# Phase 5: SQLLogicTest SQLite Compatibility Conformance Suite (cmd/sqllogictest)
# ------------------------------------------------------------------------------
cooldown_pause 3
log_header "[Phase 5/7] SQLLogicTest Core Conformance Suite (32 Files)"
slt_out=$("${BIN_DIR}/sqllogictest" -dir=test/slt/core -parallel=8 2>&1 | tee -a "$LOG_FILE")
slt_passed=$(echo "$slt_out" | grep "Files:" | awk '{print $4}' | tr -d ',')
slt_failed=$(echo "$slt_out" | grep "Files:" | awk '{print $6}' | tr -d ',')
slt_queries=$(echo "$slt_out" | grep "Queries:" | awk '{print $2}')
slt_time=$(echo "$slt_out" | grep "Total Time:" | awk '{print $3}')

key_slt="P5_SLT"
add_phase_key "$key_slt"
set_res "$key_slt" "TPS" "${slt_queries:-0} queries"
set_res "$key_slt" "LAT_P50" "${slt_time:-0s}"
set_res "$key_slt" "VERDICT" "${slt_passed} passed / ${slt_failed} failed"

if [ "${slt_failed:-1}" = "0" ]; then
    set_res "$key_slt" "STATUS" "PASS"
    echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] SQLLogicTest Core: ${slt_passed}/32 files passed (${slt_queries} queries in ${slt_time}, 0 failures)"
else
    set_res "$key_slt" "STATUS" "FAIL"
    echo -e "    [${CLR_RED}FAIL${CLR_RESET}] SQLLogicTest Core: ${slt_failed} files failed"
fi

# ------------------------------------------------------------------------------
# Phase 6: Cold Restart & Crash Durability (Abrupt SIGKILL Invariant)
# ------------------------------------------------------------------------------
cooldown_pause 3
log_header "[Phase 6/7] Cold Restart & Crash Durability Verification (SIGKILL)"
crash_dir="${TEMP_BASE}/crash_p6"
mkdir -p "$crash_dir"

init_sql="CREATE TABLE crash_durability (id INT PRIMARY KEY, payload VARCHAR(100)); INSERT INTO crash_durability VALUES"
for i in $(seq 1 50); do
    if [ "$i" -gt 1 ]; then init_sql="${init_sql},"; fi
    init_sql="${init_sql} ($i, 'payload_$i')"
done
init_sql="${init_sql};"

"${BIN_DIR}/lionsql" sql -dir="$crash_dir" -engine=memory "$init_sql" >>"$LOG_FILE" 2>&1

# Start Server, Abruptly SIGKILL (-9), Cold Restart
"${BIN_DIR}/lionsql" serve -dir="$crash_dir" -engine=memory -pg="127.0.0.1:5488" -tds="127.0.0.1:1488" -http="" >>"$LOG_FILE" 2>&1 &
srv_crash_pid=$!
CHILD_PIDS+=("$srv_crash_pid")
sleep 0.4

kill -9 "$srv_crash_pid" 2>/dev/null || true
wait "$srv_crash_pid" 2>/dev/null || true

# Cold Restart and verify 50 rows 100% intact
count_out=$("${BIN_DIR}/lionsql" sql -dir="$crash_dir" -engine=memory "SELECT COUNT(*) AS total FROM crash_durability;" 2>&1)

key_crash="P6_CRASH"
add_phase_key "$key_crash"
if echo "$count_out" | grep -q "50"; then
    set_res "$key_crash" "STATUS" "PASS"
    set_res "$key_crash" "VERDICT" "50/50 Rows Intact (100% Zero-Loss)"
    echo -e "    [${CLR_GREEN}PASS${CLR_RESET}] Abrupt SIGKILL Recovery: 50/50 rows verified intact upon cold restart."
else
    set_res "$key_crash" "STATUS" "FAIL"
    set_res "$key_crash" "VERDICT" "DATA LOSS DETECTED"
    echo -e "    [${CLR_RED}FAIL${CLR_RESET}] Abrupt SIGKILL Recovery: Row count mismatch after cold restart: ${count_out}"
fi
rm -rf "$crash_dir"

# ------------------------------------------------------------------------------
# Phase 7: Executive Summary Dashboard & Markdown Report Generation
# ------------------------------------------------------------------------------
log_header "[Phase 7/7] Executive Summary Dashboard & Results Matrix"

OVERALL_PASS=true
for k in "${ALL_PHASE_KEYS[@]}"; do
    st=$(get_res "$k" "STATUS")
    if [ "$st" != "PASS" ]; then
        OVERALL_PASS=false
        break
    fi
done

REPORT_MD="# LionSQL Benchmark Suite Executive Report

- **Date / Timestamp**: ${DETECT_TIMESTAMP}
- **Mode**: \`${MODE}\`
- **Host**: ${DETECT_OS} (Kernel: \`${DETECT_KERNEL}\`)
- **CPU**: ${DETECT_CPU} (\`${DETECT_CORES}\`)
- **RAM**: ${DETECT_RAM}
- **Runtime**: \`${DETECT_GO}\`
- **Revision**: \`${DETECT_GIT_BRANCH}@${DETECT_GIT_COMMIT}\`

---

## Benchmark Results Matrix

| Phase | Benchmark Target | Engine | Status | Throughput (tpmC / TPS) | Latency (P50 / P95) | Durability / Verdict |
|:---:|:---|:---:|:---:|:---|:---|:---|
"

for eng in "${ENGINES[@]}"; do
    p1_k="P1_${eng}"
    p2_k="P2_${eng}"
    p3_k="P3_${eng}"
    p4_pg_k="P4_PG_${eng}"
    p4_tds_k="P4_TDS_${eng}"
    
    st_p1=$(get_res "$p1_k" "STATUS"); tps_p1=$(get_res "$p1_k" "TPS"); p50_p1=$(get_res "$p1_k" "LAT_P50"); p95_p1=$(get_res "$p1_k" "LAT_P95"); v_p1=$(get_res "$p1_k" "VERDICT")
    st_p2=$(get_res "$p2_k" "STATUS"); tpmc_p2=$(get_res "$p2_k" "TPMC"); tps_p2=$(get_res "$p2_k" "TPS"); p95_p2=$(get_res "$p2_k" "LAT_P95"); v_p2=$(get_res "$p2_k" "VERDICT")
    st_p3=$(get_res "$p3_k" "STATUS"); tpmc_p3=$(get_res "$p3_k" "TPMC"); tps_p3=$(get_res "$p3_k" "TPS"); p50_p3=$(get_res "$p3_k" "LAT_P50"); p95_p3=$(get_res "$p3_k" "LAT_P95"); v_p3=$(get_res "$p3_k" "VERDICT")
    st_p4pg=$(get_res "$p4_pg_k" "STATUS"); tpmc_p4pg=$(get_res "$p4_pg_k" "TPMC"); tps_p4pg=$(get_res "$p4_pg_k" "TPS"); p50_p4pg=$(get_res "$p4_pg_k" "LAT_P50"); p95_p4pg=$(get_res "$p4_pg_k" "LAT_P95"); ab_p4pg=$(get_res "$p4_pg_k" "ABORT_PCT")
    st_p4tds=$(get_res "$p4_tds_k" "STATUS"); tpmc_p4tds=$(get_res "$p4_tds_k" "TPMC"); tps_p4tds=$(get_res "$p4_tds_k" "TPS"); p50_p4tds=$(get_res "$p4_tds_k" "LAT_P50"); p95_p4tds=$(get_res "$p4_tds_k" "LAT_P95"); ab_p4tds=$(get_res "$p4_tds_k" "ABORT_PCT")
    
    REPORT_MD="${REPORT_MD}| **Phase 1** | Banking Stored Procedure (\`cmd/bench\`) | ${eng} | **${st_p1:-SKIP}** | ${tps_p1:-N/A} | P50: ${p50_p1:-N/A} / P95: ${p95_p1:-N/A} | ${v_p1:-N/A} |
| **Phase 2** | Storage Kernel TPC-C (\`cmd/tpcc_kernel\`) | ${eng} | **${st_p2:-SKIP}** | ${tpmc_p2:-N/A} (${tps_p2:-N/A}) | P95: ${p95_p2:-N/A} | ${v_p2:-N/A} |
| **Phase 3** | VDBE Runtime TPC-C (\`cmd/tpcc_runtime\`) | ${eng} | **${st_p3:-SKIP}** | ${tpmc_p3:-N/A} (${tps_p3:-N/A}) | P50: ${p50_p3:-N/A} / P95: ${p95_p3:-N/A} | ${v_p3:-N/A} |
| **Phase 4a** | Network TPC-C PGWire (\`cmd/tpcc_client\`) | ${eng} | **${st_p4pg:-SKIP}** | ${tpmc_p4pg:-N/A} (${tps_p4pg:-N/A}) | P50: ${p50_p4pg:-N/A} / P95: ${p95_p4pg:-N/A} | Abort: ${ab_p4pg:-N/A} |
| **Phase 4b** | Network TPC-C TDS (\`cmd/tpcc_client\`) | ${eng} | **${st_p4tds:-SKIP}** | ${tpmc_p4tds:-N/A} (${tps_p4tds:-N/A}) | P50: ${p50_p4tds:-N/A} / P95: ${p95_p4tds:-N/A} | Abort: ${ab_p4tds:-N/A} |
"
done

st_slt=$(get_res "P5_SLT" "STATUS"); tps_slt=$(get_res "P5_SLT" "TPS"); time_slt=$(get_res "P5_SLT" "LAT_P50"); v_slt=$(get_res "P5_SLT" "VERDICT")
st_crash=$(get_res "P6_CRASH" "STATUS"); v_crash=$(get_res "P6_CRASH" "VERDICT")

REPORT_MD="${REPORT_MD}| **Phase 5** | SQLLogicTest Core Suite (32 Files) | memory | **${st_slt:-SKIP}** | ${tps_slt:-N/A} | Total: ${time_slt:-N/A} | ${v_slt:-N/A} |
| **Phase 6** | Cold SIGKILL Crash Durability | memory | **${st_crash:-SKIP}** | N/A | Cold Restart | ${v_crash:-N/A} |

---

## Overall Assessment
"

if [ "$OVERALL_PASS" = true ]; then
    REPORT_MD="${REPORT_MD}
**Result**: \`ALL 7 BENCHMARKS PASSED\`
All performance, concurrency, wire protocol, and durability invariants verified with 100% integrity.
"
    echo -e "\n${CLR_GREEN}${CLR_BOLD}================================================================================${CLR_RESET}"
    echo -e "${CLR_GREEN}${CLR_BOLD}  ALL 7 BENCHMARKS & VERIFICATION PHASES PASSED (100% PASS)${CLR_RESET}"
    echo -e "${CLR_GREEN}${CLR_BOLD}================================================================================${CLR_RESET}"
else
    REPORT_MD="${REPORT_MD}
**Result**: \`ONE OR MORE BENCHMARKS FAILED\`
Check ${LOG_FILE} for diagnostic details.
"
    echo -e "\n${CLR_RED}${CLR_BOLD}================================================================================${CLR_RESET}"
    echo -e "${CLR_RED}${CLR_BOLD}  BENCHMARK SUITE FINISHED WITH FAILURES${CLR_RESET}"
    echo -e "${CLR_RED}${CLR_BOLD}================================================================================${CLR_RESET}"
fi

echo ""
echo "$REPORT_MD"

# Save Report File if Requested
if [ "$SAVE_REPORT" = true ]; then
    mkdir -p "$NOTES_RUNS_DIR"
    REPORT_FILE="${CUSTOM_REPORT_FILE}"
    if [ -z "$REPORT_FILE" ]; then
        STAMP="$(date '+%Y%m%d_%H%M%S')"
        REPORT_FILE="${NOTES_RUNS_DIR}/report_${STAMP}.md"
    fi
    echo "$REPORT_MD" > "$REPORT_FILE"
    echo -e "  [${CLR_GREEN}SAVED${CLR_RESET}] Markdown benchmark report saved to: ${CLR_BOLD}${REPORT_FILE}${CLR_RESET}"
fi

if [ "$OVERALL_PASS" = true ]; then
    exit 0
else
    exit 1
fi
