# LionSQL Engineering P1 과제 세부 명세 및 수락 기준 아카이브

이 문서는 LionSQL의 `v0.9.0` 스프린트 완료 이전에 [TODO.ko.md](../TODO.ko.md) 제2절 하단에 유지되었던 **P1 핵심 과제별 세부 명세 및 수락 기준(Acceptance Criteria)**의 원문을 보존하는 공식 아카이브 문서입니다.

> [!NOTE]
> 활성 엔지니어링 백로그 및 마감 대시보드는 [TODO.ko.md](../TODO.ko.md)를 참조하세요.
> 107대 결함 조치 아카이브는 [BUG_ARCHIVE.ko.md](../BUG_ARCHIVE.ko.md)에 보존되어 있습니다.
> 각 기능별 영구 공식 기술 문서는 본문의 관련 링크를 참조하세요.

---

### P1 과제 세부 명세 및 수락 기준 (Acceptance Criteria)

#### 1. 글로벌 메모리 예산 상한제 및 회로 차단기 (`pkg/config`, `pkg/execution`, `cmd/server`)
* **과제 개요**: 무제한 메모리 할당으로 인한 OS OOM Killer 사살 방지(Golden Rule 1, 23)를 위한 프로세스/세션 레벨 방어 체계 구축.
* **핵심 아키텍처 및 구현 요구사항**:
  1. **`--max-memory` 글로벌 CLI 플래그 및 환경 설정 도입**:
     - `--max-memory` (예: `2GB`, `4GB`) 플래그 및 `resources.max_memory` 설정 연동.
     - 미설정 시 기본값: **호스트 시스템 물리 메모리(RAM)의 65%** (1GB OS 예비 및 50% 하한 가드) 자동 계산 적용 (cgroups/sysctl/OS 메모리 자동 감지).
  2. **Go 런타임 플랫폼 프리미티브 연동 (`debug.SetMemoryLimit`)**:
     - Go 1.19+ `debug.SetMemoryLimit`을 호출하여 힙 메모리가 상한에 근접할 시 Go 런타임 GC가 사전에 능동적으로 메모리를 회수하도록 보장 (Golden Rule 14).
     - 서버 기동 배너에 실시간 적용 메모리 예산 로깅 (`[Memory Governor] max_memory: X.XGB (65% of RAM), work_mem: 64MB`).
  3. **대량 조인(`HashJoinIterator`) Fail-Fast 회로 차단기 (Circuit Breaker)**:
     - 복잡한 디스크 스필러 오버엔지니어링(YAGNI, Golden Rule 15)을 배제하고, Build 단계에서 `buildMemory > workMem` 도달 시 즉시 안전한 중단 및 에러 반환 (`hash join memory budget exceeded: ... > work_mem`).
     - 비정상적인 카테시안 조인 또는 초대용량 해시 조인으로부터 다른 정상 OLTP 쿼리들을 완벽히 격리 보호.
* **영구 기술 문서**: [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](../doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md)

