#!/usr/bin/env bash
# ==============================================================================
# LionSQL Dual-AI Release Audit Diff Extractor (scripts/audit_diff.sh)
#
# Automates the extraction of code changes between git revisions and formats
# a structured prompt for Claude adversarial audit.
#
# Usage:
#   ./scripts/audit_diff.sh [base_tag_or_commit] [target_commit]
#
# Examples:
#   ./scripts/audit_diff.sh v0.92.0 HEAD
#   ./scripts/audit_diff.sh $(git describe --tags --abbrev=0) HEAD
# ==============================================================================

set -euo pipefail

BASE="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo "HEAD~10")}"
TARGET="${2:-HEAD}"

echo "=============================================================================="
echo " LionSQL Dual-AI Adversarial Audit Diff Extractor"
echo " Range: ${BASE} -> ${TARGET}"
echo "=============================================================================="

# Count files and lines changed
DIFF_FILES=$(git diff --name-only "${BASE}" "${TARGET}" -- 'pkg/**/*.go' 'cmd/**/*.go' | grep -v '_test\.go$' || true)
TOTAL_FILES=$(echo "${DIFF_FILES}" | grep -c . || true)

if [ "${TOTAL_FILES}" -eq 0 ]; then
    echo "No Go production source files modified between ${BASE} and ${TARGET}."
    exit 0
fi

echo "Found ${TOTAL_FILES} production Go files modified:"
echo "${DIFF_FILES}" | sed 's/^/  - /'
echo ""

OUTPUT_PROMPT="/tmp/lionsql_audit_prompt_${BASE}_${TARGET}.md"

cat << 'EOF' > "${OUTPUT_PROMPT}"
# LionSQL Pre-Release Adversarial Code Audit

다음 코드는 이전 릴리즈 태그 이후 새롭게 추가되거나 수정된 LionSQL의 Go 프로덕션 코드 diff입니다.
Go 동시성, 고성능 OLTP 데이터베이스 엔진, 분산 시스템 보안 전문가 관점에서 아래 4대 핵심 축을 바탕으로 비판적·적대적 감사(Adversarial Audit)를 수행해 주세요.

## 4대 감사 중점 점검 축
1. **자원 및 동시성 누수 (Resource & Concurrency)**
   - 고루틴 누수, 컨텍스트 조기 종료 시 백그라운드 워커 고아화 ([RULE:ZeroOrphan])
   - time.After/time.NewTimer 누수, select-default 채널 백프레셔 결여
   - 락 계층(Lock Hierarchy) 위반, 데드락 위험, 뮤텍스 보유 상태에서 블로킹 I/O 호출
2. **비대칭 경계값 및 산술 안정성 (Boundary & Arithmetic)**
   - math.MinInt64 나눗셈/나머지 연산 오버플로 (-9223372036854775808 / -1 및 % -1)
   - 0 나눗셈, NaN/Inf 부동소수점 오염
   - 슬라이스 인덱스 경계 초과 및 음수 레지스터 인덱싱
3. **예외 처리 및 Fail-Closed 원칙 (Failure Propagation)**
   - 에러 무단 삼킴 (_ = err), 실패 시 트랜잭션/세션 롤백 누락
   - 메모리 한도(work_mem) 초과 시 OOM 방어 및 임시 파일 정리 누락
4. **인터페이스 임베딩 및 영구 불변식 (Invariants & Interface)**
   - 인터페이스 임베딩으로 인한 기저 구조체 메서드 섀도잉 (Method Shadowing)
   - GEMINI.md의 11대 핵심 불변식 위반 여부

## 변경 파일 목록
EOF

echo "${DIFF_FILES}" | sed 's/^/- /' >> "${OUTPUT_PROMPT}"
echo "" >> "${OUTPUT_PROMPT}"
echo "## Git Diff" >> "${OUTPUT_PROMPT}"
echo '```diff' >> "${OUTPUT_PROMPT}"
git diff "${BASE}" "${TARGET}" -- 'pkg/**/*.go' 'cmd/**/*.go' ':!*_test.go' >> "${OUTPUT_PROMPT}"
echo '```' >> "${OUTPUT_PROMPT}"

echo "=============================================================================="
echo "Audit prompt successfully generated at: ${OUTPUT_PROMPT}"
echo "Lines: $(wc -l < "${OUTPUT_PROMPT}")"
echo "You can now pass this file directly to Claude for pre-release adversarial review."
echo "=============================================================================="
