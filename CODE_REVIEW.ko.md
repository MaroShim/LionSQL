# LionSQL 코드 리뷰 및 결함 감사 트래커 (CODE_REVIEW.ko.md)

본 문서는 LionSQL의 전체 코드베이스를 대상으로 진행되는 **정밀 정적 분석 ➔ 이슈 등록 ➔ 회귀 테스트 및 수정 ➔ 3단계 검증(Build & Race-Test & Commit)** 루프의 진행 상황과 백로그를 추적하는 전용 트래커입니다.

---

## 1. 검증 파이프라인 원칙 (3-Stage Invariant)

모든 이슈 수정은 반드시 아래 3단계를 순서대로 통과한 후 커밋 및 반영합니다.
1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **Test (Race-Free Verification)**:
   ```bash
   go test -tags wiredtiger -count=1 -race ./...
   ```
   (전체 패키지 100% PASS 및 데이터 레이스 0건 필수)
3. **Commit**:
   - Conventional Commits 형식 (`fix(engine): ...`, `refactor(...): ...`)
   - 장식용 이모지 사용 금지

---

## 2. 상태 범례

- `[ ]` : 분석 및 등록 완료 (수정 대기)
- `[~]` : 수정 및 테스트 작성 진행 중
- `[x]` : 3단계 검증 완료 및 커밋 완료

---

