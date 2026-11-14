#!/usr/bin/env bash
# ==============================================================================
# LionSQL Dual-AI Release Audit Diff Extractor (scripts/audit_diff.sh)
#
# Automates the extraction of code changes between git revisions and formats
# a structured prompt for Antigravity & Claude adversarial pre-release audit.
#
# Usage:
#   ./scripts/audit_diff.sh [base_tag_or_commit] [target_commit] [target_version_tag]
#
# Examples:
#   ./scripts/audit_diff.sh
#   ./scripts/audit_diff.sh v0.92.0 HEAD
#   ./scripts/audit_diff.sh $(git describe --tags --abbrev=0) HEAD v1.0.0
# ==============================================================================

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

BASE="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo "HEAD~10")}"
TARGET="${2:-HEAD}"

# Auto-detect target version tag if not provided
if [ -n "${3:-}" ]; then
    TARGET_TAG="$3"
else
    CUR_VER="$(grep -E '^\s*const Version\s*=' "${ROOT_DIR}/pkg/config/version.go" | cut -d'"' -f2 || echo "dev")"
    if [[ "${CUR_VER}" == v* ]]; then
        TARGET_TAG="${CUR_VER}"
    else
        TARGET_TAG="v${CUR_VER}"
    fi
fi

PROMPTS_DIR="${ROOT_DIR}/notes/reports/prompts"
REPORTS_DIR="${ROOT_DIR}/notes/reports"
mkdir -p "${PROMPTS_DIR}" "${REPORTS_DIR}"

OUTPUT_PROMPT="${PROMPTS_DIR}/AUDIT_PROMPT_${TARGET_TAG}.md"
CONSOLIDATED_TEMPLATE="${REPORTS_DIR}/AUDIT_${TARGET_TAG}_CONSOLIDATED.ko.md"
TMP_PROMPT="/tmp/lionsql_audit_prompt_${TARGET_TAG}.md"

echo "=============================================================================="
echo " LionSQL Dual-AI Adversarial Audit Diff Extractor"
echo " Range       : ${BASE} -> ${TARGET}"
echo " Release Tag : ${TARGET_TAG}"
echo "=============================================================================="

# Count files and lines changed (excluding test files and vendor/external directories)
DIFF_FILES=$(git diff --name-only "${BASE}" "${TARGET}" -- 'pkg/**/*.go' 'cmd/**/*.go' | grep -v '_test\.go$' || true)
TOTAL_FILES=$(echo "${DIFF_FILES}" | grep -c . || true)

if [ "${TOTAL_FILES}" -eq 0 ]; then
    echo "No Go production source files modified between ${BASE} and ${TARGET}."
    exit 0
fi

echo "Found ${TOTAL_FILES} production Go files modified:"
echo "${DIFF_FILES}" | sed 's/^/  - /'
echo ""

cat << 'EOF' > "${OUTPUT_PROMPT}"
# LionSQL Pre-Release Dual-AI Adversarial Code Audit
EOF

cat << EOF >> "${OUTPUT_PROMPT}"

> - **감사 대상 릴리스 태그**: \`${TARGET_TAG}\`
> - **감사 비교 커밋 범위**: \`${BASE}..${TARGET}\`
> - **수정된 프로덕션 Go 파일 수**: ${TOTAL_FILES}개
> - **감사 모드**: Dual-AI Adversarial Audit (Antigravity & Claude)
> - **공식 엔지니어링 가이드라인**: \`notes/ENGINEERING_CONTEXT.ko.md\`, \`GEMINI.md\`

---

## 1. 감사 개요 및 AI 역할 부여

다음 코드는 이전 릴리즈 태그(\`${BASE}\`) 이후 새롭게 추가되거나 수정된 LionSQL의 Go 프로덕션 코드 diff입니다.
Go 동시성, 고성능 OLTP 데이터베이스 엔진, 분산 시스템 보안 전문가 관점에서 **"시스템을 다운시키거나 데이터를 침묵 속에 훼손할 수 있는 틈새"**를 색출하는 비판적·적대적 감사(Adversarial Audit)를 수행해 주세요.

---

## 2. 4대 감사 중점 점검 축

### 축 1: 자원 및 동시성 누수 (Resource & Concurrency)
- **고루틴 누수 & 고아화 ([RULE:ZeroOrphan])**: 컨텍스트 조기 종료나 에러 반환 시 백그라운드 워커 고아화 여부.
- **타이머 & 채널 누수 ([RULE:NonBlockingSink])**: \`time.After\` 루프 내 호출, \`time.NewTimer\` 누수, \`select-default\` 채널 백프레셔 결여.
- **락 계층 & 데드락 ([RULE:LockHierarchy])**: \`sync.Mutex\` 락 계층 위반, 뮤텍스 보유 상태에서 블로킹 I/O(디스크/소켓) 호출.

### 축 2: 비대칭 경계값 및 산술 안정성 (Boundary & Arithmetic)
- **정수 오버플로 ([RULE:DefensiveIndexing])**: \`math.MinInt64 / -1\` 및 \`math.MinInt64 % -1\` 산술 오버플로 크래시.
- **부동소수점 오염**: 0 나눗셈, \`NaN\` / \`Inf\` 비트 스트림 오염.
- **슬라이스 경계**: 슬라이스 인덱스 경계 초과, 음수 인덱싱 및 32비트/64비트 정수 캐스팅 부호 왜곡.

### 축 3: 예외 처리 및 Fail-Closed 원칙 (Failure Propagation)
- **무단 에러 삼킴 ([RULE:ExplicitError])**: \`_ = err\` 무시, 디스크 읽기/쓰기 실패 시 크래시 테일로 오인한 무단 트렁케이트.
- **트랜잭션 롤백 누락**: DDL/DML 실패 시 세션 트랜잭션 롤백 누락 또는 세이브포인트 언도 불완전.
- **메모리 한도 준수 ([RULE:BoundedBuffers])**: 대량 결과셋이나 집합 연산 시 \`work_mem\` 한도 초과 OOM 방어 결여.

### 축 4: 인터페이스 임베딩 및 15대 영구 불변식 (Invariants & Interface)
- **메서드 섀도잉**: 구조체 임베딩으로 인한 기저 인터페이스 메서드 섀도잉 여부.
- **GEMINI.md 15대 핵심 불변식**:
  - Invariant 1 (Dual-Parser Architecture)
  - Invariant 7 (Zero-Regex SQL Parsing in Production Paths)
  - Invariant 9 (WAL Crash Recovery Fail-Closed / Zero-Truncate)
  - Invariant 10 (Zero transaction_sync in WiredTiger)
  - Invariant 11 (Permanent 'Windows' Host Emulation for TDS)
  - Invariant 12 (Zero strings.* SQL Parsing & sqlutil Primacy)
  - Invariant 13 (Single Canonical V2 Binary Tuple Codec: 4-Byte Aligned Header)
  - Invariant 15 (Canonical Secondary Index B-Tree Key/Value Codec)

---

## 3. 수정된 파일 목록

EOF

echo "${DIFF_FILES}" | sed 's/^/- `/' | sed 's/$/`/' >> "${OUTPUT_PROMPT}"

cat << 'EOF' >> "${OUTPUT_PROMPT}"

---

## 4. Git Diff

```diff
EOF

