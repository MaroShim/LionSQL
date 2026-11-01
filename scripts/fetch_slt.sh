#!/usr/bin/env bash
# fetch_slt.sh - Fetches the LionSQL SQLLogicTest 1.0 GA Full Suite (~40,000 queries)
# Mirrors from the official SQLite SQLLogicTest repository via CockroachDB mirror.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_DEST="${ROOT_DIR}/test/slt"
BASE_URL="https://raw.githubusercontent.com/cockroachdb/sqllogictest/master/test"

FORCE=false
SUITE="all"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --force|-f)
            FORCE=true
            shift
            ;;
        --suite|-s)
            SUITE="$2"
            shift 2
            ;;
        --help|-h)
            echo "Usage: $0 [options]"
            echo "Options:"
            echo "  --suite, -s <all|full|evidence|index|random>  Fetch specific test suite (default: all)"
            echo "  --force, -f                                  Overwrite existing test files"
            echo "  --help, -h                                   Display this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

fetch_file() {
    local rel_path="$1"
    local dest="${BASE_DEST}/${rel_path}"
    local dir
    dir="$(dirname "${dest}")"
    mkdir -p "${dir}"

    if [[ -s "${dest}" && "${FORCE}" == false ]]; then
        echo "  [cached] ${rel_path} ($(wc -l < "${dest}" | tr -d ' ') lines)"
        return 0
    fi

    echo "  [fetch]  ${rel_path} from upstream..."
    if curl -sSL -f "${BASE_URL}/${rel_path}" -o "${dest}"; then
        echo "  [saved]  ${rel_path} ($(wc -l < "${dest}" | tr -d ' ') lines)"
    else
        echo "  [error]  Failed to fetch ${rel_path}" >&2
        rm -f "${dest}"
        return 1
    fi
}

echo "================================================================================"
echo "  LionSQL SQLLogicTest 1.0 GA Full Suite Fetcher"
echo "================================================================================"
echo "  Target Directory: ${BASE_DEST}"
echo "  Selected Suite:   ${SUITE}"
echo "  Force Overwrite:  ${FORCE}"
echo "--------------------------------------------------------------------------------"

# 1. Full Suites (select1.test ~ select5.test)
if [[ "${SUITE}" == "all" || "${SUITE}" == "full" ]]; then
    echo ">> Fetching Tier 1 'full' suites (select1.test ~ select5.test)..."
    for s in select1.test select2.test select3.test select4.test select5.test; do
        fetch_file "full/${s}"
    done
fi

# 2. Evidence Suites (12 specification files)
if [[ "${SUITE}" == "all" || "${SUITE}" == "evidence" ]]; then
    echo ">> Fetching Tier 2 'evidence' specification suites..."
    evidence_files=(
        "evidence/in1.test"
        "evidence/in2.test"
        "evidence/slt_lang_aggfunc.test"
        "evidence/slt_lang_createtrigger.test"
        "evidence/slt_lang_createview.test"
        "evidence/slt_lang_dropindex.test"
        "evidence/slt_lang_droptable.test"
        "evidence/slt_lang_droptrigger.test"
        "evidence/slt_lang_dropview.test"
        "evidence/slt_lang_reindex.test"
        "evidence/slt_lang_replace.test"
        "evidence/slt_lang_update.test"
    )
    for f in "${evidence_files[@]}"; do
        fetch_file "${f}"
    done
fi

# 3. Index Suites (16 B-Tree range & commute files)
if [[ "${SUITE}" == "all" || "${SUITE}" == "index" ]]; then
    echo ">> Fetching Tier 2 'index' suites..."
    index_files=(
        "index/between/1/slt_good_0.test"
        "index/between/10/slt_good_0.test"
        "index/between/100/slt_good_0.test"
        "index/commute/1/slt_good_0.test"
        "index/commute/10/slt_good_0.test"
        "index/commute/100/slt_good_0.test"
        "index/in/1/slt_good_0.test"
        "index/in/10/slt_good_0.test"
        "index/in/100/slt_good_0.test"
        "index/orderby/1/slt_good_0.test"
        "index/orderby/10/slt_good_0.test"
        "index/orderby/100/slt_good_0.test"
        "index/orderby_nosort/1/slt_good_0.test"
        "index/orderby_nosort/10/slt_good_0.test"
        "index/delete/1/slt_good_0.test"
        "index/delete/10/slt_good_0.test"
        "index/random/10/slt_good_0.test"
        "index/view/10/slt_good_0.test"
    )
    for f in "${index_files[@]}"; do
        fetch_file "${f}"
    done
fi

# 4. Random Suites (Representative 10-packs for expr, groupby, aggregates, select)
if [[ "${SUITE}" == "all" || "${SUITE}" == "random" ]]; then
    echo ">> Fetching Tier 2 'random' representative suites (40 test files)..."
    for category in expr groupby aggregates select; do
        for i in {0..9}; do
            fetch_file "random/${category}/slt_good_${i}.test"
        done
    done
fi

echo "--------------------------------------------------------------------------------"
echo "  Fetch Complete! All files staged in ${BASE_DEST}"
echo "  Run SLT suite with:"
echo "    ./bin/sqllogictest -dir test/slt -parallel 8"
echo "================================================================================"