## 3. 엔진 계층 (`pkg/engine`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-ENG-001** | P1 | 버그 | [`pkg/engine/sqlexec_dml.go:18`](pkg/engine/sqlexec_dml.go#L18) | `reDmlInsertValues` 정규식에서 `\s+INTO\s+` 강제로 인해 `INSERT tbl VALUES (...)` 형태 쿼리 파싱 실패 | `[x]` | 정규식 `(?:\s+INTO)?` 선택사항으로 수정 및 라우터 정규식 연동 완료 (`db57751`) |
| **REV-ENG-002** | P3 | 오류 | [`pkg/engine/sqlexec_dml.go:1248`](pkg/engine/sqlexec_dml.go#L1248) | DELETE 실행 중 PK 부재 시 `identify row for update`로 출력되는 복사-붙여넣기 에러 메시지 오류 | `[x]` | `identify row for delete`로 수정 완료 (`db57751`) |
| **REV-ENG-003** | P2 | 취약점 | [`pkg/engine/sqlexec_dml.go:248-253`](pkg/engine/sqlexec_dml.go#L248-L253) | UPSERT 접두사 처리 시 다중 공백이나 탭(`\t`) 처리 미흡으로 치환 누락 가능성 | `[x]` | `reDmlUpsertPrefix` 정규식 도입 및 ExecuteSQL 라우팅 정규화 완료 (`db57751`) |
| **REV-ENG-004** | P1 | 버그 | [`pkg/engine/sqlexec_eval.go:930-948`](pkg/engine/sqlexec_eval.go#L930-L948) | `evalUpdateExpr` 정수 나눗셈 시 `rightI == 0` 미검사로 인한 `integer divide by zero` 런타임 패닉 위험 | `[x]` | `rightF == 0 \|\| (targetType == TypeInt && rightI == 0)` 검사 및 divide by zero 에러 반환 |
| **REV-ENG-005** | P2 | 정합성 | [`pkg/engine/sqlexec_eval.go:137-145, 821-826`](pkg/engine/sqlexec_eval.go#L137) | `splitLogicalConditions` 및 `findArithmeticOp`의 SQL 연속 작은따옴표 이스케이프(`''`) 처리 누락 | `[x]` | `quoteChar` 연속 출현 시 이스케이프 건너뛰기 로직 보강 |
| **REV-ENG-006** | P2 | 버그 | [`pkg/engine/sqlexec_dml.go:21`](pkg/engine/sqlexec_dml.go#L21) | `UPDATE ... SET col = 'a WHERE b' OUTPUT ... WHERE ...`에서 `reDmlUpdate`가 SET 절을 지연 매칭하여 문자열 리터럴 내부 `WHERE`에서 분할, 에러 없이 0건 갱신 후 성공 반환 (OUTPUT 정규식 경로 한정) | `[x]` | `ARCH-DML-1`에서 정규식 폐기와 함께 해소됨 (`ParseDMLCommand`가 리터럴/괄호를 건너뛰고 최상위 키워드로만 SET/OUTPUT/WHERE 분할). `knownBug` 마커 제거 완료: `TestDMLCharacterization_Update/output_literal_with_keyword_where` |
| **REV-ENG-007** | P1 | 오버스펙 | [`pkg/engine/sqlexec_procs.go:1263-1295`](pkg/engine/sqlexec_procs.go#L1263) | `sp_executesql` 인자 파싱 시 `@stmt`, `@statement` 등 파라미터 이름을 스위치문으로 검사하여 비표준 이름 전송 시 `default`로 빠져 쿼리 텍스트가 유실되는 오버스펙 결함 | `[x]` | 100% 순수 위치 기반(Slot 0 = statement) 매핑으로 단순화 완료 (`57e51aa`) |
| **REV-ENG-008** | P2 | 아키텍처 | [`pkg/engine/sqlexec_select.go`](pkg/engine/sqlexec_select.go) | 단순 SELECT를 위해 자체적인 프로젝션/필터링/정렬을 구현하여 `pkg/execution/` 및 `pkg/proc/runtime/` VDBE와 3갈래 중복 실행 경로 유지 부채 (`KISS-5`) | `[ ]` | 단일 실행 경로 단일화 및 아키텍처 다이어트 예정 |

---

## 4. 스토리지 계층 (`pkg/storage`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| - | - | - | - | (엔진 완료 후 순차 점검 예정) | - | - |

---

## 5. 프로시저 & VDBE 계층 (`pkg/proc`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| - | - | - | - | (엔진 완료 후 순차 점검 예정) | - | - |

---

## 6. 와이어 프로토콜 & API 계층 (`pkg/wire`, `pkg/wire/tds`, `pkg/api`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-WIRE-001** | P1 | 결함 | [`pkg/wire/tds/server.go:1344-1350`](pkg/wire/tds/server.go#L1344-L1350) | `handleRPCExecuteSQL`의 Second pass에서 2번째 슬롯 매핑 시 `p.Name == "" \|\| name == "@params"` 조건 제약으로 인해 `@parameters` 등 비표준 이름 사용 시 `paramsDecl` 누락 및 `boundParams` 오염 위험 | `[ ]` | 100% 위치 기반 매핑(Slot 1 문자열이면 무조건 paramsDecl)으로 수정 예정 (`TDS-RPC-AUDIT-1`) |
| **REV-WIRE-002** | P1 | 결함 | [`pkg/wire/tds/server.go:947-970, 1106-1126`](pkg/wire/tds/server.go#L947) | `handleRPCPrepare` (ProcID 11) 및 `handleRPCPrepExec` (ProcID 13)에서 이름 매핑 루프와 슬라이스 길이 fallback의 혼선으로 비표준 인자명 전송 시 쿼리/선언부 왜곡 위험 | `[ ]` | [MS-TDS] 2.2.6.6 규격 기반 100% Slot Index 매핑으로 단순화 예정 (`TDS-RPC-AUDIT-1`) |
| **REV-WIRE-003** | P2 | 오버스펙 | [`pkg/wire/tds/server.go:2530-2612`](pkg/wire/tds/server.go#L2530) | `matchVersionQuery`가 DbGate 4컬럼 쿼리 및 단일 `SERVERPROPERTY` 쿼리를 문자열 패턴으로 가로채어 VDBE 내장 스칼라 엔진을 우회하고 인위적 결과셋을 합성하는 오버스펙 잔재 | `[ ]` | 필수 단일 프로브(`@@VERSION`) 제외 정규 VDBE 엔진 위임으로 다이어트 예정 |
| **REV-WIRE-004** | P1 | 결함 | [`pkg/wire/tds/server.go:2536-2550`](pkg/wire/tds/server.go#L2536) | `matchVersionQuery`에서 `SERVERPROPERTY` 2개 이상 포함 시 임의 쿼리를 강제로 4개 컬럼(`version, productVersion, engineEdition, versionText`)으로 하이재킹하던 치명적 결함 | `[x]` | 다중 컬럼 하이재킹 전면 제거 및 정규 VDBE 스칼라 엔진 위임 완료 (`57e51aa`) |
| **REV-CAT-001** | P2 | 안티패턴 | [`pkg/catalog/virtual/mssql.go, interceptor.go`](pkg/catalog/virtual/mssql.go) | 가상 카탈로그 핸들러 명칭에 외부 GUI 도구(`Beekeeper`, `DbGate`, `DBeaver`)를 하드코딩하고 `isDBeaver` 등 도구 탐지 플래그를 사용하던 안티패턴 | `[x]` | 객관적 스키마 함수(`execSysIndexesPrimaryKey`, `hasStorageSizeCols` 등)로 리팩토링 완료 (`57e51aa`) |
