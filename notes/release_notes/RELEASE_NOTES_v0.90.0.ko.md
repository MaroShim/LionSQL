# LionSQL v0.90.0 릴리즈 노트 (`notes/RELEASE_NOTES_v0.90.0.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **최종 갱신일**: 2026-11-03
> - **릴리즈 버전**: `v0.90.0` (Official GA)
> - **마일스톤 주제**: **내구성 및 신뢰성 극한 검증 (Durability & Reliability Hardening), 코어 동시성 결함 완치, 그리고 8대 에코시스템 연동 완주**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.90.0`은 단순한 기능 추가를 넘어, 1.0 프로덕션 릴리즈를 향한 **스토리지 엔진의 내구성(Durability), 프로세스 강제 종료(`kill -9`) 복구력, 동시성 격리 무결성(Zero Torn Reads), 그리고 SQL 표준 3치 논리 오라클 검증**을 달성한 기념비적인 공식 릴리즈입니다.

- **100% 무손실 크래시 복구 검증 (`cmd/crashtest`)**:
  - WiredTiger 및 In-Memory OCC 양대 스토리지 엔진 대상 강제 종료(`SIGKILL`, `DOUBLE_KILL`, `TORN_WAL_TAIL`, `SIGTERM`) 환경에서 **누적 수십만 건(최신 스모크 12.6만 건 포함)의 트랜잭션 100% 무손실 복구** 입증.
  - SHA-256 커밋 장부(`crash_ledger`) 기반 4대 오라클(D1 커밋 내구성, D2 원자성 `SUM(balance)=const`, D3 체크섬 일관성, D4 복구 가능성) 100% PASS.
- **장시간 다중 워커 소크 테스트 완주 (`cmd/soaktest`)**:
  - 8워커 연속 트랜잭션 부하 환경에서 WiredTiger 및 In-Memory OCC 엔진의 **자원 누수 0(RSS/FD/고루틴 비율 1.00 유지) 및 트랜잭션 불변식(`SUM(balance)=1,000,000`) 100% 보존**.
- **SQLLogicTest 표준 스위트 전수 통과 (`cmd/sqllogictest` & `pkg/slt`)**:
  - 순수 Go 기반 SQLite 호환 표준 SLT 러너를 엔진에 내장하고, **90개 표준 테스트 파일(수만 개 쿼리) 100% PASS (0건 실패)** 달성.
  - 대수식/서브쿼리/CASE/DISTINCT/윈도우 함수/CTE/3치 논리/셀프 및 크로스 조인/JSON/Upsert/공백 경계 완벽 검증.
- **초고속 트랜잭션 처리량 달성 (`cmd/tpcc_runtime`)**:
  - In-Memory OCC 10 Warehouses / 10 Workers 기준 **732,956 tpmC (27,118.95 TPS)** 달성.
  - New-Order P50 0.30ms / P99 2.79ms, Payment P50 0.08ms / P99 1.29ms, 직렬화 실패율 0.00% (40.7만 회 시도 중 11회 취소) 기록.
- **코어 커널 잠재 동시성 결함 완치**:
  - WiredTiger 세션 간 커서 비동기 해제 `SIGSEGV` 완치 및 `transaction_sync=(enabled=true,method=none)` 크래시 내구성 복원.
  - In-Memory OCC `Scan()` 카노니컬 오름차순 샤드 RLock 스냅샷 도입으로 찢긴 읽기(Torn Read) 원천 차단.
  - `parseInCondition` 복합 조건 오인 가드 구축 및 회귀 테스트 영구 수립.
- **외부 8대 실서비스 프로덕션 연동 완성**:
  - Grafana, Casdoor, Apache Answer, Vaultwarden, Metabase BI, Umami, Woodpecker CI, Vikunja 연동 완주.

---

## 2. 세부 변경 사항

### 2.1. 내구성 및 신뢰성 검증 도구 구축 (Reliability Harnesses)
- **`cmd/crashtest` (kill -9 크래시 복구 하네스)**:
  - 다중 워커 계좌 이체 중 임의 시점 `SIGKILL`, 복구 도중 재크래시(`SIGKILL+DOUBLE_KILL`), WAL 로그 꼬리 절단 손상 복구(`SIGKILL+TORN_WAL_TAIL`), `SIGTERM` 대조군을 포함한 극한의 크래시 시나리오 하네스 구축.
  - SHA-256 커밋 장부(`crash_ledger`) 대조 기반 4단계 무결성 오라클(D1 커밋 내구성, D2 원자성, D3 체크섬 일관성, D4 복구 가능성) 구현.
  - 양대 스토리지 엔진(WiredTiger, In-Memory OCC) 대상 누적 100% 무손실 통과.
- **`cmd/soaktest` (장시간 부하 및 자원 누수 자동 판정 도구)**:
  - 8워커 계좌 이체 + 연결 처닝(Churn) + 실시간 불변식 검증 + Prometheus 지표 수집 연동.
  - 워밍업 이후 RSS 메모리, 고루틴 수, 열린 파일 디스크립터(FD), p99 지연시간의 선형 열화 여부를 판단하는 자동 판정 엔진 내장.
  - 장기 부하 테스트 시 잔액 불변 유지, FD 고정(누수 0), p99 지연시간 열화 0 입증.
- **`cmd/sqllogictest` & `pkg/slt` (SQLite 호환 블랙박스 오라클 러너)**:
  - 순수 Go 기반 표준 SLT 파일 파서, In-Process 및 PGWire 듀얼 모드, rowsort/valuesort/nosort 정렬 및 MD5 해시 대조 엔진 구현.
  - 코어 로직(`select1`~`select5`), 고난도 기능(`case_expr`, `distinct`, `subquery`, `window`, `setops`, `functions`, `dml_mutate`, `between`, `in_list`, `like`, `group_having`, `limit_offset`, `cte`, `arithmetic`, `cast_type`, `boolean_3vl`, `join_advanced`, `json_ops`, `order_upsert`), 공백/경계 조건 등 총 90개 표준 파일 100% PASS.
  - SQLite 전용 비표준/퍼징 스위트 14개 파일은 `test/sqlite_fuzz/`로 분리 격리하여 릴리즈 게이트 신뢰성 확보.

---

### 2.2. 스토리지 엔진 코어 결함 완치 (Critical Kernel Bug Fixes)
- **WiredTiger 세션 간 커서 비동기 해제 동시성 `SIGSEGV` 완치**:
  - DDL(`DROP TABLE`) 실행 시 타 세션 OS 스레드에 바인딩된 커서를 직접 닫으려다 발생하던 WiredTiger C 커널의 스레드 위반 크래시(`addr=0xd8`) 발굴.
  - 세션별 `toClose` 지연 정리 큐 및 `go_wt_session_drop(..., "force=true")` 안전 드롭 메커니즘을 적용하여 완치.
- **In-Memory OCC `Scan()` 찢긴 읽기(Torn Read) 완치**:
  - 테이블 스캔 중 256개 샤드를 순차 락/해제하며 단일 행 단위로 조회하던 기존 구조로 인해, 동시 이체 중 잔액 합산(`SELECT SUM(balance)`) 시 찢긴 읽기가 발생하던 스냅샷 분리 결함 발굴.
  - 카노니컬 오름차순 샤드 RLock 기반 `snapshotRowsAndKeys()` 일괄 원자적 스냅샷 캡처를 도입하여 동시성 스캔 무결성 100% 확보.
- **WiredTiger 크래시 동기화 보장 (`transaction_sync`)**:
  - WiredTiger 인메모리 유저 로그 버퍼가 SIGKILL 시 유실되지 않도록 `transaction_sync=(enabled=true,method=none)` 설정을 복원하여 D1 커밋 내구성 보장.
- **`parseInCondition` 복합 조건 탈취 결함 완치**:
  - 복합 조건식(`col1 IN (1, 2) AND (col2 = 3)`) 파싱 시 후속 괄호 표현식을 단일 IN 조건으로 오인하던 결함을 매칭 괄호 인덱스 검사 및 단일 컬럼 유효성 검증을 통해 완치하고 영구 회귀 테스트 구축.
- **WiredTiger `WT_ROLLBACK` 충돌 직렬화 오류 매핑**:
  - C 커널의 쓰기-쓰기 충돌(`WT_ROLLBACK`, -31800)을 `storage.ErrSerializationFailure`로 래핑하여 PGWire `SQLSTATE 40001` 표준 오류로 전달.

---

### 2.3. SQL 파서, AST, 3치 논리 및 아키텍처 개선 (Parser & Logic)
- **정규식 파싱 전면 제거 및 모듈식 조건 파서 추상화 (`[RULE:ModularAbstraction]`, Invariant 7 & 8)**:
  - 중첩 괄호 파싱 및 SQL 절 분석에서 취약한 정규 표현식을 완전히 배제하고, `pkg/sqlutil` 내 결정론적 스캐너 및 파서(`FindMatchingParen`, `ScanKeyword`, `HasPrefixKeyword` 등)로 중앙 표준화.
- **불변 룰 슬러그 체계 정착**:
  - 변경 가능한 가변 순번 대신 불변 슬러그(`[RULE:RadicalKISS]`, `[RULE:PipelineVerification]` 등)를 엔지니어링 표준으로 확립.
- **함수 내 `DISTINCT` 집계 지원**:
  - `COUNT(DISTINCT col)`, `SUM(DISTINCT col)` 지원 및 `FunctionCallExpr.Distinct` AST 필드 반영으로 중복 제거 집계 평가 결함 완치.
- **SQL 3치 논리(Three-Valued Logic) 정합성 완치**:
  - `NULL = NULL`, `val = NULL` 비교뿐만 아니라 `NOT BETWEEN` 및 `NOT IN` (리스트 내 `NULL` 원소 포함) 평가 시 `NULL` 피연산자가 `true`로 오판되지 않고 항상 UNKNOWN(falsy)으로 평가되도록 런타임 비교 로직 정합성 확립.
- **AST Deparser 연산자 결합 우선순위 보존**:
  - `(A OR B) AND C` deparsing 시 연산자 우선순위에 따른 부모 괄호 탈락 버그 완치.
- **AST 및 카탈로그 기반 라우팅 리팩토링 (`ROUTING-AST-CATALOG-1`)**:
  - 45개의 문자열 휴리스틱 매칭 코드를 AST 구조체 및 카탈로그 심볼 해석 기반으로 전면 전환.

---

### 2.4. 외부 실서비스 생태계 연동 확장 (Ecosystem Integrations)
- **Grafana (`GRAFANA-INTEGRATION-1`)**: PostgreSQL & TDS(MSSQL) 듀얼 프로토콜 대시보드 연동 가이드 및 호환성 완료.
- **Casdoor (`CASDOOR-INTEGRATION-1`)**: 글로벌 IAM/SSO 복합 기본키(Composite PK, `\x1f` 구분자) 및 TDS varbinary 왕복 호환 완료.
- **Apache Answer (`INTEG-ANSWER-1`)**: 개발자 Q&A 지식인 플랫폼 PGWire E2E 연동 및 테이블 마이그레이션 호환 완료.
- **Vaultwarden (`INTEG-VAULTWARDEN-1`)**: Rust Diesel ORM 기반 패스워드 매니저 연동 및 동기화 API 호환 완료.
- **Metabase (`INTEG-METABASE-1`)**: 엔터프라이즈 BI 분석 플랫폼 듀얼 프로토콜 연동 설계 및 호환 완료.
- **Umami (`INTEG-UMAMI-1`)**: 프라이버시 친화적 웹 분석 플랫폼 Prisma ORM 연동 완료.
- **Woodpecker CI & Vikunja (`INTEG-WOODPECKER-1`, `INTEG-VIKUNJA-1`)**: 경량 CI/CD 및 태스크 관리 플랫폼 연동 완료.
- **IOTA-SDK**: Go ERP 프레임워크 연동 및 PostgreSQL DDL/프로토콜 호환 완료.

---

### 2.5. 성능 최적화 및 벤치마크 (Performance & Benchmark)
- **In-Memory OCC TPC-C 극한 성능 입증**: 10W/10Workers 환경에서 **732,956 tpmC / 27,118.95 TPS** 달성 (New-Order P50 0.30ms, P99 2.79ms).
- **PGWire Prepared Statement VDBE 직결 바인딩 (`PERF-PGWIRE-PREP-TX`)**: 트랜잭션 내 다중 실행 쿼리의 파싱 오버헤드 제거.
- **PGWire 32KB 출력 버퍼 풀링 & `SELECT 1` 제로 파싱 패스트패스**.
- **성능 회귀 게이트 구축 (`PERF-REGRESSION-RECOVERY-1`)**: 1,000건 ACID 트랜잭션 0.12초 완주를 상시 검증하는 경량 게이트 수립.

---

## 3. 공식 검증 지표 성적표 (Scorecard)

| 검증 영역 | 테스트 스위트 | 실행 규모 | 주요 실측 지표 / 결과 | 최종 판정 |
| :--- | :--- | :--- | :--- | :---: |
| **SQLLogicTest 표준 스위트** | `cmd/sqllogictest` | 90개 표준 파일 전수 | 0 불일치 / 표준 SQL 전 영역 커버 | **100% PASS** |
| **크래시 내구성** | `cmd/crashtest` (WiredTiger) | 20 라운드 (kill -9/DOUBLE/TORN) | 63,310 tx 복구 / 데이터 손실 0건 / 4대 오라클 충족 | **100% PASS** |
| **크래시 내구성** | `cmd/crashtest` (In-Memory OCC) | 20 라운드 (kill -9/DOUBLE/TORN) | 62,844 tx 복구 / 데이터 손실 0건 / 4대 오라클 충족 | **100% PASS** |
| **장시간 자원 소크** | `cmd/soaktest` (In-Memory OCC) | 8 워커 연속 부하 (200 ops/s) | 잔액 불변식 일치 / RSS·FD·고루틴 비율 1.00 고정 (누수 0) / p99 열화 0 | **100% PASS** |
| **TPC-C OLTP 처리량** | `cmd/tpcc_runtime` (OCC) | 10W / 10 Workers / 15s | **732,956 tpmC** / **27,118.95 TPS** (P50 0.30ms, P99 2.79ms) | **100% PASS** |
| **13-Domain 스모크 검증** | `scripts/verify_0.90.sh` (Stage 4) | In-Memory & WiredTiger 양대 엔진 | 13/13 도메인 정상 통과 | **100% PASS** |
| **데이터 레이스 무결성** | `go test -race -short ./...` | 22개 패키지 전수 | 데이터 레이스 0건 / 패키지 전수 통과 | **100% PASS** |
