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

# 1. Full Suites (select1.slt ~ select5.slt)
if [[ "${SUITE}" == "all" || "${SUITE}" == "full" ]]; then
    echo ">> Fetching Tier 1 'full' suites (select1.slt ~ select5.slt)..."
    for s in select1.slt select2.slt select3.slt select4.slt select5.slt; do
        fetch_file "full/${s}"
    done
fi

# 2. Evidence Suites (Specification files)
if [[ "${SUITE}" == "all" || "${SUITE}" == "evidence" ]]; then
    echo ">> Fetching Tier 2 'evidence' specification suites..."
    evidence_files=(
        "evidence/in2.slt"
        "evidence/slt_lang_aggfunc.slt"
        "evidence/slt_lang_createtrigger.slt"
        "evidence/slt_lang_createview.slt"
        "evidence/slt_lang_dropindex.slt"
        "evidence/slt_lang_droptable.slt"
        "evidence/slt_lang_droptrigger.slt"
        "evidence/slt_lang_dropview.slt"
        "evidence/slt_lang_reindex.slt"
        "evidence/slt_lang_replace.slt"
        "evidence/slt_lang_update.slt"
    )
    for f in "${evidence_files[@]}"; do
        fetch_file "${f}"
    done
fi

# 3. Index Suites (Moved to sqlite_fuzz in v0.90.0 per Invariant/RadicalKISS)
if [[ "${SUITE}" == "fuzz" || "${SUITE}" == "index" ]]; then
    echo ">> Fetching Tier 2 'index' fuzz suites into test/sqlite_fuzz..."
    index_files=(
        "index/between/1/slt_good_0.slt"
        "index/between/10/slt_good_0.slt"
        "index/between/100/slt_good_0.slt"
        "index/commute/10/slt_good_0.slt"
        "index/commute/100/slt_good_0.slt"
        "index/commute/1000/slt_good_0.slt"
        "index/in/10/slt_good_0.slt"
        "index/in/100/slt_good_0.slt"
        "index/in/1000/slt_good_0.slt"
        "index/orderby/10/slt_good_0.slt"
        "index/orderby/100/slt_good_0.slt"
        "index/orderby_nosort/10/slt_good_0.slt"
        "index/orderby_nosort/100/slt_good_0.slt"
        "index/delete/1/slt_good_0.slt"
        "index/delete/10/slt_good_0.slt"
        "index/random/10/slt_good_0.slt"
        "index/view/10/slt_good_0.slt"
    )
    for f in "${index_files[@]}"; do
        fetch_file "${f}"
    done
fi
        fetch_file "${f}"
    done
fi

# 4. Random Suites (Representative 10-packs for expr, groupby, aggregates, select)
if [[ "${SUITE}" == "all" || "${SUITE}" == "random" ]]; then
    echo ">> Fetching Tier 2 'random' representative suites (40 test files)..."
    for category in expr groupby aggregates select; do
        for i in {0..9}; do
            fetch_file "random/${category}/slt_good_${i}.slt"
        done
    done
fi

echo "--------------------------------------------------------------------------------"
echo "  Fetch Complete! All files staged in ${BASE_DEST}"
echo "  Run SLT suite with:"
echo "    ./bin/sqllogictest -dir test/slt -parallel 8"
echo "================================================================================"
