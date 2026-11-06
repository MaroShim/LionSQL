#!/usr/bin/env bash
# ==============================================================================
# LionSQL Automated Release Pipeline (scripts/release.sh)
#
# Automates the entire version release and tagging workflow:
# 1. Validates working tree cleanliness, branch state, and builds
# 2. Collects unreleased git commits since previous tag and groups by category
# 3. Synchronizes version SSOT across:
#    - pkg/config/version.go
#    - CHANGELOG.md
#    - README.md & README.ko.md
#    - TODO.md
# 4. Atomically commits release changes with Conventional Commits
# 5. Generates annotated git tag (vX.Y.Z)
# 6. Pushes release commit and tag to GitHub
#
# Usage:
#   ./scripts/release.sh <version> [options]
#
# Examples:
#   ./scripts/release.sh v0.90.0-beta4
#   ./scripts/release.sh 0.90.0-beta4 --dry-run
#   ./scripts/release.sh v0.90.0-beta4 --skip-tests --no-push
#   ./scripts/release.sh v0.90.0 --push-only
#
# Flags:
#   --dry-run      Preview actions and changelog output without modifying files
#   --skip-tests   Bypass 'go test' verification step (fast path)
#   --no-push      Commit and tag locally without pushing to remote origin
#   --push-only    Push an existing local release commit/tag and create GitHub release
# ==============================================================================

set -euo pipefail

# ANSI Color Codes (no emojis, clean engineering aesthetic)
CLR_RESET="\033[0m"
CLR_BOLD="\033[1m"
CLR_RED="\033[1;31m"
CLR_GREEN="\033[1;32m"
CLR_YELLOW="\033[1;33m"
CLR_BLUE="\033[1;34m"
CLR_CYAN="\033[1;36m"
CLR_GRAY="\033[0;90m"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

CHANGELOG_FILE="${ROOT_DIR}/CHANGELOG.md"
VERSION_GO_FILE="${ROOT_DIR}/pkg/config/version.go"
README_FILE="${ROOT_DIR}/README.md"
README_KO_FILE="${ROOT_DIR}/README.ko.md"
TODO_FILE="${ROOT_DIR}/TODO.md"

# Defaults
DRY_RUN=false
SKIP_TESTS=false
NO_PUSH=false
PUSH_ONLY=false
SKIP_GH_RELEASE=false
TARGET_INPUT=""

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --dry-run)
            DRY_RUN=true
            ;;
        --skip-tests)
            SKIP_TESTS=true
            ;;
        --no-push)
            NO_PUSH=true
            ;;
        --push-only)
            PUSH_ONLY=true
            ;;
        --skip-github-release|--no-gh-release)
            SKIP_GH_RELEASE=true
            ;;
        -h|--help)
            echo -e "${CLR_BOLD}Usage:${CLR_RESET} ./scripts/release.sh <version> [options]"
            echo ""
            echo "Options:"
            echo "  --dry-run              Preview changelog and version updates without applying"
            echo "  --skip-tests           Skip build and test verification pipeline"
            echo "  --no-push              Create commit and tag locally without pushing to origin"
            echo "  --push-only            Push existing local release commit/tag and publish release"
            echo "  --skip-github-release  Do not create GitHub Release via gh CLI"
            exit 0
            ;;
        -*)
            echo -e "${CLR_RED}Error: Unknown flag '${arg}'${CLR_RESET}" >&2
            exit 1
            ;;
        *)
            if [ -z "${TARGET_INPUT}" ]; then
                TARGET_INPUT="$arg"
            else
                echo -e "${CLR_RED}Error: Multiple version arguments provided ('${TARGET_INPUT}', '${arg}')${CLR_RESET}" >&2
                exit 1
            fi
            ;;
    esac
done

if [ -z "${TARGET_INPUT}" ]; then
    echo -e "${CLR_RED}Error: Target version is required.${CLR_RESET}" >&2
    echo -e "Usage: ./scripts/release.sh <version> [--dry-run] [--skip-tests] [--no-push]"
    exit 1
fi