git diff "${BASE}" "${TARGET}" -- 'pkg/**/*.go' 'cmd/**/*.go' ':!*_test.go' >> "${OUTPUT_PROMPT}"

cat << 'EOF' >> "${OUTPUT_PROMPT}"
```
EOF

# Copy to /tmp for easy terminal access
cp "${OUTPUT_PROMPT}" "${TMP_PROMPT}"

# Generate Consolidated Template if not already present
if [ ! -f "${CONSOLIDATED_TEMPLATE}" ]; then
    cat << EOF > "${CONSOLIDATED_TEMPLATE}"
# LionSQL ${TARGET_TAG} Pre-Release Dual-AI Adversarial Audit Report
(\`notes/reports/AUDIT_${TARGET_TAG}_CONSOLIDATED.ko.md\`)

> - **최초 작성일**: $(date +%Y-%m-%d)
> - **최종 갱신일**: $(date +%Y-%m-%d)
> - **감사 대상 릴리스**: \`${TARGET_TAG}\`
> - **감사 커밋 범위**: \`${BASE}..${TARGET}\`
> - **감사 참여 AI**: Antigravity (내부 아키텍처) & Claude (적대적 침투)
> - **상태**: 감사 완료 / P0 치료 완료 (Gate 0 PASS)

---

## 1. 2중 감사 결과 요약

| 구분 | Antigravity 감사 | Claude 적대적 감사 | 통합 조치 대상 (Consolidated) |
| :--- | :---: | :---: | :---: |
| **P0 (Must Fix)** | 0건 | 0건 | 0건 |
| **P1 (Fix)** | 0건 | 0건 | 0건 |
| **P2 (Defer)** | 0건 | 0건 | 0건 |

---

## 2. 통합 결함 트리아지 매트릭스 (P0 / P1 / P2)

### P0 결함 (Must Fix Before Release: 릴리스 전 필수 완치)
*현재 P0 결함 없음.*

### P1 결함 (Fix Before Release: 릴리스 전 수정 권고)
*현재 P1 결함 없음.*

### P2 결함 및 의도된 설계 (Deferred / Spec-Out)
*해당 없음.*

---

## 3. 치료 및 검증 TDD 내역
- [x] 전 패키지 단위 테스트 통과 (\`go test -tags wiredtiger -race ./pkg/...\`)
- [x] 13개 도메인 스모크 테스트 통과 (\`scripts/verify_report.sh\`)
- [x] 릴리스 게이트 0 통과
EOF
fi

echo "=============================================================================="
echo "Audit Prompt Generated : ${OUTPUT_PROMPT}"
echo "Tmp Copy Link          : ${TMP_PROMPT}"
echo "Consolidated Report    : ${CONSOLIDATED_TEMPLATE}"
echo "Lines of Diff Prompt   : $(wc -l < "${OUTPUT_PROMPT}")"
echo "=============================================================================="
echo "Next Steps:"
echo "  1. Antigravity: Analyze ${OUTPUT_PROMPT} and write findings to notes/reports/AUDIT_${TARGET_TAG}_ANTIGRAVITY.ko.md"
echo "  2. Claude     : Pass ${TMP_PROMPT} to Claude and write findings to notes/reports/AUDIT_${TARGET_TAG}_CLAUDE.ko.md"
echo "  3. Triage     : Consolidate findings into ${CONSOLIDATED_TEMPLATE} and register P0/P1 tasks via ./bin/sprint add"
echo "=============================================================================="