#### 2. [Post-1.0 (v1.1.0) 연기] 엔터프라이즈 암호학적 불변 감사 로그 서브시스템 구축 (`pkg/audit`, `AUDIT-LOG-1`)
* **마일스톤 분류**: 1.0 LTS 출시 기준 일원화(Gate 4는 기존 TDE, RBAC, HTTP 감사 추적으로 충족)에 따라 **Post-1.0 (`v1.1.0`)** 마일스톤으로 연기 확정.
* **표준 규격**: PCI-DSS v4.0 Requirement 10, SOC 2 Type II, HIPAA §164.312(b) ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](IMMUTABLE_AUDIT_LOG_PLAN.md))
* **핵심 아키텍처 및 구현 요구사항**:
  1. **SHA-256 해시 체이닝 (Cryptographic Hash Chaining)**:
     - 각 감사 레코드는 이전 레코드의 해시값을 포함하여 연결 ($H_n = \text{SHA256}(H_{n-1} \parallel R_n)$).
     - 로그 파일 임의 변조, 행 삭제, 재정렬 시 오프라인 검증기에서 $100\%$ 즉각 탐지.
  2. **비차단 MPSC 링 버퍼 (Non-Blocking Ring Buffer, Golden Rule 29)**:
     - 트랜잭션 커밋 핫패스에서 디스크 I/O를 직접 수행하지 않고 인메모리 링 버퍼로 원자적 전달.
     - 전용 백그라운드 플러셔 goroutine이 배치 단위 fsync 기록하여 OLTP 트랜잭션 지연 영향 $0\text{ms}$ 보장.
  3. **감사 대상 이벤트 태깅**:
     - 인증 이벤트: 로그인 성공/실패, 토큰 발급/폐기, 패스워드 변경.
     - 권한 제어: `CREATE LOGIN/USER`, `GRANT`, `REVOKE`, `DENY`.
     - 스키마 변경: `CREATE/ALTER/DROP TABLE`, `INDEX`, `PROCEDURE`, `VIEW`.
     - 데이터 조작(선택적): 슈퍼유저 DML, 테이블 벌크 반출입.
  4. **독립 CLI 무결성 검증기**:
     - `lionsql audit verify -file=<audit.log>` 명령어를 통해 외부 감사인이 오프라인에서 암호학적 무결성 검증 가능.

#### 3. 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 (`pkg/storage/s3sign`, `pkg/config`, `pkg/api`, `pkg/proc`)
* **과제 개요**: 대용량 바이너리 스트리밍 및 이미지 썸네일 처리를 DB 커널에서 배제(Spec-Out, `GEMINI.md` 8.7절)하고, 클라이언트가 외부 S3/R2/MinIO로 직접 업로드하도록 지원하는 제로-의존성 순수 Go SigV4 서명 서브시스템 구축.
* **사전 구현 세부 요구사항 (Phase 1 ~ Phase 4)**:
  1. **제로-의존성 경량 AWS SigV4 서명기 (`pkg/storage/s3sign`)**:
     - 외부 AWS SDK(수십 MB) 없이 Go 표준 `crypto/hmac`, `crypto/sha256`만으로 Canonical Request, StringToSign, 다단계 파생 서명 키 계산.
     - `SignPresignedPUT(bucket, key, contentType, expiresIn)` 및 `SignPresignedGET(bucket, key, expiresIn)` 구현.
     - AWS 공식 Canonical Test Suite 기반 100% 호환성 단위 테스트 검증.
  2. **통합 스토리지 설정 서브시스템 (`pkg/config`)**:
     - `S3Config` (endpoint, region, access_key_id, secret_access_key, default_bucket, force_path_style, cdn_base_url) 구조체 및 환경 변수 바인딩 (`LIONSQL_S3_*`).
  3. **HTTP REST Pre-signed 발급 API 엔드포인트 (`pkg/api/handlers_storage.go`)**:
     - `POST /api/storage/presign` 엔드포인트 (JWT/API-Key RBAC 인증).
     - 업로드용 `upload_url`, 조회용 `public_url`, 유효기간 반환 및 Content-Length/MIME 타입 화이트리스트 가드.
  4. **T-SQL 내장 스칼라 함수 확장 (`pkg/proc/builtins_s3.go`, `pkg/engine`)**:
     - `S3_PRESIGNED_PUT_URL('avatars/user.png')` 및 `S3_PRESIGNED_GET_URL(...)` 스칼라 함수 등록.
     - Stored Procedure 및 대화형 쿼리에서 직접 업로드 링크 생성 지원.
* **영구 기술 문서**: [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](S3_PRESIGNED_STORAGE_PLAN.md)