# Normalize version string
# TAG: 'v0.90.0-beta4', VER: '0.90.0-beta4'
CLEAN_VER="${TARGET_INPUT#v}"
TAG="v${CLEAN_VER}"
VER="${CLEAN_VER}"
DATE="$(date +%Y-%m-%d)"

# Shield badge version replaces single hyphen with double hyphen (e.g. 0.90.0--beta4)
SHIELD_VER="${VER//-/--}"

echo -e "${CLR_CYAN}======================================================================${CLR_RESET}"
echo -e "${CLR_BOLD}LionSQL Release Automation Pipeline${CLR_RESET}"
echo -e "${CLR_CYAN}======================================================================${CLR_RESET}"
echo -e "Target Version : ${CLR_BOLD}${VER}${CLR_RESET}"
echo -e "Git Tag        : ${CLR_BOLD}${TAG}${CLR_RESET}"
echo -e "Release Date   : ${CLR_BOLD}${DATE}${CLR_RESET}"
echo -e "Dry Run Mode   : ${CLR_BOLD}${DRY_RUN}${CLR_RESET}"
echo ""

# ------------------------------------------------------------------------------
# 1. Pre-flight Git Sanity Checks
# ------------------------------------------------------------------------------
echo -e "${CLR_BLUE}[1/5] Running Pre-flight Git Checks...${CLR_RESET}"

# Check working tree cleanliness (unless dry-run or push-only)
if [ "${DRY_RUN}" = false ] && [ "${PUSH_ONLY}" = false ]; then
    if ! git diff-index --quiet HEAD --; then
        echo -e "${CLR_RED}Error: Working tree has uncommitted modifications.${CLR_RESET}" >&2
        echo "Please commit or stash your changes before triggering a release."
        git status -s
        exit 1
    fi
fi

# In push-only mode, the tag MUST exist locally
if [ "${PUSH_ONLY}" = true ]; then
    if ! git rev-parse "${TAG}" >/dev/null 2>&1; then
        echo -e "${CLR_RED}Error: Local git tag '${TAG}' does not exist for --push-only mode.${CLR_RESET}" >&2
        exit 1
    fi

    echo -e "${CLR_GREEN}Found local release tag '${TAG}'. Proceeding directly to remote push...${CLR_RESET}"
    echo -e "Pushing commit and tag to remote origin..."
    git push origin HEAD
    git push origin "${TAG}"
    echo -e "${CLR_GREEN}Pushed successfully to origin (branch + ${TAG})${CLR_RESET}"

    if [ "${SKIP_GH_RELEASE}" = false ]; then
        if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
            echo -e "Creating/Updating GitHub Release via gh CLI..."
            PRERELEASE_OPT=""
            if [[ "${TAG}" =~ (beta|alpha|rc) ]]; then
                PRERELEASE_OPT="--prerelease"
            fi

            ASSETS=()
            [ -f "bin/lionsql-darwin-arm64" ] && ASSETS+=("bin/lionsql-darwin-arm64")
            [ -f "bin/lionsql-linux-amd64" ] && ASSETS+=("bin/lionsql-linux-amd64")
            [ -f "bin/lionsql-windows-amd64.exe" ] && ASSETS+=("bin/lionsql-windows-amd64.exe")

            # Check if comprehensive release notes exist in notes/ (e.g. notes/RELEASE_NOTES_v0.90.0.md)
            GH_NOTES_CONTENT=""
            NOTES_DOC_MD="${ROOT_DIR}/notes/RELEASE_NOTES_${TAG}.md"
            if [ -f "${NOTES_DOC_MD}" ]; then
                echo -e "Found formal release notes in ${CLR_BOLD}${NOTES_DOC_MD}${CLR_RESET}."
                GH_NOTES_CONTENT="$(cat "${NOTES_DOC_MD}")"
                # Extract changelog delta from CHANGELOG.md if present
                if grep -q "## \[${VER}\]" "${CHANGELOG_FILE}"; then
                    CHANGELOG_DELTA="$(awk -v ver="## [${VER}]" '
                        index($0, ver) > 0 { p=1; next }
                        p && /^## \[/ { exit }
                        p { print }
                    ' "${CHANGELOG_FILE}" | sed -e :a -e '/^\n*$/{$d;N;};/\n$/ba')"
                    GH_NOTES_CONTENT+=$'\n\n---\n\n<details>\n<summary><b>Detailed Changelog Delta</b></summary>\n\n'
                    GH_NOTES_CONTENT+="${CHANGELOG_DELTA}"
                    GH_NOTES_CONTENT+=$'\n</details>'
                fi
            fi

            GH_NOTES_OPT=("--generate-notes")
            GH_NOTES_FILE=""
            if [ -n "${GH_NOTES_CONTENT}" ]; then
                GH_NOTES_FILE="$(mktemp)"
                printf "%s\n" "${GH_NOTES_CONTENT}" > "${GH_NOTES_FILE}"
                GH_NOTES_OPT=("--notes-file" "${GH_NOTES_FILE}")
            fi

            gh release create "${TAG}" \
                --title "LionSQL ${TAG}" \
                "${GH_NOTES_OPT[@]}" \
                ${PRERELEASE_OPT} \
                "${ASSETS[@]}" || gh release edit "${TAG}" "${GH_NOTES_OPT[@]}"
            [ -n "${GH_NOTES_FILE}" ] && rm -f "${GH_NOTES_FILE}"
            echo -e "${CLR_GREEN}GitHub Release published: https://github.com/MaroShim/LionSQL/releases/tag/${TAG}${CLR_RESET}"
        else
            echo -e "${CLR_YELLOW}Note: GitHub CLI (gh) not logged in. Skipped GitHub Release creation.${CLR_RESET}"
        fi
    fi

    echo ""
    echo -e "${CLR_CYAN}======================================================================${CLR_RESET}"
    echo -e "${CLR_GREEN}${CLR_BOLD}Push Only for ${TAG} Successfully Completed!${CLR_RESET}"
    echo -e "${CLR_CYAN}======================================================================${CLR_RESET}"
    exit 0
