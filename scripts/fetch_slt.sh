#!/usr/bin/env bash
# fetch_slt.sh - Fetches official SQLite SQLLogicTest test suites from CockroachDB / SQLite mirrors.
set -euo pipefail

DEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/test/slt/full"
mkdir -p "${DEST_DIR}"

BASE_URL="https://raw.githubusercontent.com/cockroachdb/sqllogictest/master/test"

for suite in select1.test select2.test; do
    echo "[slt] Fetching ${suite} from upstream..."
    curl -sSL "${BASE_URL}/${suite}" -o "${DEST_DIR}/${suite}"
    echo "[slt] Saved to ${DEST_DIR}/${suite} ($(wc -l < "${DEST_DIR}/${suite}") lines)"
done

echo "[slt] Fetch complete."
