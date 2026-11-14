#!/usr/bin/env bash
# scripts/verify_no_slt_mutation.sh
# Enforces Invariant 16: Zero-Tampering Oracle Test Invariant.
# Aborts with exit code 1 if any .slt or test/corpora/ oracle file has been modified or staged.

set -euo pipefail

if [ "${ALLOW_ORACLE_UPDATE:-0}" = "1" ]; then
    echo "[OK] ALLOW_ORACLE_UPDATE=1 set: authorized oracle baseline update."
    exit 0
fi

# 1. Check staged files in git index
staged_violations=$(git diff --cached --name-only 2>/dev/null | grep -E '(\.slt$|^test/corpora/)' || true)
if [ -n "$staged_violations" ]; then
    echo "==========================================================================" >&2
    echo "[FATAL][SECURITY] Zero-Tampering Oracle Invariant Violation Detected!" >&2
    echo "Modifications to *.slt test suites or test/corpora/ are strictly forbidden." >&2
    echo "Staged forbidden files:" >&2
    echo "$staged_violations" | sed 's/^/  - /' >&2
    echo "Tests are the immutable ground truth; engine code must adapt to tests, never tests to engine." >&2
    echo "==========================================================================" >&2
    exit 1
fi

# 2. Check unstaged working tree modifications against HEAD
working_violations=$(git diff --name-only 2>/dev/null | grep -E '(\.slt$|^test/corpora/)' || true)
if [ -n "$working_violations" ]; then
    echo "==========================================================================" >&2
    echo "[FATAL][SECURITY] Zero-Tampering Oracle Invariant Violation Detected!" >&2
    echo "Unstaged modifications to *.slt test suites or test/corpora/ detected." >&2
    echo "Modified forbidden files:" >&2
    echo "$working_violations" | sed 's/^/  - /' >&2
    echo "Revert these changes immediately: git checkout -- <files>" >&2
    echo "==========================================================================" >&2
    exit 1
fi

echo "[OK] Zero-Tampering Oracle check passed: zero *.slt or corpora modifications detected."
exit 0