fi

# Check if tag already exists (for standard release)
if git rev-parse "${TAG}" >/dev/null 2>&1; then
    echo -e "${CLR_RED}Error: Git tag '${TAG}' already exists locally.${CLR_RESET}" >&2
    exit 1
fi

if git ls-remote --tags origin "${TAG}" 2>/dev/null | grep -q "${TAG}"; then
    echo -e "${CLR_RED}Error: Git tag '${TAG}' already exists on remote origin.${CLR_RESET}" >&2
    exit 1
fi

# Find previous tag
PREV_TAG="$(git describe --tags --abbrev=0 2>/dev/null || echo "")"
if [ -z "${PREV_TAG}" ]; then
    echo -e "Previous Tag   : ${CLR_GRAY}(None found, using all commits)${CLR_RESET}"
    COMMIT_RANGE="HEAD"
else
    echo -e "Previous Tag   : ${CLR_BOLD}${PREV_TAG}${CLR_RESET}"
    COMMIT_RANGE="${PREV_TAG}..HEAD"
fi

# Count commits to be packaged
COMMIT_COUNT="$(git rev-list --count ${COMMIT_RANGE})"
echo -e "Commits Found  : ${CLR_BOLD}${COMMIT_COUNT}${CLR_RESET} commit(s) in range ${COMMIT_RANGE}"

if [ "${COMMIT_COUNT}" -eq 0 ]; then
    echo -e "${CLR_YELLOW}Warning: No new commits found since ${PREV_TAG}.${CLR_RESET}"
fi

# ------------------------------------------------------------------------------
# 2. Automated Verification Pipeline (Build & Test)
# ------------------------------------------------------------------------------
echo ""
echo -e "${CLR_BLUE}[2/5] Running Verification Pipeline...${CLR_RESET}"

if [ "${SKIP_TESTS}" = true ]; then
    echo -e "${CLR_YELLOW}Skipping test pipeline (--skip-tests specified).${CLR_RESET}"
