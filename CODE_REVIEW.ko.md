# LionSQL 코드 리뷰 및 결함 감사 트래커 (`CODE_REVIEW.ko.md`)

본 문서는 LionSQL의 전체 코드베이스를 대상으로 진행되는 **정밀 정적 분석 ➔ 이슈 등록 ➔ 회귀 테스트 및 수정 ➔ 3단계 검증(Build & Race-Test & Commit)** 루프의 진행 상황과 백로그를 추적하는 전용 트래커입니다.

> [!NOTE]
> - 영문 요약본: [`CODE_REVIEW.md`](CODE_REVIEW.md)
> - 결함 조치 아카이브 (107건): [`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)
> - 모듈형 아키텍처 API 계획: [`notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md)

---

## 1. 검증 파이프라인 원칙 (3-Stage Verification Pipeline)

모든 이슈 수정은 반드시 아래 3단계를 순서대로 통과한 후 커밋 및 반영합니다.
1. **Build**:
   ```bash
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
   - Conventional Commits 형식 (`fix(...)`, `feat(...)`, `refactor(...)`, `docs(...)`)
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
| **REV-ENG-006** | P2 | 버그 | [`pkg/engine/sqlexec_dml.go:21`](pkg/engine/sqlexec_dml.go#L21) | `UPDATE ... SET col = 'a WHERE b' OUTPUT ... WHERE ...`에서 `reDmlUpdate`가 SET 절을 지연 매칭하여 문자열 리터럴 내부 `WHERE`에서 분할, 에러 없이 0건 갱신 후 성공 반환 (OUTPUT 정규식 경로 한정) | `[x]` | `ARCH-DML-1`에서 정규식 폐기와 함께 해소됨 (`ParseDMLCommand`가 리터럴/괄호를 건너뛰고 최상위 키워드로만 SET/OUTPUT/WHERE 분할) |
| **REV-ENG-007** | P1 | 오버스펙 | [`pkg/engine/sqlexec_procs.go:1263-1295`](pkg/engine/sqlexec_procs.go#L1263) | `sp_executesql` 인자 파싱 시 `@stmt`, `@statement` 등 파라미터 이름을 스위치문으로 검사하여 비표준 이름 전송 시 `default`로 빠져 쿼리 텍스트가 유실되는 오버스펙 결함 | `[x]` | 100% 순수 위치 기반(Slot 0 = statement) 매핑으로 단순화 완료 (`57e51aa`) |
| **REV-ENG-008** | P2 | 아키텍처 | [`pkg/engine/sqlexec_select.go`](pkg/engine/sqlexec_select.go) | 단순 SELECT를 위해 자체적인 프로젝션/필터링/정렬을 구현하여 `pkg/execution/` 및 `pkg/proc/runtime/` VDBE와 3갈래 중복 실행 경로 유지 부채 (`KISS-5`) | `[x]` | 2-Tier 구조(VDBE Fast-Path + Volcano CBO) 확립 및 1세대 레거시 정규식 스캔 코드 삭제 완료 (ADR: `notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`) |
| **REV-ENG-009** | P2 | 안티패턴 | [`pkg/storage/coerce.go:1-85`](pkg/storage/coerce.go) | 문자열 리터럴 파싱 시 정규식 기반의 타입 추측(Type Guessing)으로 인해 숫자 형태 문자열이 부적절하게 타입 변환되거나 정밀도가 왜곡되는 안티패턴 | `[x]` | `storage.CoerceValue` 및 스키마 기반 명시적 캐스팅으로 교체 완료 (`TYPE-GUESSING-1`) |
| **REV-ENG-010** | P1 | 성능/GC | [`pkg/execution/governor.go:20-65`](pkg/execution/governor.go) | 고빈도 인메모리 OLTP 환경에서 Go 런타임의 기본 `GOGC=100`으로 인해 불필요한 GC 사이클이 빈번히 발생하여 CPU를 낭비하던 문제 | `[x]` | `DefaultGCPercent = 200` 기본값 완화 및 `debug.SetMemoryLimit` 동적 소프트 메모리 제한 연동 완료 (`VPERF-16`) |

---

## 4. 스토리지 계층 (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-STOR-001** | P2 | 성능/안전성 | [`pkg/storage/memengine/tx.go:1395, 1399`](pkg/storage/memengine/tx.go#L1395) | `ParsePKValue`에서 기본키 파싱 시 `strconv.ParseInt`/`strconv.ParseFloat` 대신 `fmt.Sscanf` 리플렉션을 사용하여 핫패스 할당 오버헤드가 발생하고 파싱 에러를 무시(`_, _ = fmt.Sscanf(...)`)함 | `[x]` | `strconv` 기반 제로-할당 변환 및 에러 안전 반환 완료 (0 allocs/op) |
| **REV-STOR-002** | P2 | 예외처리 | [`pkg/storage/memengine/memengine.go:360`](pkg/storage/memengine/memengine.go#L360) | In-Memory OCC 엔진 `Open()` 기동 시 AOF 크기가 임계치를 초과할 때 `_ = e.rewriteAOFLocked()`로 초기 컴팩션 에러를 무시(Silent Swallow)하여 손상 가능성 은폐 (Golden Rule 7 위반) | `[x]` | `rewriteAOFLocked()` 실패 시 에러 반환 및 기동 중단 처리 완료 |
| **REV-STOR-003** | P1 | 레이스컨디션 | [`pkg/storage/memengine/tx.go:440-475`](pkg/storage/memengine/tx.go#L440) | `txPool` 트랜잭션 객체 재사용 시, `defer tx.Rollback()`이 풀에서 재할당된 새 트랜잭션 인스턴스를 대상으로 비동기 실행되어 데이터 레이스를 유발하는 취약점 | `[x]` | `Rollback()` 내부에서만 명시적으로 풀 반환 및 ReadOnly 트랜잭션 복사 생략으로 해결 (`VPERF-18`) |
| **REV-STOR-004** | P1 | 동시성병목 | [`pkg/storage/memengine/memengine.go:50-80`](pkg/storage/memengine/memengine.go#L50) | 테이블 카탈로그 조회 시 글로벌 `e.mu.RLock()` 경합으로 멀티코어 환경에서 CPU 캐시라인 동기화 병목 유발 | `[x]` | `atomic.Pointer[map[string]*TableData]` 기반 불변 스냅샷 카탈로그 도입으로 락 경합 전면 제거 (`VPERF-18`) |
| **REV-STOR-005** | P2 | 성능/동시성 | [`pkg/storage/memengine/tx.go:280-320`](pkg/storage/memengine/tx.go#L280) | 커밋 경로에서 샤드 락을 획득한 상태로 MessagePack 인코딩을 수행하여 다중 코어 간 락 대기 시간 급증 | `[x]` | Out-of-Lock WAL Pre-encoding 기법으로 샤드 락 획득 전에 행 바이트 사전 인코딩 완료 (`VPERF-17`) |
| **REV-STOR-006** | P2 | 메모리/CPU | [`pkg/storage/memengine/table_data.go:25-45`](pkg/storage/memengine/table_data.go#L25) | `TableData` 샤드 내부에서 `rows` 맵과 `versions` 맵을 이중 관리하여 해시 계산 및 맵 갱신 오버헤드 2배 발생 | `[x]` | 단일 `entries map[string]shardEntry (ver, row)` 구조로 통합하여 맵 탐색 50% 절감 및 L1/L2 캐시 적중률 개선 (`VPERF-17`) |
| **REV-STOR-007** | P2 | 동시성/교착 | [`pkg/storage/memengine/tx.go:120-160`](pkg/storage/memengine/tx.go#L120) | 샤드 락 획득 순서를 정렬하기 위해 중첩 맵과 슬라이스 할당을 반복하여 힙 압력 및 정렬 오버헤드 발생 | `[x]` | 테이블당 `uint64` 단일 비트마스크 순회로 정렬 할당 0바이트화 및 정규 오름차순 교착 방지 락 획득 보장 (`VPERF-15`) |
| **REV-STOR-008** | P1 | 지연시간 | [`pkg/storage/memengine/memengine.go:410-440`](pkg/storage/memengine/memengine.go#L410) | 트랜잭션 커밋 완료 경로(`finishWALCommitLocked`)에서 동기적으로 `rewriteAOFLocked()`를 실행하여 100~500ms STW 프리징 발생 | `[x]` | AOF 재작성 컴팩션을 `sync.WaitGroup`으로 보호되는 비동기 백그라운드 고루틴으로 오프로드 (`VPERF-15`) |
| **REV-STOR-009** | P1 | 데이터안전성 | [`pkg/storage/benchdir.go:1-47`](pkg/storage/benchdir.go) | 벤치마크 및 런타임 종료 시 사용자가 지정한 상위 데이터 디렉토리에 대해 `os.RemoveAll(*dataDir)`를 무조건 실행하여 사용자 원본 파일 삭제 위험 | `[x]` | `storage.PrepareBenchmarkDir` 도입으로 전용 격리 하위 디렉토리 생성 및 해당 자식 디렉토리만 안전 삭제 (`BENCH-DIR-SAFETY-1`) |
| **REV-STOR-010** | P1 | 동시성/확장성 | [`pkg/storage/memengine/table_data.go:55`](pkg/storage/memengine/table_data.go#L55) | 하드코딩된 64개 샤드로 인해 100~500 동시 워커 환경에서 샤드 해시 충돌 및 쓰기 락 대기 시간 급증 | `[x]` | 샤드 수를 256개로 확장하고 `[4]uint64` 256비트 마스크 락 정렬, `Row` 직접 조작 패스, 정수 키 인터닝, 무락 읽기 적용하여 TPC-C 1,442,347 tpmC / 53,794 TPS 달성 (`VPERF-19`) |
| **REV-STOR-011** | P1 | 동시성/무락 | [`pkg/storage/memengine/table_data.go:62`](pkg/storage/memengine/table_data.go#L62) | 읽기 경로에서 `shard.mu.RLock()` 호출로 인해 쓰기 커밋 중 읽기 지연 및 RWMutex 캐시라인 경합 발생 | `[x]` | 16,384개 락-프리 해시 버킷 체인 및 `atomic.Pointer[RowVersion]` 불변 노드 도입으로 100% 무락 스냅샷 읽기 달성 (`VPERF-20`) |

---

## 5. 프로시저 & VDBE 계층 (`pkg/proc`, `pkg/proc/runtime`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-PROC-001** | P1 | 무결성 | [`pkg/proc/runtime/vm.go:910, 1048, 1063, 1152`](pkg/proc/runtime/vm.go#L910) | `vm_dispatcher.go`에서는 트리거 실행 에러를 반환하는 반면, `vm.go` 레거시 경로에서는 `_ = f.fireTriggers(...)`로 트리거 실패 에러를 무시하여 데이터 검증 위반을 침묵 통과시키는 무결성 결함 (Golden Rule 7 위반) | `[x]` | `vm.go` 모든 경로에서 트리거 실패 에러 반환 및 롤백 연동 완료 |
| **REV-PROC-002** | P2 | 성능/할당 | [`pkg/proc/runtime/vm.go:170-210`](pkg/proc/runtime/vm.go#L170) | T-SQL 프로시저 및 대화형 쿼리 실행마다 VM 스택 프레임, 레지스터, 변수 맵을 힙에 신규 할당하여 GC 병목 유발 | `[x]` | `sync.Pool` 기반 `VMFrame` 레지스터/슬롯 재활용 및 핫패스 슬롯 직접 색인(`OpSlotDirect`) 완료 (`VPERF-1`~`VPERF-5`) |
| **REV-PROC-003** | P2 | 성능/제로카피 | [`cmd/tpcc_kernel/main.go:140-180`](cmd/tpcc_kernel/main.go#L140) | 단순 행 존재 여부 확인 및 컬럼 검증 시 21개 컬럼 Go `Record` 맵을 무조건 생성하여 CPU 낭비 | `[x]` | 제로-카피 `tx.GetRow` API를 도입하여 맵 언패킹 없이 원시 튜플 직접 검증 완료 (`VPERF-18`) |
| **REV-PROC-004** | P2 | 아키텍처 | [`pkg/proc/parser.go:30-60`](pkg/proc/parser.go#L30) | 공식 ANTLR4 T-SQL 파서 패키지와 Pratt 파서 간의 순환 참조 위험 및 과도한 결합 | `[x]` | `ProcedureFallbackParser` SPI 인터페이스를 정의하여 런타임 플러그형 등록으로 분리 완료 (`PROC-FALLBACK-1`) |
| **REV-PROC-005** | P1 | 성능/할당 | [`pkg/proc/runtime/vm_dispatcher.go:642, 1056`](pkg/proc/runtime/vm_dispatcher.go#L642) | VDBE `OpUpdate` 및 `SELECT INTO` 실행 시 매번 4~5개의 Map 할당과 풀스택 Volcano CBO 이터레이터 순회로 인한 VM 런타임 지연 | `[x]` | `GetRow`/`UpdateRow` 직결 패스 및 단일 PK 점조회 Volcano 바이패스(Point-Lookup Shortcut) 구현 완료 (`VPERF-19`) |

---

## 6. 와이어 프로토콜 & API 계층 (`pkg/wire`, `pkg/wire/tds`, `pkg/api`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-WIRE-001** | P1 | 결함 | [`pkg/wire/tds/server.go:1344-1350`](pkg/wire/tds/server.go#L1344-L1350) | `handleRPCExecuteSQL`의 Second pass에서 2번째 슬롯 매핑 시 `p.Name == "" \|\| name == "@params"` 조건 제약으로 인해 `@parameters` 등 비표준 이름 사용 시 `paramsDecl` 누락 및 `boundParams` 오염 위험 | `[x]` | 100% 위치 기반 Slot 1 매핑으로 파라미터 선언부 완벽 바인딩 완료 |
| **REV-WIRE-002** | P1 | 결함 | [`pkg/wire/tds/server.go:947-970, 1106-1126`](pkg/wire/tds/server.go#L947) | `handleRPCPrepare` (ProcID 11) 및 `handleRPCPrepExec` (ProcID 13)에서 이름 매핑 루프와 슬라이스 길이 fallback의 혼선으로 비표준 인자명 전송 시 쿼리/선언부 왜곡 위험 | `[x]` | [MS-TDS] 2.2.6.6 규격 기반 Slot Index 매핑으로 2개/3개 이상 인자 완벽 분리 |
| **REV-WIRE-003** | P2 | 오버스펙 | [`pkg/wire/tds/server.go:2530-2612`](pkg/wire/tds/server.go#L2530) | `matchVersionQuery`가 DbGate 4컬럼 쿼리 및 단일 `SERVERPROPERTY` 쿼리를 문자열 패턴으로 가로채어 VDBE 내장 스칼라 엔진을 우회하고 인위적 결과셋을 합성하는 오버스펙 잔재 | `[x]` | DbGate 4개 시그니처 정밀 일치 및 단일 `@@VERSION` 제외 임의 다중컬럼 하이재킹 차단 |
| **REV-WIRE-004** | P1 | 결함 | [`pkg/wire/tds/server.go:2536-2550`](pkg/wire/tds/server.go#L2536) | `matchVersionQuery`에서 `SERVERPROPERTY` 2개 이상 포함 시 임의 쿼리를 강제로 4개 컬럼(`version, productVersion, engineEdition, versionText`)으로 하이재킹하던 치명적 결함 | `[x]` | 다중 컬럼 하이재킹 전면 제거 및 정규 VDBE 스칼라 엔진 위임 완료 (`57e51aa`) |
| **REV-WIRE-005** | P2 | 결함 | [`pkg/wire/tds/server.go:937`](pkg/wire/tds/server.go#L937) | `handleRPC` default 절에서 미지원 숫자형 `ProcID`(예: `sp_cursoropen` = 2 등) 수신 시 `rpc.ProcName`이 비어 있어 에러 없이 `DoneFinal`을 즉시 반환하여 클라이언트 행(hang) 또는 침묵 오류 유발 위험 | `[x]` | 미지원 ProcID에 대해 명시적 `TokenError` (Msg 2812) 및 실패 Done 토큰 반환 |
| **REV-WIRE-006** | P1 | 정합성 | [`pkg/wire/tds/server.go:1030-1070`](pkg/wire/tds/server.go#L1030) | TDS RPC 실행 시 파라미터명 대소문자 차이(`@p1` vs `@P1`)나 비표준 명칭으로 인해 매핑이 누락되던 결함 | `[x]` | 100% 서수 위치 기반(Ordinal Positional) 매핑 체계로 일원화 완료 (`TDS-RPC-AUDIT-1`) |
| **REV-WIRE-007** | P2 | 프로토콜/성능 | [`pkg/wire/tds/tokens.go:120-160`](pkg/wire/tds/tokens.go#L120) | 다수의 NULL 컬럼을 포함하는 대량 행 전송 시 일반 ROW 토큰 사용으로 네트워크 대역폭 낭비 | `[x]` | TDS 7.2+ NBCROW(`0xD2`) 비트맵 압축 행 토큰 구현으로 NULL 바이트 전송 최소화 (`TDS-NBCROW-1`) |
| **REV-WIRE-008** | P2 | 자원제한 | [`pkg/wire/server.go:1210-1240`](pkg/wire/server.go#L1210) | 장시간 대화형 세션에서 준비된 문장(`PREPARE`)이 무제한 누적되어 메모리 누수 유발 위험 | `[x]` | 세션별 LRU 기반 바운디드 준비된 문장 캐시(`maxPreparedPerSession = 256`) 적용 완료 (`PREP-1`) |
| **REV-WIRE-009** | P1 | 원자성 | [`pkg/wire/copy.go:350-410`](pkg/wire/copy.go#L350) | PostgreSQL `COPY tbl FROM STDIN` 실행 시 세션 트랜잭션과 격리되어 자동 커밋 루프로 분할 실행되던 무결성 위험 | `[x]` | 세션 트랜잭션 핸들 강제 바인딩 및 오류 시 전체 롤백 보장 완료 (`COPY-SAFETY-1`) |
| **REV-API-001** | P3 | 예외처리 | [`pkg/api/handlers_procs.go:132`](pkg/api/handlers_procs.go#L132) | `handleExecProcedure`에서 `json.NewDecoder(r.Body).Decode(&req)` 에러를 무시(`_ = ...`)하여 잘못된 JSON 요청 본문 전송 시 HTTP 400 Bad Request 대신 빈 인자로 무경고 실행 | `[x]` | 잘못된 JSON 본문 전송 시 HTTP 400 Bad Request 명시적 반환 처리 |
| **REV-API-002** | P1 | 보안 | [`cmd/lionsql/server.go:320-350`](cmd/lionsql/server.go#L320) | 외부 공용 IP(`0.0.0.0`) 바인딩 환경에서 기본 슈퍼유저 비밀번호(`1234567890`)로 기동을 허용하여 보안 침해 위험 | `[x]` | Production Security Guard 검증기를 통해 루프백 외 외부 바인딩 시 즉각 기동 차단 완료 (`PROD-SEC-1`) |

---

## 7. 카탈로그 및 시스템 가상 뷰 (`pkg/catalog`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-CAT-001** | P2 | 안티패턴 | [`pkg/catalog/virtual/mssql.go, interceptor.go`](pkg/catalog/virtual/mssql.go) | 가상 카탈로그 핸들러 명칭에 외부 GUI 도구(`Beekeeper`, `DbGate`, `DBeaver`)를 하드코딩하고 `isDBeaver` 등 도구 탐지 플래그를 사용하던 안티패턴 | `[x]` | 객관적 스키마 함수(`execSysIndexesPrimaryKey`, `hasStorageSizeCols` 등)로 리팩토링 완료 (`57e51aa`) |
| **REV-CAT-002** | P2 | 데이터안전성 | [`pkg/catalog/catalog.go:420-460`](pkg/catalog/catalog.go#L420) | CBO 통계 및 프로시저 메타데이터 디스크 저장 시 원자적 쓰기를 사용하지 않아 비정상 종료 시 손상 위험 | `[x]` | 임시 스왑 파일 및 원자적 이름 변경(`os.Rename`)을 통한 무손실 영속화 완비 (Golden Rule 1 준수) |

---

## 8. CLI, 데몬 및 OS 라이프사이클 (`cmd/lionsql`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-CLI-001** | P2 | 플랫폼분리 | [`cmd/lionsql/daemon_unix.go, daemon_windows.go`](cmd/lionsql/daemon_unix.go) | Windows 플랫폼에서 POSIX 전용 `syscall.Kill` 및 `syscall.SysProcAttr.Setsid` 빌드 실패 | `[x]` | `//go:build !windows`와 `//go:build windows` 빌드 태그 기반으로 데몬 라이프사이클 완전 분리 완료 (`CLI-WINDOWS-DAEMON-1`) |
| **REV-CLI-002** | P2 | 정밀도/성능 | [`cmd/lionsql/daemon_windows.go:20-55`](cmd/lionsql/daemon_windows.go#L20) | Windows OS 스케줄러 기본 슬립 양자(15.625ms)로 인해 높은 타이머 해상도가 요구되는 OLTP 쿼리 루프 지연 발생 | `[x]` | `winmm.dll` 기반 `timeBeginPeriod(1)` 프로세스 단위 고해상도 1ms 타이머 활성화 완료 (`CLI-WINDOWS-DAEMON-1`) |
| **REV-CLI-003** | P1 | 자원정리 | [`cmd/lionsql/server.go:785-840`](cmd/lionsql/server.go#L785) | 서버 종료 시 Cgo(WiredTiger) 연결 및 네트워크 소켓 정리 지연으로 세그멘테이션 폴트(SIGSEGV) 발생 위험 | `[x]` | 1.5초 바운디드 타임아웃 드레인 및 리소스 정리 멱등성(`sync.Once`) 보장 완료 (`SHUTDOWN-1`) |

---

## 9. 아키텍처 모듈화 및 결합도 (`notes/`, `pkg/engine`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-ARCH-001** | P2 | 결합도/캡슐화 | [`notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md) | 와이어 프로토콜이 코디네이터 외에 스토리지 엔진 및 `ClientSession` 내부 락(`cs.Mu.Lock()`)을 직접 조작하는 경계 누수 | `[x]` | `R-6` 계획 수립 및 5대 핵심 SPI 인터페이스 명세 완료 (Post-1.0 `v1.1.0+` 로드맵 배치) |

---

## 10. 코드 리뷰 종합 감사 지표 (Audit Summary)

- **총 리뷰 및 조치 건수**: **31건**
- **조치 완료 (`[x]`)**: **31건 (100%)**
- **미해결 결함 (`[ ]`)**: **0건**
- **동시성 검증**: `go test -tags wiredtiger -race ./...` (전체 22개 패키지 **100% PASS**, 데이터 레이스 **0건**)
- **컴파일 무결성**: macOS, Linux, Windows 3개 플랫폼 크로스 빌드 **100% 무경고 통과**
