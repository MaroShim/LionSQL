# LionSQL v0.94.0 릴리즈 노트 (`notes/release_notes/RELEASE_NOTES_v0.94.0.ko.md`)

> - **최초 작성일**: 2026-11-23
> - **릴리즈 버전**: `v0.94.0` (Minor Release)
> - **마일스톤 주제**: **6대 쌍대성(Duality) 교차 검증 하네스 확립, 14개 라운드 라이브 엔진 전수 감사 및 500+ 결함 완치, 통합 트랜잭션 관리자 세션 수명주기 하드닝, 및 Canonical sqlutil 표준화**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.94.0` 버전은 216개의 누적 커밋에 걸쳐 데이터베이스 엔진의 신뢰성을 극한으로 검증하기 위한 **6대 쌍대성(Duality-Driven Cross-Verification) 교차 검증 하네스**를 확립하고, 실무 환경에서 발생할 수 있는 엣지 케이스를 색출한 **14개 라운드의 라이브 엔진 전수 감사**를 완료한 엔터프라이즈 마일스톤 릴리즈입니다:

- **6대 쌍대성(Duality-Driven Cross-Verification) 교차 검증 체계 확립 (`ARCH-DUALITY-CROSS-VERIFICATION-1`)**:
  - 단일 테스트 스위트에 의존하지 않고, 엔진의 상반된 두 축(Dual Axes)을 맞물려 상호 검증하는 전면 대칭성 아키텍처 구축:
    1. **Scenario A (Dual-Wire Differential SLT)**: 동일한 10,706개 코어 SQLLogicTest 쿼리를 PGWire(`:5433`)와 TDS(`:1433`) 양대 프로토콜로 동시 주입하여 바이트 단위 결과셋 100% 대칭성 달성 (`SLT-DUALITY-WIRE-1`).
    2. **Scenario B (Dual-Storage Twin-Engine SLT)**: In-Memory OCC 엔진과 WiredTiger MVCC 엔진에 대해 21,412개 쿼리를 동시 교차 실행하여 스토리지 엔진 간 릴레이셔널 결과 일치율 100% 달성 (`SLT-DUALITY-STORAGE-1`).
    3. **Scenario C (Dual-Parser Differential SLT)**: 네이티브 Fast-Path Pratt 파서와 공식 ANTLR4 T-SQL 문법 파서의 구문 분석 결과를 교차 대조하여 34개 쿼리 및 30개 프로시저 스위트 100% AST 파리티 달성 (`SLT-DUALITY-PARSER-1`).
    4. **Scenario D (T-SQL Stored Procedure Wire Duality)**: 30개 코어 T-SQL 프로시저 스위트를 TDS 네트워크 와이어(`:1433`)와 인프로세스 실행기로 교차 주입하여 RPC 및 프로시저 상태 대칭성 100% 달성 (`SLT-DUALITY-TSQL-WIRE-1`).
    5. **Scenario E (Protocol Dual-Path Symmetry SLT)**: PGWire의 Simple('Q') vs Extended('PBE') 프로토콜, TDS의 SQL Batch vs RPC `sp_executesql` 경로 간의 파라미터 바인딩 및 쿼리 결과셋 대칭성 검증 완료 (`SLT-DUALITY-DUALPATH-1`).
    6. **Scenario F (Cold Compilation vs Hot PlanCache Duality)**: 콜드 파싱/컴파일 경로와 핫 플랜 캐시 경로 간의 실행 결과 대조 및 캐시 오염/뮤테이션 안전성 100% 검증 (`SLT-DUALITY-PLANCACHE-1`).

- **14개 라운드 라이브 엔진 전수 감사 및 500+ 결함 완치 (`notes/reports/20261120_LIVE_AUDIT_ROUNDS_1_TO_14.ko.md`)**:
  - 실제 `psql` 16.15 및 Go 기반 다중 세션 적대적 프로브를 통해 14개 영역에 걸쳐 라이브 감사를 수행하고 발견된 모든 결함을 전수 치료:
    - **Round 1 (T-SQL 프로시저 수명주기)**: `OUTPUT INTO @table_variable` 데이터 유실 방지, `SCOPE_IDENTITY()` 스택 프레임 격리, 로컬 `#temp` 테이블 수명주기 정리, 프로시저 내 CTE/VIEW/MERGE 확장.
    - **Round 2 (수치 및 날짜 산술)**: `DATEADD`, `DATEDIFF`, 부동소수점 승격 및 윈도우 프레임 경계 보정.
    - **Round 3~6 (코어 클러스터)**: DML/JOIN/PK 단락 평가 및 릴레이셔널 연산자 정밀화.
    - **Round 7~8 (내장 함수)**: 시스템 내장 함수 Arity 검증, 날짜 생성자 및 윈도우 집계 정합성.
    - **Round 9 (보안 및 트랜잭션)**: RBAC/DCL 권한 체계 정립, 암호화 및 트랜잭션 격리 강화.
    - **Round 10 (VM 및 커서)**: VDBE 가상 머신 레지스터 재활용, 커서 상태 머신 라이프사이클.
    - **Round 11 (시퀀스 및 인덱스)**: `NEXT VALUE FOR` 동시성 레이스 차단, WiredTiger 인덱스 복합 키 일원화.
    - **Round 12 (JSON 및 데이터 타입)**: JSON strict 모드 RFC 8259 준수, XML 메소드 차단, CONVERT 스타일 정밀화.
    - **Round 13 (집계 및 DML 스코프)**: 지연 역직렬화 정합성, MERGE 카디널리티 및 DML 서브쿼리 스코프 격리.
    - **Round 14 (TDS 및 와이어 프레이밍)**: TDS 대용량 가변길이 컬럼(PLP) 스트리밍 프레이밍 정밀화 및 스칼라 WHERE 1=0 메타데이터 동기화.