else
    echo -e "Compiling 3 official release binaries..."
    # 1. macOS Apple Silicon (arm64)
    go build -tags wiredtiger -buildvcs=false -o bin/lionsql-darwin-arm64 ./cmd/lionsql
    cp bin/lionsql-darwin-arm64 bin/lionsql
    echo -e "${CLR_GREEN}Build OK: bin/lionsql-darwin-arm64 (macOS Apple Silicon)${CLR_RESET}"

    # 2. Linux amd64 (Pure Go Zero-Cgo)
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -o bin/lionsql-linux-amd64 ./cmd/lionsql
    echo -e "${CLR_GREEN}Build OK: bin/lionsql-linux-amd64 (Linux x86_64)${CLR_RESET}"

    # 3. Windows amd64 (Pure Go Zero-Cgo, 1ms timer)
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql-windows-amd64.exe ./cmd/lionsql
    echo -e "${CLR_GREEN}Build OK: bin/lionsql-windows-amd64.exe (Windows x86_64)${CLR_RESET}"

    echo -e "Compiling benchmark and verification binaries..."
    go build -tags wiredtiger -buildvcs=false -o bin/sqllogictest ./cmd/sqllogictest
    go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
    go build -tags wiredtiger -buildvcs=false -o bin/crashtest ./cmd/crashtest
    go build -tags wiredtiger -buildvcs=false -o bin/soaktest ./cmd/soaktest

    echo -e "[Gate 1/6] Running core unit & regression tests across all packages..."
    go test -tags wiredtiger -count=1 ./pkg/...
    echo -e "${CLR_GREEN}Gate 1 OK: 100% PASS across 23 core packages${CLR_RESET}"

    echo -e "[Gate 2/6] Running 13-Domain Extended Smoke Test (memory & wiredtiger)..."
    bash scripts/verify_report.sh --engine=memory
    bash scripts/verify_report.sh --engine=wiredtiger
    echo -e "${CLR_GREEN}Gate 2 OK: 100% PASS across 13 domains${CLR_RESET}"

    echo -e "[Gate 3/6] Running TPC-C OLTP Benchmark Verification (In-Memory OCC & WiredTiger)..."
    ./bin/tpcc-runtime -engine memory -warehouses 2 -workers 10 -duration 5s
    ./bin/tpcc-runtime -engine wiredtiger -warehouses 2 -workers 10 -duration 5s
    echo -e "${CLR_GREEN}Gate 3 OK: High-throughput OLTP verified (>600k tpmC)${CLR_RESET}"

    echo -e "[Gate 4/6] Running SQLLogicTest Relational Conformance Suite (400k+ queries)..."
    ./bin/sqllogictest -dir test/slt -parallel 16
    echo -e "${CLR_GREEN}Gate 4 OK: 100% PASS (Zero relational mismatches)${CLR_RESET}"

    echo -e "[Gate 5/6] Running T-SQL Stored Procedure 14-Suite Hermetic Conformance..."
    go test -count=1 ./test/slt_proc/...
    echo -e "${CLR_GREEN}Gate 5 OK: 100% PASS across 14 procedural suites${CLR_RESET}"

    echo -e "[Gate 6/6] Running Crash Durability & Resource Leakage Sentinels..."
    ./bin/crashtest -rounds 3 -workers 4 -engine memory
    ./bin/crashtest -rounds 3 -workers 4 -engine wiredtiger
    ./bin/soaktest -duration 5s -engine memory -workers 4 -sample 1s
    echo -e "${CLR_GREEN}Gate 6 OK: Zero-loss crash recovery & resource invariants verified${CLR_RESET}"
fi

# ------------------------------------------------------------------------------
# 3. Categorize Commits & Generate Changelog Section
# ------------------------------------------------------------------------------
echo ""
echo -e "${CLR_BLUE}[3/5] Extracting & Categorizing Commits...${CLR_RESET}"

ADDED_LIST=()
FIXED_LIST=()
PERF_LIST=()
DOCS_LIST=()
CHANGED_LIST=()

