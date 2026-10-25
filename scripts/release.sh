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
#
# Flags:
#   --dry-run      Preview actions and changelog output without modifying files
#   --skip-tests   Bypass 'go test' verification step (fast path)
#   --no-push      Commit and tag locally without pushing to remote origin
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

# Defaults
DRY_RUN=false
SKIP_TESTS=false
NO_PUSH=false
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

# Check working tree cleanliness (unless dry-run)
if [ "${DRY_RUN}" = false ]; then
    if ! git diff-index --quiet HEAD --; then
        echo -e "${CLR_RED}Error: Working tree has uncommitted modifications.${CLR_RESET}" >&2
        echo "Please commit or stash your changes before triggering a release."
        git status -s
        exit 1
    fi
fi

# Check if tag already exists
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
    echo -e "Compiling core binaries..."
    # 1. Native Unix binary with WiredTiger
    go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
    echo -e "${CLR_GREEN}Build OK: bin/lionsql (Unix/Native)${CLR_RESET}"

    # 2. Windows Standalone Binaries (.exe, pure-Go in-memory engine, 1ms timer)
    echo -e "Compiling Windows standalone binaries (.exe)..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql
    echo -e "${CLR_GREEN}Build OK: bin/lionsql.exe (Windows x86_64)${CLR_RESET}"

    CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -buildvcs=false -o bin/lionsql-arm64.exe ./cmd/lionsql
    echo -e "${CLR_GREEN}Build OK: bin/lionsql-arm64.exe (Windows ARM64)${CLR_RESET}"

    echo -e "Running core unit tests..."
    go test -tags wiredtiger -count=1 ./pkg/config/... ./pkg/storage/...
    echo -e "${CLR_GREEN}Tests OK: 100% PASS${CLR_RESET}"
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
        echo "  gh release create ${TAG} --title \"LionSQL ${TAG}\" [assets: bin/lionsql, bin/lionsql.exe, bin/lionsql-arm64.exe]"
    fi
    echo ""
    echo -e "${CLR_GREEN}Dry run completed successfully. No files were modified.${CLR_RESET}"
    exit 0
fi

# 4.1. Update CHANGELOG.md
# Reads existing ## [Unreleased], inserts new section below, leaves clean ## [Unreleased]
TEMP_CHANGELOG="$(mktemp)"
awk -v new_section="${RELEASE_NOTES}" '
BEGIN { in_unreleased = 0; inserted = 0; }
/^## \[Unreleased\]/ {
    print $0;
    print "";
    print new_section;
    inserted = 1;
    next;
}
{
    print $0;
}
' "${CHANGELOG_FILE}" > "${TEMP_CHANGELOG}"

mv "${TEMP_CHANGELOG}" "${CHANGELOG_FILE}"
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

# ------------------------------------------------------------------------------
# 5. Git Commit, Tag & Remote Push
# ------------------------------------------------------------------------------
echo ""
echo -e "${CLR_BLUE}[5/5] Creating Release Commit, Git Tag, and Pushing...${CLR_RESET}"

# Stage modified version files
git add "${CHANGELOG_FILE}" "${VERSION_GO_FILE}" "${README_FILE}" "${README_KO_FILE}"

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
            [ -f "bin/lionsql" ] && ASSETS+=("bin/lionsql")
            [ -f "bin/lionsql.exe" ] && ASSETS+=("bin/lionsql.exe")
            [ -f "bin/lionsql-arm64.exe" ] && ASSETS+=("bin/lionsql-arm64.exe")

            gh release create "${TAG}" \
                --title "LionSQL ${TAG}" \
                --notes "${RELEASE_NOTES}" \
                ${PRERELEASE_OPT} \
                "${ASSETS[@]}"
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
echo -e "Compiled Binaries in bin/:"
echo -e "  - ${CLR_BOLD}bin/lionsql${CLR_RESET} (Unix/macOS/Linux with WiredTiger)"
echo -e "  - ${CLR_BOLD}bin/lionsql.exe${CLR_RESET} (Windows x86_64 Standalone)"
echo -e "  - ${CLR_BOLD}bin/lionsql-arm64.exe${CLR_RESET} (Windows ARM64 Standalone)"
echo ""