#### 4. 크리티컬 패스 5대 무할당 최적화 완결 (A~E) (`PERF-1`) (`pkg/engine`, `pkg/wire/tds`)
* **과제 개요**: TDS 네트워크 수신부터 쿼리 분할, 파라미터 바인딩, 엔진 실행, 결과 행 직렬화에 이르는 핫패스 전수 진단 및 5대 병목(A~E) 전면 제거 (완료 `[x]`).
* **5대 최적화 상세 내역 및 벤치마크 성과**:
  1. **[A] 정규식 패키지 싱글톤 승격 (`pkg/engine`)**:
     - `execSelect`, `execCreateTable`, `execCreateView`, `execDropView`, `execAlterTable`, `execCreateStatistics` 내부 동적 `regexp.MustCompile`을 전면 패키지 레벨 `var`로 승격.
     - 쿼리당 15~30µs 컴파일 지연 및 15~25개 힙 객체 할당 100% 제거.
  2. **[B] 문장 분할기 인덱스 기반 제로-카피 슬라이스 스캐너 전면 재작성 (`pkg/engine/query_utils.go`)**:
     - 바이트 단위 `cur.String()` O(N^2) 검사 루프와 `strings.Builder`를 전면 폐기하고, 단일 쿼리 세미콜론 검사 Fast-Path 및 인덱스 슬라이싱(`query[start:i]`) 도입.
     - **단일 쿼리 분할 성능: 16.46 ns/op, 16 B/op, 1 allocs/op** (40KB 메모리 폭증 및 마이크로초 지연 완전 박멸).
  3. **[C] TDS 파라미터 바인딩 O(N) 단일 패스 치환기 (`pkg/wire/tds/server.go`)**:
     - `sp_executesql`, `sp_execute`에서 파라미터별 `regexp.MustCompile` 및 `[]rune` 변환을 제거하고 바이트 스캐너 기반 1-Pass 치환(`substituteParamsSinglePass`) 적용.
     - **3개 파라미터 바인딩 성능: 923.1 ns/op (50배 가속, 서브마이크로초 진입)**.
  4. **[D] TDS Row 스트리밍 직렬화 및 리플렉션 제거 (`pkg/wire/tds/token.go`, `server.go`)**:
     - `WriteRowDirect` 구현으로 행당 신규 슬라이스 할당 및 이중 복사 완전 제거 (**53.49 ns/op, 0 B/op, 0 allocs/op 달성**).
     - `writeColumnValue` 내 `binary.Write` 리플렉션을 스택 바이트 배열 기반 `binary.LittleEndian.PutUint16/32/64` 및 `math.Float64bits`로 교체하여 런타임 리플렉션 100% 제거.
     - 문자열 컬럼 `val.(string)` 타입 단언 선행으로 불필요한 `fmt.Sprintf` 우회.
  5. **[E] UTF-16LE / UTF-8 제로-할당 변환 (`pkg/wire/tds/packet.go`)**:
     - ASCII Fast-Path 도입으로 쿼리/메타데이터 송수신 시 중간 `[]rune`, `[]uint16` 슬라이스 제거.
     - **`EncodeUTF16LE_ASCII`: 121.9 ns/op, 192 B/op, 1 allocs/op** (3회 ➔ 1회 할당).
     - **`DecodeUTF16LE_ASCII`: 266.3 ns/op, 96 B/op, 1 allocs/op** (3회 ➔ 1회 할당).
* **재발 방지 3대 불변 원칙 수립**: [`notes/PITFALLS_AND_TRICKS.ko.md`](PITFALLS_AND_TRICKS.ko.md) 13절에 유틸리티 침투 경계, 크리티컬 패스 정규식/바이트 빌더 금지, 벤치마크 미러링 불변 원칙 영구 명문화.

