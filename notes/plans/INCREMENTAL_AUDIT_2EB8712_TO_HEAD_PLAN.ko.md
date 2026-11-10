# 증분 듀얼 AI (Antigravity × Claude) 감사 보고서 (`2eb8712` ➔ `HEAD`)

> - **최초 작성일**: 2026-11-10
> - **감사 기준선 (Base)**: `2eb8712` (P1 감사 패치 완결 커밋)
> - **감사 대상선 (Target)**: `18cbb31` (P2 그룹 A/B 가드 반영 커밋) 및 후속 검증 완료점
> - **감사 범위 커밋**:
>   1. `d7435c8`: docs: update CLI output to v0.92.0 and synchronize benchmark metrics with doc/BENCHMARK.md
>   2. `2e60b63`: feat(proc,engine): support legacy comparison operators (!<, !>) and quantified operators (ALL, ANY, SOME)
>   3. `acf9ab3`: feat(proc,engine): support T-SQL table variables (DECLARE @tbl TABLE) in stored procedures and batches
>   4. `18cbb31`: fix(storage,proc,engine): apply Group A and B edge-case guards and T-SQL parity fixes
> - **상태**: 감사 및 패치 완결 (`AUDIT_PASSED`)

---

## 1. 개요 및 변경 범위

P1 전수 감사 완료 커밋(`2eb8712`) 이후 새롭게 추가/수정된 26개 프로덕션 Go 파일(총 2,221 diff 라인)에 대해 듀얼 AI(Antigravity 심층 추론 × Claude 적대적 감사)를 수행하였다.

### 감사 대상 26개 파일 목록
- `pkg/engine/coordinator_impl.go`
- `pkg/engine/evaluator.go`
- `pkg/engine/sqlexec_eval.go`
- `pkg/engine/sqlexec_explain.go`
- `pkg/engine/sqlexec_procs.go`
- `pkg/execution/governor.go`
- `pkg/proc/ast.go`
- `pkg/proc/builtins_math.go`
- `pkg/proc/builtins_string.go`
- `pkg/proc/builtins_sys.go`
- `pkg/proc/bytecode.go`
- `pkg/proc/compiler.go`
- `pkg/proc/eval.go`
- `pkg/proc/lexer.go`
- `pkg/proc/parser_ddl.go`
- `pkg/proc/parser_dml.go`
- `pkg/proc/parser_expr.go`
- `pkg/proc/parser_stmt.go`
- `pkg/proc/runtime/vm.go`
- `pkg/proc/runtime/vm_dispatcher.go`
- `pkg/proc/token.go`
- `pkg/sqlutil/params.go`
- `pkg/sqlutil/predicate.go`
- `pkg/sqlutil/tokenizer.go`
- `pkg/storage/engine.go`
- `pkg/storage/types_extended.go`

---

## 2. 4대 핵심 축 감사 결과 및 조치

### 1) 자원 및 동시성 누수 (Resource & Concurrency)
- **발견 사항**: `OpDeclareTable` 실행 시 단일 프레임에서 동일 테이블 변수명이 재선언될 경우, 기존 물리 테이블(`__tv_*`)이 덮어쓰여 프레임 종료까지 스토리지에 잔류할 수 있는 문제 확인.
- **조치**: 신규 테이블 변수 생성 시 기존 매핑이 존재하면 선제적 `DropTable(oldPhys)`을 호출하도록 보강 완료.
- **판정**: PASS.

### 2) 비대칭 경계값 및 산술 안정성 (Boundary & Arithmetic)
- `MOD(MinInt64, -1)`, `MinInt64 / -1` 정수 오버플로우 패닉 방지 가드 검증 완료.
- `FastRandN` 비부호 정수 변환 및 음수 모듈로 차단 검증 완료.
- `ParseMemSize` 초대형 바이트 입력 시 `MaxInt64` 클램핑 및 음수 방어 검증 완료.
- `FindMatchingParen` 음수 깊이 스캔 즉시 탈출 검증 완료.
- **판정**: PASS.

### 3) 예외 처리 및 SQL 3VL 정합성 (Failure Propagation & 3VL)
- **발견 사항**: `sqlexec_explain.go`의 `col > ALL (SELECT ...)` 서브쿼리 최적화에서 서브쿼리 결과에 `NULL`이 포함된 경우 SQL 표준 3VL(Three-Valued Logic)에 따라 조건이 UNKNOWN(False)이 되어야 하나, 최대값 비교로 누락될 수 있는 엣지 케이스 확인.
- **조치**: `hasNullVal` 감지 시 `kw == "ALL"`에 대해 `1=0`으로 안전하게 치환하도록 보강 완료.
- **판정**: PASS.

### 4) 인터페이스 및 아키텍처 불변식 (Invariants & Interface)
- Zero-Regex SQL 파싱 불변식 준수: `params.go` 상태머신 토크나이저 활용.
- In-Memory OCC & WiredTiger 락 계층 준수.
- Windows Host Platform Parity 불변식 준수.
- **판정**: PASS.

---

## 3. 검증 결과

1. **테스트 파이프라인**:
   - `go test -tags wiredtiger -race ./pkg/storage/... ./pkg/proc/... ./pkg/engine/... ./pkg/execution/... ./pkg/sqlutil/...`: **100% PASS (0 data race)**
2. **바이너리 빌드**:
   - `bin/lionsql`, `bin/bench`, `bin/tpcc-runtime` 정상 컴파일 완료.
3. **기록 보존**:
   - 본 문서는 `2eb8712`부터 현재 HEAD까지의 감사 완료 기준선으로 영구 보존된다.