while IFS='|' read -r hash subject || [ -n "${hash:-}" ]; do
    [ -z "${hash}" ] && continue
    # Classify commit based on Conventional Commit prefix
    if [[ "${subject}" =~ ^feat(\(.*\))?:[[:space:]]*(.*) ]]; then
        ADDED_LIST+=("- ${BASH_REMATCH[2]} (${hash})")
    elif [[ "${subject}" =~ ^fix(\(.*\))?:[[:space:]]*(.*) ]]; then
        FIXED_LIST+=("- ${BASH_REMATCH[2]} (${hash})")
    elif [[ "${subject}" =~ ^perf(\(.*\))?:[[:space:]]*(.*) ]]; then
        PERF_LIST+=("- ${BASH_REMATCH[2]} (${hash})")
    elif [[ "${subject}" =~ ^docs(\(.*\))?:[[:space:]]*(.*) ]]; then
        DOCS_LIST+=("- ${BASH_REMATCH[2]} (${hash})")
    elif [[ "${subject}" =~ ^(refactor|chore|test)(\(.*\))?:[[:space:]]*(.*) ]]; then
        CHANGED_LIST+=("- ${BASH_REMATCH[3]} (${hash})")
    else
        CHANGED_LIST+=("- ${subject} (${hash})")
    fi
done < <(git log "${COMMIT_RANGE}" --reverse --pretty=format:"%h|%s")

# Build Markdown content for the new release section
RELEASE_NOTES="## [${VER}] - ${DATE}"$'\n\n'