#### 5. EXPLAIN ANALYZE Volcano 런타임 실행 프로파일러 완결 (`OBS-2`) (`pkg/planner`, `pkg/execution`, `pkg/engine`)
* **과제 개요**: 기존 평면적 전체 쿼리 소요 시간 복사 방식을 탈피하고, Volcano 반복자(Iterator) 계층 구조에 결합된 실시간 연산자 단위 프로파일러 구축 (완료 `[x]`).
* **핵심 구현 내역**:
  1. **`InstrumentedIterator` 및 서브마이크로초 나노초 정밀 계측 (`pkg/execution/instrument.go`)**:
     - 개별 연산자의 첫 튜플 반환 시점(`ActualStartupUS`)과 전체 실행 시간(`ActualDurationUS`), 처리 행수(`ActualRows`), 반복 횟수(`ActualLoops`)를 실시간 집계.
     - 나노초(`totalNanos`) 누적으로 1µs 미만 초고속 연산자의 양자화 오차(0µs 절삭) 방지.
     - `Next()` 단일 튜플 및 `NextChunk()` 벡터화 배치 스트리밍 동시 지원.
  2. **재귀적 실행기 계측 빌더 (`pkg/execution/builder.go`)**:
     - `BuildInstrumentedExecutor`를 통해 Scan, Filter, Sort, Limit, HashJoin, MergeJoin, IndexScan 등 모든 Volcano 반복자를 `InstrumentedIterator`로 래핑.
     - `IndexNestedLoopJoinIterator` 내부 인덱스 시크 시 `innerPlan` 루프 횟수(`ActualLoops`) 및 누적 행수 연동.
  3. **쿼리 코디네이터 연동 및 안전한 폴백 (`pkg/engine/sqlexec_explain.go`)**:
     - `EXPLAIN ANALYZE` 실행 시 읽기 전용 트랜잭션에서 계측 실행기를 구동하여 청크를 소진(drain)하고 실행 트리 통계 수집.
     - Volcano 실행 불가 쿼리 시 안전하게 기존 `execSelect`로 폴백하는 무회귀 안전망 유지.
  4. **PostgreSQL 표준 포맷 호환성 (`pkg/planner/planner.go`, `explain.go`)**:
     - Text 형식: `(cost=0.00..0.00 rows=10 width=0) (actual time=0.000..0.250 rows=10 loops=1)`
     - JSON 형식: `"Actual Startup Time"`, `"Actual Total Time"`, `"Actual Rows"`, `"Actual Loops"` 완전 직렬화.
* **영구 기술 문서**: [`doc/INTERNALS_CBO_AND_STATISTICS.md`](../doc/INTERNALS_CBO_AND_STATISTICS.md)