- **통합 트랜잭션 관리자(Unified Transaction Manager) 및 세션 수명주기 하드닝**:
  - 와이어 프로토콜 분리나 네트워크 단절 시 발생하던 트랜잭션 핸들 및 락 누수를 완벽 차단.
  - VDBE 가상 머신 컴파일러에서 트랜잭션 제어 구문(`BEGIN`, `COMMIT`, `ROLLBACK`)을 VDBE 바이트코드 분기에서 제외하여 세션 트랜잭션 관리자(`cs.Tx()`)에 바인딩되도록 일원화.

- **Canonical `pkg/sqlutil` 표준화 및 중복 제거 모듈 추상화**:
  - 행/값 중복 제거(`sqlutil.DeduplicateRowsAndValues`)를 모듈화하여 `SELECT DISTINCT` 쿼리의 프로토콜 간 일치성 보장.
  - 쿼리 디코더(`DecodeQueryResult`)에서 중복 컬럼명에 대한 순번 매핑(`col#1`, `col#2`)을 지원하여 셀프 조인 및 복합 집계 쿼리의 컬럼 덮어쓰기 결함 근절.
  - SQL 문자열 조작 시 `strings.*` 직호출을 금지하고 문맥 인식 파서(`pkg/sqlutil`)를 강제하는 Invariant 12 확립.

- **Zero-Tampering 오라클 4계층 봉인 및 1,289개 T-SQL 프로시저 스위트 100% 통과**:
  - SQLLogicTest 409,000+ 쿼리 및 T-SQL 저장 프로시저 1,289개 스위트(37,178개 레코드) 전수 무결점 통과 (0 Data Races).

---

## 2. 세부 변경 사항

### 2.1. 6대 쌍대성(Duality) 교차 검증 하네스 (`ARCH-DUALITY-CROSS-VERIFICATION-1`)
- **Dual-Wire 대칭성 검증 (`SLT-DUALITY-WIRE-1`)**:
  - `pkg/slt/wire_duality_test.go`를 구현하여 동일한 SLT 스크립트를 PGWire와 TDS 클라이언트로 동시 전송하고 응답 바이트를 상호 대조.
  - 10,706개 쿼리에 대해 결과셋, 상태 코드, NULL 정렬 대칭성 100% 입증.
- **Dual-Storage 트윈 엔진 검증 (`SLT-DUALITY-STORAGE-1`)**:
  - `pkg/slt/storage_duality_test.go`를 구현하여 인메모리 OCC 스토리지 커널과 WiredTiger 디스크 B+Tree 엔진 간 21,412개 쿼리 실행 결과 대조.
- **Dual-Parser 구문 분석 검증 (`SLT-DUALITY-PARSER-1`)**:
  - `pkg/proc/parser_duality_test.go`를 구축하여 서브마이크로초 네이티브 Pratt 파서와 ANTLR4 T-SQL 파서 간 구문 트리 생성 일치율 100% 달성.