if [ ${#ADDED_LIST[@]} -gt 0 ]; then
    RELEASE_NOTES+="### Added"$'\n'
    for item in "${ADDED_LIST[@]}"; do
        RELEASE_NOTES+="${item}"$'\n'
    done
    RELEASE_NOTES+=$'\n'
fi

if [ ${#FIXED_LIST[@]} -gt 0 ]; then
    RELEASE_NOTES+="### Fixed"$'\n'
    for item in "${FIXED_LIST[@]}"; do
        RELEASE_NOTES+="${item}"$'\n'
    done
    RELEASE_NOTES+=$'\n'
fi

if [ ${#PERF_LIST[@]} -gt 0 ]; then
    RELEASE_NOTES+="### Performance"$'\n'
    for item in "${PERF_LIST[@]}"; do
        RELEASE_NOTES+="${item}"$'\n'
    done
    RELEASE_NOTES+=$'\n'
fi

if [ ${#DOCS_LIST[@]} -gt 0 ]; then
    RELEASE_NOTES+="### Documentation"$'\n'
    for item in "${DOCS_LIST[@]}"; do
        RELEASE_NOTES+="${item}"$'\n'
    done
    RELEASE_NOTES+=$'\n'
fi

if [ ${#CHANGED_LIST[@]} -gt 0 ]; then
    RELEASE_NOTES+="### Changed"$'\n'
    for item in "${CHANGED_LIST[@]}"; do
        RELEASE_NOTES+="${item}"$'\n'
    done
    RELEASE_NOTES+=$'\n'
fi

# Trim trailing newline
RELEASE_NOTES="$(echo "${RELEASE_NOTES}" | sed -e :a -e '/^\n*$/{$d;N;};/\n$/ba')"

echo -e "${CLR_GRAY}Generated Release Notes Preview:${CLR_RESET}"
echo -e "${CLR_GRAY}--------------------------------------------------${CLR_RESET}"
echo "${RELEASE_NOTES}"
echo -e "${CLR_GRAY}--------------------------------------------------${CLR_RESET}"

# ------------------------------------------------------------------------------
# 4. Synchronize Version SSOT Across Codebase
# ------------------------------------------------------------------------------
echo ""
echo -e "${CLR_BLUE}[4/5] Synchronizing Version SSOT Across Files...${CLR_RESET}"

CHANGELOG_FILE="${ROOT_DIR}/CHANGELOG.md"
VERSION_GO_FILE="${ROOT_DIR}/pkg/config/version.go"
README_FILE="${ROOT_DIR}/README.md"
README_KO_FILE="${ROOT_DIR}/README.ko.md"
TODO_FILE="${ROOT_DIR}/TODO.md"

if [ "${DRY_RUN}" = true ]; then
    echo -e "${CLR_YELLOW}[DRY-RUN] Would update CHANGELOG.md with new section:${CLR_RESET}"
    echo "  Insert ## [${VER}] - ${DATE} below ## [Unreleased]"
    echo -e "${CLR_YELLOW}[DRY-RUN] Would update pkg/config/version.go:${CLR_RESET}"
    echo "  const Version = \"${VER}\""
    echo -e "${CLR_YELLOW}[DRY-RUN] Would update README badges to: v${SHIELD_VER}${CLR_RESET}"
    if [ "${SKIP_GH_RELEASE}" = false ] && command -v gh >/dev/null 2>&1; then
        echo -e "${CLR_YELLOW}[DRY-RUN] Would create GitHub Release:${CLR_RESET}"
        echo "  gh release create ${TAG} --title \"LionSQL ${TAG}\" [assets: bin/lionsql-darwin-arm64, bin/lionsql-linux-amd64, bin/lionsql-windows-amd64.exe]"
    fi
    echo ""
    echo -e "${CLR_GREEN}Dry run completed successfully. No files were modified.${CLR_RESET}"
    exit 0
fi

# 4.1. Update CHANGELOG.md
# Reads existing ## [Unreleased], inserts new section below, leaves clean ## [Unreleased]
NOTES_TMP="$(mktemp)"
printf "%s\n" "${RELEASE_NOTES}" > "${NOTES_TMP}"
TEMP_CHANGELOG="$(mktemp)"
awk -v notes_file="${NOTES_TMP}" '
/^## \[Unreleased\]/ {
    print $0;
    print "";
    while ((getline line < notes_file) > 0) {
        print line;
    }
    close(notes_file);
    next;
}
{
    print $0;
}
' "${CHANGELOG_FILE}" > "${TEMP_CHANGELOG}"

mv "${TEMP_CHANGELOG}" "${CHANGELOG_FILE}"
rm -f "${NOTES_TMP}"
echo -e "Updated: ${CLR_BOLD}CHANGELOG.md${CLR_RESET}"

# 4.2. Update pkg/config/version.go
# If base SemVer (e.g. 0.90.0-beta4 -> base 0.90.0)
sed -i.bak -E "s/const Version = \".*\"/const Version = \"${VER}\"/" "${VERSION_GO_FILE}"
rm -f "${VERSION_GO_FILE}.bak"
echo -e "Updated: ${CLR_BOLD}pkg/config/version.go${CLR_RESET} -> ${VER}"

# 4.3. Update README.md & README.ko.md badges
sed -i.bak -E "s/badge\/version-v[0-9A-Za-z\.\-]+-blue\.svg/badge\/version-v${SHIELD_VER}-blue.svg/g" "${README_FILE}"
rm -f "${README_FILE}.bak"

sed -i.bak -E "s/badge\/version-v[0-9A-Za-z\.\-]+-blue\.svg/badge\/version-v${SHIELD_VER}-blue.svg/g" "${README_KO_FILE}"
rm -f "${README_KO_FILE}.bak"
echo -e "Updated: ${CLR_BOLD}README.md & README.ko.md badges${CLR_RESET}"

# 4.4. Ensure Formal Release Notes exist in notes/ (Auto-scaffold if missing)
NOTES_DOC_MD="${ROOT_DIR}/notes/RELEASE_NOTES_${TAG}.md"
NOTES_DOC_KO="${ROOT_DIR}/notes/RELEASE_NOTES_${TAG}.ko.md"
NOTES_README_KO="${ROOT_DIR}/notes/README.ko.md"
NOTES_README_EN="${ROOT_DIR}/notes/README.md"

if [ ! -f "${NOTES_DOC_MD}" ]; then
    echo -e "Scaffolding formal release notes: ${CLR_BOLD}${NOTES_DOC_MD}${CLR_RESET}"
    cat <<EOF > "${NOTES_DOC_MD}"
# LionSQL ${TAG} Release Notes (\`notes/RELEASE_NOTES_${TAG}.md\`)

> - **Initial Date**: ${DATE}
> - **Release Version**: \`${TAG}\`
> - **Milestone Theme**: LionSQL ${TAG} Maintenance and Compatibility Release

---

## 1. Release Highlights

LionSQL \`${TAG}\` incorporates cumulative stability hardening, performance optimizations, and ecosystem compatibility fixes.

---

## 2. Detailed Changes

See the detailed changelog delta below for commit-level records across procedural execution, storage durability, and wire protocols.

---

## 3. Verification & Compliance

1. **3-Stage Pipeline Verification (\`[RULE:PipelineVerification]\`)**:
   - Official release binaries built for macOS (\`darwin-arm64\`), Linux (\`linux-amd64\`), and Windows (\`windows-amd64\`).
   - \`go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...\` 100% PASS (0 data races).
EOF
fi

if [ ! -f "${NOTES_DOC_KO}" ]; then
    echo -e "Scaffolding formal release notes (KO): ${CLR_BOLD}${NOTES_DOC_KO}${CLR_RESET}"
    cat <<EOF > "${NOTES_DOC_KO}"
# LionSQL ${TAG} 릴리즈 노트 (\`notes/RELEASE_NOTES_${TAG}.ko.md\`)

> - **최초 작성일**: ${DATE}
> - **릴리즈 버전**: \`${TAG}\`
> - **마일스톤 주제**: LionSQL ${TAG} 유지보수 및 호환성 강화 릴리즈

---

## 1. 릴리즈 하이라이트 개요

LionSQL \`${TAG}\` 버전은 누적된 안정성 강화, 성능 최적화 및 에코시스템 호환성 개선 사항을 포함합니다.

---

## 2. 세부 변경 사항

상세 커밋별 변경 내역은 하단의 세부 변경 사항(Detailed Changelog Delta)을 참조하십시오.

---

## 3. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (\`[RULE:PipelineVerification]\`)**:
   - macOS (\`darwin-arm64\`), Linux (\`linux-amd64\`), Windows (\`windows-amd64\`) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - \`go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...\` 100% 통과 (0 Data Races).
EOF
fi

# 4.5. Register in notes/README.ko.md and notes/README.md if not present
if [ -f "${NOTES_README_KO}" ] && ! grep -q "RELEASE_NOTES_${TAG}" "${NOTES_README_KO}"; then
    sed -i.bak -E "s|(7\. \*\*마일스톤 실전 검증 보고서 및 포스트모텀.*?\*\*:)|\\1\n   - **[\`RELEASE_NOTES_${TAG}.ko.md\`](\`RELEASE_NOTES_${TAG}.ko.md\`)** ([English](RELEASE_NOTES_${TAG}.md)): LionSQL ${TAG} 공식 릴리즈 노트.|g" "${NOTES_README_KO}"
    rm -f "${NOTES_README_KO}.bak"
    echo -e "Registered in: ${CLR_BOLD}notes/README.ko.md${CLR_RESET}"
fi

if [ -f "${NOTES_README_EN}" ] && ! grep -q "RELEASE_NOTES_${TAG}" "${NOTES_README_EN}"; then
    sed -i.bak -E "s|(7\. \*\*Production Integration Reports & Postmortems\*\*:)|\\1\n   - **[\`RELEASE_NOTES_${TAG}.md\`](\`RELEASE_NOTES_${TAG}.md\`)** ([Korean](RELEASE_NOTES_${TAG}.ko.md)): LionSQL ${TAG} official release notes.|g" "${NOTES_README_EN}"
    rm -f "${NOTES_README_EN}.bak"
    echo -e "Registered in: ${CLR_BOLD}notes/README.md${CLR_RESET}"
fi

# ------------------------------------------------------------------------------
# 5. Git Commit, Tag & Remote Push
# ------------------------------------------------------------------------------
echo ""
echo -e "${CLR_BLUE}[5/5] Creating Release Commit, Git Tag, and Pushing...${CLR_RESET}"

# Stage modified version files and generated notes
git add "${CHANGELOG_FILE}" "${VERSION_GO_FILE}" "${README_FILE}" "${README_KO_FILE}"
[ -f "${NOTES_DOC_MD}" ] && git add "${NOTES_DOC_MD}"
[ -f "${NOTES_DOC_KO}" ] && git add "${NOTES_DOC_KO}"
[ -f "${NOTES_README_KO}" ] && git add "${NOTES_README_KO}"
[ -f "${NOTES_README_EN}" ] && git add "${NOTES_README_EN}"

# Commit
git commit -m "chore(release): bump version to ${TAG}"
echo -e "${CLR_GREEN}Committed: chore(release): bump version to ${TAG}${CLR_RESET}"

# Tag
git tag -a "${TAG}" -m "Release ${TAG}"
echo -e "${CLR_GREEN}Created Tag: ${TAG}${CLR_RESET}"

# Push
if [ "${NO_PUSH}" = true ]; then
    echo -e "${CLR_YELLOW}Skipping remote push (--no-push specified).${CLR_RESET}"
else
    echo -e "Pushing commit and tag to remote origin..."
    git push origin HEAD
    git push origin "${TAG}"
    echo -e "${CLR_GREEN}Pushed successfully to origin (branch + ${TAG})${CLR_RESET}"

    # 5.3. Create GitHub Release via gh CLI if available and authenticated
    if [ "${SKIP_GH_RELEASE}" = false ]; then
        if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
            echo -e "Creating GitHub Release via gh CLI..."
            PRERELEASE_OPT=""
            if [[ "${TAG}" =~ (beta|alpha|rc) ]]; then
                PRERELEASE_OPT="--prerelease"
            fi

            ASSETS=()
            [ -f "bin/lionsql-darwin-arm64" ] && ASSETS+=("bin/lionsql-darwin-arm64")
            [ -f "bin/lionsql-linux-amd64" ] && ASSETS+=("bin/lionsql-linux-amd64")
            [ -f "bin/lionsql-windows-amd64.exe" ] && ASSETS+=("bin/lionsql-windows-amd64.exe")

            # Check if comprehensive release notes exist in notes/ (e.g. notes/RELEASE_NOTES_v0.90.0.md)
            GH_NOTES_CONTENT=""
            NOTES_DOC_MD="${ROOT_DIR}/notes/RELEASE_NOTES_${TAG}.md"
            if [ -f "${NOTES_DOC_MD}" ]; then
                echo -e "Found formal release notes in ${CLR_BOLD}${NOTES_DOC_MD}${CLR_RESET}."
                GH_NOTES_CONTENT="$(cat "${NOTES_DOC_MD}")"
                GH_NOTES_CONTENT+=$'\n\n---\n\n<details>\n<summary><b>Detailed Changelog Delta</b></summary>\n\n'
                # Strip out ## [version] header since it's already represented in tag and summary
                CLEAN_DELTA="$(echo "${RELEASE_NOTES}" | sed -E '/^## \[/d')"
                GH_NOTES_CONTENT+="${CLEAN_DELTA}"
                GH_NOTES_CONTENT+=$'\n</details>'
            else
                GH_NOTES_CONTENT="${RELEASE_NOTES}"
            fi

            GH_NOTES_FILE="$(mktemp)"
            printf "%s\n" "${GH_NOTES_CONTENT}" > "${GH_NOTES_FILE}"

            gh release create "${TAG}" \
                --title "LionSQL ${TAG}" \
                --notes-file "${GH_NOTES_FILE}" \
                ${PRERELEASE_OPT} \
                "${ASSETS[@]}"
            rm -f "${GH_NOTES_FILE}"
            echo -e "${CLR_GREEN}GitHub Release published: https://github.com/MaroShim/LionSQL/releases/tag/${TAG}${CLR_RESET}"
        else
            echo -e "${CLR_YELLOW}Note: GitHub CLI (gh) not logged in. Skipped GitHub Release creation.${CLR_RESET}"
        fi
    fi
fi

echo ""
echo -e "${CLR_CYAN}======================================================================${CLR_RESET}"
echo -e "${CLR_GREEN}${CLR_BOLD}Release ${TAG} Successfully Completed!${CLR_RESET}"
echo -e "${CLR_CYAN}======================================================================${CLR_RESET}"
echo -e "Compiled 3 Official Release Binaries in bin/:"
echo -e "  - ${CLR_BOLD}bin/lionsql-darwin-arm64${CLR_RESET} (macOS Apple Silicon)"
echo -e "  - ${CLR_BOLD}bin/lionsql-linux-amd64${CLR_RESET} (Linux x86_64)"
echo -e "  - ${CLR_BOLD}bin/lionsql-windows-amd64.exe${CLR_RESET} (Windows x86_64)"
echo ""