#### 6. LionSQL 1.0 릴리즈 사전 점검 및 정합성 액션 플랜 (`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`)
* **과제 개요**: 1.0 LTS 정식 출시를 앞두고 실시된 전수 아키텍처 리뷰에서 도출된 핵심 개선 과제 (보안 이스케이프, 임시 객체 누수, 브랜드 단일화, 릴리즈 게이트 일치화).
* **세부 과제 및 진행 현황**:
  1. **[A] 세션 변수(`@var`) 치환 시 작은따옴표 이스케이프 (`SEC-ESC-1`, 완료 `[x]`)**:
     - `ParseSessionVarValue`의 SQL 리터럴(`'O''Reilly'`) 언이스케이프(`strings.ReplaceAll(inner, "''", "'")`) 보강.
     - 쿼리 내 `@var` 치환 시 문자열 리터럴에 대해 작은따옴표 이중화(`''`) 강제로 2차 SQL 인젝션 및 구문 파괴 방지.
  2. **[B] 테이블 변수(`TableVars`) 및 임시 테이블 생명주기 완전 정리 (`SESS-CLEAN-1`, 완료 `[x]`)**:
     - 세션 종료(`CloseSession`) 및 코디네이터 종료(`Close`) 시 `default` 세션을 포함하여 모든 `__tv_*` 물리 테이블 즉시 드롭.
     - 커넥션 풀 리셋(`DISCARD ALL`, `RESET ALL`) 실행 시 할당된 세션 임시 테이블과 테이블 변수를 물리 드롭하여 스토리지 누수 원천 차단.
  3. **[C] 웹훅 헤더, SDK 엔드포인트 및 잔여 브랜드 브랜드 완전 정돈 (`BRAND-UNIFY-1`, 완료 `[x]`)**:
     - `pkg/api/webhook.go`, `pkg/cdc/outbox_sinks.go`: 웹훅 헤더를 `X-LionSQL-Signature`, `X-LionSQL-Event`, `X-LionSQL-Delivery` 및 `LionSQL-Webhook/1.0`으로 완전 전환.
     - `pkg/api/sdk/`: `lionsql.js` 및 `lionsql.d.ts`로 정식 전환, 엔드포인트 `/sdk/lionsql.js` 신설 및 `class LionSQL`로 완전 일원화 (하위 호환 별칭 없이 단일화).
     - `DB_NAME()`, `APP_NAME()`, `SHOW server_version`, `SELECT @@VERSION`, TDS `BuildLoginAck`, `BuildError`, PG Wire ParameterStatus, TLS 인증서 Organization의 잔여 명칭을 `LionSQL`로 완전 일원화.
  4. **[D] 1.0 릴리즈 게이트 4 기준 일치화 및 문서 권한 정규화 (`REL-ALIGN-1`, 완료 `[x]`)**:
     - `ROADMAP.md` Gate 4를 엔터프라이즈 보안 및 거버넌스(TDE, RBAC, 감사 추적)로 1.0 충족 확정하고 암호학적 불변 감사 로그(`pkg/audit`)를 Post-1.0(`v1.1.0`)으로 연기 일원화.
     - `doc/README.md`, `doc/REFERENCE_MANUAL.ko.md` 파일 퍼미션 `0644` 정규화 완료 (Golden Rule 5).
  5. **[E] 외부 인터페이스 바인딩 시 프로덕션 보안 가드레일 (`PROD-SEC-1`, 완료 `[x]`)**:
     - `pkg/config`: `IsLoopbackHost`, `NonLoopbackServiceAddresses`, `ValidateSecurityGuard` 구현.
     - `127.0.0.1` / `localhost` 바인딩일 때만 기본 비밀번호(`1234567890`) 허용 (Zero-config 편의성 유지).
     - `0.0.0.0`, `:port`, 공용 IP 등 외부 노출 인터페이스 바인딩 시 기본 비밀번호 기동 즉각 차단 및 조치 가이드라인 출력 (`os.Exit(1)`).
     - 환경 변수 `LIONSQL_ADMIN_PASS` 지원 추가.
     - `lionsql doctor`에 `Production Security Guard` 진단 항목 신설 (외부 노출 시 FAIL, 로컬호스트 또는 비밀번호 변경 시 OK 판정).
     - `pkg/config/config_test.go` 단위 테스트 100% 검증 통과.
* **영구 기술 문서**: [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)

#### 7. TDS 프로토콜 ➔ 코디네이터 ➔ VDBE ➔ 스토리지 전 계층 수직 성능 최적화 (`VPERF-1` ~ `VPERF-14`)
* **과제 개요**: TDS 네트워크 패킷 수신부(`pkg/wire/tds`)부터 SQL 코디네이터(`pkg/engine`), VDBE 바이트코드 VM(`pkg/proc/runtime`), 스토리지 엔진(`memengine`, `wiredtiger`)까지 수직 핫패스(Vertical Hot-Path)의 병목을 전면 제거.
* **Track A — 스토리지 & 코디네이터 치명적 병목 즉시 해소 (`VPERF-1` ~ `VPERF-4`)**:
  1. **`VPERF-1` (`pkg/storage/memengine/tx.go`, `memengine.go`, 완료 `[x]`)**: `MEM-3` 메모리 가버너 활성 시 매 쓰기 트랜잭션 `Commit()`마다 런타임 메트릭스 및 슬라이스 힙 할당을 수행하던 병목을 원자적 캐시(`cachedHeapBytes`) 및 128회 주기 샘플링(`commitCounter`) 방식으로 교체하여 커밋 핫패스 $O(1)$(1~2ns) 무할당 전환 완료.
  2. **`VPERF-2` (`pkg/engine`, `pkg/wire/tds`, 완료 `[x]`)**: Autocommit 쿼리(`tryPKLookupBypass`, `execVolcanoSelect`, `execInsert`, `execUpdate`, TDS `handleRPCExecute`)마다 `engine.NewSession()` 생성 및 `Close()`로 인해 WiredTiger 세션별 `WT_CURSOR` 풀이 매번 파괴되는 문제를 `ClientSession.Session`(또는 커넥션별 영속 세션) 재사용으로 해결.
  3. **`VPERF-3` (`pkg/engine/sqlexec_dml.go`, `sqlexec_ddl.go`, 완료 `[x]`)**: `AUTO_INCREMENT`(`IDENTITY`) 컬럼 보유 테이블에 대한 `execInsert` / `execInsertSelect` 시 매번 전체 테이블을 풀 스캔(`tx.Scan`)하던 $O(N)$ 병목을 코디네이터 메모리 시퀀스 캐시(`GetTableLastIdentity`) 기반 $O(1)$ 즉시 채번으로 교체 및 `TRUNCATE`/`DROP` 시 0 리셋 연동 완료.
  4. **`VPERF-4` (`pkg/storage/memengine/tx.go`, `memengine.go`, 완료 `[x]`)**: `memengine`의 매 `Update` / `Delete`마다 `engine.mu.RLock()`을 획득하고 전체 테이블 맵을 순회하며 역방향 외래키(FK)를 검사하는 오버헤드를 DDL 시점 사전 계산된 역방향 외래키 인덱스 캐시(`fkCache`)로 교체하여 $O(1)$ 단락 처리.