- **T-SQL 프로시저 와이어 대칭성 (`SLT-DUALITY-TSQL-WIRE-1`)**:
  - `test/slt_proc/proc_wire_duality_test.go`를 통해 30개 코어 저장 프로시저 스위트의 TDS 와이어 실행과 인프로세스 실행 결과의 완전 일치 확인.
- **프로토콜 듀얼 패스 대칭성 (`SLT-DUALITY-DUALPATH-1`)**:
  - `pkg/slt/dualpath_duality_test.go`를 구축하여 PGWire Simple vs Extended 경로, TDS SQL Batch vs RPC `sp_executesql` 경로 간의 파라미터 바인딩 및 쿼리 파이프라인 무결성 검증.
- **플랜 캐시 뮤테이션 안전성 (`SLT-DUALITY-PLANCACHE-1`)**:
  - `pkg/slt/plancache_duality_test.go`를 구축하여 쿼리/프로시저의 콜드 컴파일과 핫 캐시 재실행 간 결과 대칭성 검증 및 AST 변이 위험 원천 차단.

### 2.2. 라이브 엔진 감사 핵심 결함 완치
- **프로시저 DML `OUTPUT INTO @table_variable` 파이프라인 복원**:
  - `pkg/engine/sqlexec_dml.go` 및 `pkg/proc/runtime/vm.go`에 출력 데이터 라우팅을 연결하여 무음 데이터 유실 결함 완치.
- **트리거 `SCOPE_IDENTITY()` 스택 격리**:
  - 중첩 트리거 호출 시 상위 프로시저의 식별자 컨텍스트가 오염되지 않도록 호출 스택별 분리 구현.
- **로컬 `#temp` 테이블 자동 정리**:
  - 세션 내 프로시저 종료 시 임시 테이블이 남지 않도록 unwind 훅 정비.
- **TDS PLP 대용량 스트리밍 정밀화**:
  - 8KB 초과 바이너리/문자열 전송 시 청크 오프셋 및 종료 마커 프레이밍 정비.

### 2.3. 트랜잭션 관리자 및 파서 표준화
- **세션 트랜잭션 제어 일원화**:
  - `BEGIN TRANSACTION`, `COMMIT`, `ROLLBACK` 구문이 VDBE 임시 트랜잭션이 아닌 세션의 전용 트랜잭션 관리자(`TxManager`)에 귀속되도록 수정.
- **모듈러 중복 제거 (`pkg/sqlutil/dedup.go`)**:
  - `sqlutil.DeduplicateRowsAndValues` 모듈을 도입하여 VDBE 릴레이셔널 실행 경로에서 중복 행 필터링 보장.
- **Context-Blind `strings.*` SQL 파싱 퇴출**:
  - 원시 문자열 함수 대신 `sqlutil.FindTopLevelKeyword`, `sqlutil.SplitTopLevel` 등 문맥 인식 파서 프리미티브로 전면 교체.

---

## 3. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - macOS (`darwin-arm64`), Linux (`linux-amd64`), Windows (`windows-amd64`) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/... ./test/e2e/...` 100% 통과 (0 Data Races).
2. **릴리즈 게이트 전수 통과**:
   - `[Gate 0/6]` Dual-AI 사전 릴리스 감사 통과 (0 미해결 P0 결함).
   - `[Gate 0.5/6]` Zero-Tampering 오라클 불변식 검증 완료 (1,289/1,289 파일 해시 일치).
   - `[Gate 1/6]` 코어 단위, 회귀, E2E 및 레이스 테스트 100% 통과.
   - `[Gate 2/6]` 13개 도메인 확장 스모크 및 라이브 psql CLI 8개 메타 커맨드 통과.
   - `[Gate 3/6]` TPC-C 벤치마크 고부하 검증 통과 (>600k tpmC).
   - `[Gate 4/6]` SQLLogicTest 409k+ 쿼리 100% 일치.
   - `[Gate 5/6]` T-SQL 저장 프로시저 1,289개 스위트(37,178개 레코드) 100% 통과.
   - `[Gate 6/6]` 크래시 내구성(CrashTest) 및 장기 리소스 위생(SoakTest) 무결함 검증.
