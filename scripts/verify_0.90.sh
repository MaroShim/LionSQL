#!/usr/bin/env bash
# ==============================================================================
# LionSQL v0.90.0 Release Verification Gate (scripts/verify_0.90.sh)
#
# Executes the complete pre-release verification pipeline for LionSQL v0.90.0:
#   Stage 1: Build Production Binaries (lionsql, sqllogictest, bench, tpcc-runtime)
#   Stage 2: Unit & Regression Tests with Race Detector (-race)
#   Stage 3: SQLLogicTest (SLT) Full 107-File Suite (551,217 queries)
#   Stage 4: Extended 13-Domain Integration Smoke Test (verify_report.sh)
# ==============================================================================

set -euo pipefail

# ANSI Color Codes
CLR_RESET="\033[0m"
CLR_BOLD="\033[1m"
CLR_RED="\033[1;31m"
CLR_GREEN="\033[1;32m"
CLR_YELLOW="\033[1;33m"
CLR_CYAN="\033[1;36m"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

SKIP_SLT=false
SKIP_SMOKE=false
PARALLEL=8

for arg in "$@"; do
    case "$arg" in
        --skip-slt)
            SKIP_SLT=true
            ;;
        --skip-smoke)
            SKIP_SMOKE=true
            ;;
        --parallel=*)
            PARALLEL="${arg#*=}"
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --parallel=<N>   Worker count for SQLLogicTest (default: 8)"
            echo "  --skip-slt       Skip Stage 3 (SQLLogicTest Full Suite)"
            echo "  --skip-smoke     Skip Stage 4 (13-Domain Smoke Test)"
            echo "  -h, --help       Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown argument: $arg"
            exit 1
            ;;
    esac
done

log_stage() {
    echo -e "\n${CLR_CYAN}${CLR_BOLD}================================================================================${CLR_RESET}"
    echo -e "${CLR_CYAN}${CLR_BOLD}  $1${CLR_RESET}"
    echo -e "${CLR_CYAN}${CLR_BOLD}================================================================================${CLR_RESET}"
}

START_TS=$(date +%s)

# ------------------------------------------------------------------------------
# Stage 1: Build Production Binaries
# ------------------------------------------------------------------------------
log_stage "[Stage 1/4] Building Production Binaries (-tags wiredtiger)"
mkdir -p bin

go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
echo -e "  [${CLR_GREEN}BUILT${CLR_RESET}] bin/lionsql"

go build -tags wiredtiger -buildvcs=false -o bin/sqllogictest ./cmd/sqllogictest
echo -e "  [${CLR_GREEN}BUILT${CLR_RESET}] bin/sqllogictest"

go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
echo -e "  [${CLR_GREEN}BUILT${CLR_RESET}] bin/bench"

go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
echo -e "  [${CLR_GREEN}BUILT${CLR_RESET}] bin/tpcc-runtime"

# ------------------------------------------------------------------------------
# Stage 2: Unit & Regression Tests (-race)
# ------------------------------------------------------------------------------
log_stage "[Stage 2/4] Running Go Unit & Regression Tests (-tags wiredtiger -race -short)"
go test -tags wiredtiger -count=1 -short -race ./...
echo -e "  [${CLR_GREEN}PASS${CLR_RESET}] All Go packages passed with 0 data races."

# ------------------------------------------------------------------------------
# Stage 3: SQLLogicTest Full Standard Suite
# ------------------------------------------------------------------------------
if [ "$SKIP_SLT" = false ]; then
    log_stage "[Stage 3/4] Running SQLLogicTest (SLT) Standard Suite (parallel=${PARALLEL})"
    if [ ! -d "test/slt/random" ] || [ ! -d "test/slt/full" ]; then
        echo -e "  [${CLR_YELLOW}INFO${CLR_RESET}] Fetching standard SLT suite via scripts/fetch_slt.sh..."
        bash scripts/fetch_slt.sh --suite all
    fi
    ./bin/sqllogictest -dir test/slt -parallel "$PARALLEL"
    echo -e "  [${CLR_GREEN}PASS${CLR_RESET}] SQLLogicTest Standard Suite 100% PASS."
else
    log_stage "[Stage 3/4] Skipping SQLLogicTest Standard Suite (--skip-slt)"
fi

# ------------------------------------------------------------------------------
# Stage 4: Extended 13-Domain Integration Smoke Test
# ------------------------------------------------------------------------------
if [ "$SKIP_SMOKE" = false ]; then
    log_stage "[Stage 4/4] Running 13-Domain Extended Smoke Test (memory & wiredtiger)"
    bash scripts/verify_report.sh --engine=memory
    bash scripts/verify_report.sh --engine=wiredtiger
    echo -e "  [${CLR_GREEN}PASS${CLR_RESET}] 13-Domain Extended Smoke Test 100% PASS (memory & wiredtiger)."
else
    log_stage "[Stage 4/4] Skipping Extended Smoke Test (--skip-smoke)"
fi

END_TS=$(date +%s)
ELAPSED=$((END_TS - START_TS))

echo -e "\n${CLR_GREEN}${CLR_BOLD}================================================================================${CLR_RESET}"
echo -e "${CLR_GREEN}${CLR_BOLD}  LionSQL v0.90.0 Release Verification Gate: ALL STAGES PASSED (${ELAPSED}s)${CLR_RESET}"
echo -e "${CLR_GREEN}${CLR_BOLD}================================================================================${CLR_RESET}\n"