* **Track B — TDS & VDBE 저위험 무할당 튜닝 (`VPERF-7` ~ `VPERF-9`, `VPERF-13`, `VPERF-14`)**:
  5. **`VPERF-7` (`pkg/wire/tds/packet.go`, `token.go`, `server.go`, 완료 `[x]`)**: `WritePacket`의 `4096` 하드코딩을 세션 협상 `PacketSize`(최대 32KB)로 연동하고, 응답 `bytes.Buffer` 재사용 및 `WriteUTF16LEDirect` 직접 버퍼 쓰기로 셀당 슬라이스 할당 제거.
  6. **`VPERF-8` (`pkg/proc/runtime/vm.go`, 완료 `[x]`)**: `PlanCache.GetOrCompile` 조회 시 매번 수행되는 `sha256.Sum256` + `hex.EncodeToString` 해싱과 배타적 쓰기 락(`mu.Lock()`) 경합을 원문 문자열 키 및 `RLock` 패스트패스로 개선.
  7. **`VPERF-9` (`pkg/proc/runtime/vm_dispatcher.go`, `vm.go`, 완료 `[x]`)**: VDBE `OpInsert`/`OpUpdate` 시 트리거가 없는 테이블에 대한 무조건적인 `rowMap`/`oldRow`/`newRow` 할당을 생략하고, `execUpdateWhere` 내부 할당 루프의 `buildRowEvalMap` 중복 호출을 루프 외부로 호이스팅.
  8. **`VPERF-13` (`pkg/engine/sqlexec_dml.go`, 완료 `[x]`)**: `INSERT ... ON CONFLICT (col) DO UPDATE/NOTHING` 처리 시 충돌 대상 컬럼에 보조 유니크/B-Tree 인덱스가 존재함에도 불구하고 무조건 $O(N)$ 전체 테이블 스캔(`tx.Scan`)으로 폴백하던 성능 결함을 `schema.FindIndexByColumn` 검사 후 `tx.IndexScan` 단건 룩업으로 즉시 최적화.
  9. **`VPERF-14` (`pkg/proc/runtime/vm.go`, 완료 `[x]`)**: VDBE `execUpdateWhere` 및 `execDeleteWhere`에서 `for cursor.Next()` 행 스캔 루프 내부에서 매 행마다 `tg := f.executor.getTableGetter()` 및 `schema, err := tg(data.Table)` 카탈로그 조회를 반복 수행하여 카탈로그 뮤텍스 락 경합을 유발하던 루프 불변식(Loop Invariant)을 루프 외측으로 1회 호이스팅.
* **Track C — [Post-1.0 (`v1.1.0`) 연기] 고위험 구조 개편 및 스트리밍 (`VPERF-5`, `VPERF-6`, `VPERF-10` ~ `VPERF-12`)**:
  10. **`VPERF-5` (`pkg/wire/tds/server.go`)**: `runQueryWithAttention` 취소 감지 고루틴/채널 제거 (TDS 취소 프로토콜 상태머신 회귀 방지를 위해 v1.1.0 이관).
  11. **`VPERF-6` (`pkg/wire/tds/server.go`)**: `sp_executesql`(ProcID 10) VDBE 직결 (클라이언트 드라이버 RPC 호환성 보호를 위해 v1.1.0 이관).
  12. **`VPERF-10` (`pkg/engine`, `pkg/wire/tds`)**: `Row -> Tuple -> map[string]any -> TDS Wire` 맵 제거 및 `RawRows` 슬라이스 직결 (전체 프로토콜/엔진 공용 구조체 영향 최소화를 위해 v1.1.0 이관).
  13. **`VPERF-11` (`pkg/storage/memengine/tx.go`)**: In-Memory OCC `Scan()` 샤드 스트리밍 커서 전환 (검증된 TPC-C 20k TPS 커서 무결성 보호를 위해 v1.1.0 이관).
  14. **`VPERF-12` (`pkg/storage/wiredtiger/wt_engine.go`)**: WiredTiger `updateLocked` 단일 커서화 및 Zero-Copy `unsafe.Slice` 디코딩 (CGo 포인터 생명주기 세그폴트 위험 차단을 위해 v1.1.0 이관).
* **영구 기술 문서**: [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](../doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md)

#### 8. 데이터 무결성, 대소문자 정합성, 임의 추측 및 무작위 맵 순회 107대 결함 전수 조치 아카이브 (`BUG-107`)
* **과제 개요**: SQL 표준의 식별자 대소문자 무관성(Case-insensitivity)과 Go 맵/스칼라의 대소문자 엄격 비교 불일치, 그리고 식별자/인덱스 조회 실패 시의 취약한 무작위 순회 폴백(`for _, v := range rec/row/map { break }`) 및 첫 번째 요소 임의 대입(`[0]`), 외래키 무검증/오판, NULL 전파 누락, WAL 복구 절단 처리 실패, Int-Float 및 DateTime-VarChar 비교 역전, 파생테이블 조인 프로젝션 유실, 혼합타입 집계합 누락, 기본값(DEFAULT) 누락, 관리자 인증 우회, MERGE IDENTITY 누락, 복합 PK 파싱 절단, COPY 프로토콜 세션 우회 및 데이터 변조, 쉼표 오절단, RBAC 커스텀 롤 누락, 따옴표 이스케이프 분해 파괴, 샤드 초기화 경쟁 크래시, VDBE 미존재 PK 크래시, EXEC 프로시저 및 TDS RPC 트랜잭션 우회, Simple CASE 누락, 산술식 0값 변조, 윈도우 함수 RANGE 피어 누락, OCC 동일 트랜잭션 유니크 충돌 사일런트 커밋, HTTP API DATETIME 빈 문자열 방출, PITR DDL 에러 침묵 등으로 인해 발생하는 107대 치명적 무결성 결함을 원천 해결 완료.
* **조치 이력 및 세부 내역 아카이브**:
  - 본 107대 결함 트랙은 LionSQL의 데이터 무결성 조기 안정화를 위해 추진된 중간 단계 방법론(Interim Methodology)으로 100% 조치 완료(`[x]`)되었습니다.
  - 107개 결함의 상세 파일 경로, 버그 발생 원인, 수정 코드 및 증적 일람은 별도의 독립 아카이브 문서인 [**`BUG_ARCHIVE.ko.md`**](../BUG_ARCHIVE.ko.md)로 분리 이관되었습니다.
  - 향후 결함 감사, 정적 분석 및 구조적 리팩토링은 [`CODE_REVIEW.ko.md`](../CODE_REVIEW.ko.md) 및 [`REFACTORING.ko.md`](../REFACTORING.ko.md)에서 지속 관리됩니다.
