# LionSQL v0.90.0 릴리즈 노트 (`notes/RELEASE_NOTES_v0.90.0.ko.md`)

> - **릴리즈 예정**: `v0.90.0` (Official GA)
> - **버전**: `v0.90.0`
> - **마일스톤 주제**: **내구성 및 신뢰성 극한 검증 (Durability & Reliability Hardening), 코어 동시성 결함 완치, 그리고 8대 에코시스템 연동 완주**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.90.0`은 단순한 기능 추가를 넘어, 1.0 프로덕션 릴리즈를 향한 **스토리지 엔진의 내구성(Durability), 프로세스 강제 종료(`kill -9`) 복구력, 동시성 격리 무결성(Zero Torn Reads), 그리고 SQL 표준 3치 논리 오라클 검증**을 달성한 기념비적인 공식 릴리즈입니다.

- **100/100 라운드 크래시 무손실 복구 (`crashtest`)**: WiredTiger 50회 + In-Memory OCC 50회 강제 종료(`kill -9`, `DOUBLE_KILL`, `TORN_WAL_TAIL`) 환경에서 **총 340,934건의 트랜잭션 100% 무손실 복구** 입증.
- **장시간 다중 워커 소크 테스트 완주 (`soaktest`)**: 8워커 연속 부하 환경에서 WiredTiger 및 In-Memory OCC 엔진의 **자원 누수 0(RSS/FD/고루틴 비율 1.00 유지) 및 트랜잭션 불변식(`SUM(balance)=1,000,000`) 100% 보존**.
- **SQLLogicTest 블랙박스 오라클 스위트 편입 (`sqllogictest`)**: SQLite 호환 표준 SLT 러너를 엔진에 내장하고, 코어 대수식/서브쿼리/CASE/DISTINCT/윈도우 함수/CTE/3치 논리/셀프 및 크로스 조인/JSON/Upsert/공백 경계 26개 파일(182개 문장, 149개 쿼리) 100% 오라클 일치 검증.
- **코어 커널 2대 잠재 동시성 결함 완치**: WiredTiger 세션 간 커서 비동기 해제 `SIGSEGV` 완치 및 In-Memory OCC `Scan()` 카노니컬 오름차순 샤드 RLock 스냅샷 도입으로 찢긴 읽기(Torn Read) 원천 차단.
- **외부 8대 실서비스 프로덕션 연동 완성**: Grafana, Casdoor, Apache Answer, Vaultwarden, Metabase BI, Umami, Woodpecker CI, Vikunja 연동 완주.

---

## 2. 세부 변경 사항

### 2.1. 내구성 및 신뢰성 검증 도구 구축 (Reliability Harnesses)
- **`cmd/crashtest` (kill -9 크래시 복구 하네스)**:
  - 다중 워커 계좌 이체 중 임의 시점 `SIGKILL`, 복구 도중 재크래시(`SIGKILL+DOUBLE_KILL`), WAL 로그 꼬리 절단 손상 복구(`SIGKILL+TORN_WAL_TAIL`), `SIGTERM` 대조군을 포함한 극한의 크래시 시나리오 하네스 구축.
  - SHA-256 커밋 장부(`crash_ledger`) 대조 기반 4단계 무결성 오라클(D1 커밋 내구성, D2 원자성, D3 체크섬 일관성, D4 복구 가능성) 구현.
  - WiredTiger(50회, 170,477 tx) 및 In-Memory OCC(50회, 170,457 tx) 누적 100라운드 100% 무손실 통과.
- **`cmd/soaktest` (장시간 부하 및 자원 누수 자동 판정 도구)**:
  - 8워커 계좌 이체 + 연결 처닝(Churn) + 실시간 불변식 검증 + Prometheus 지표 수집 연동.
  - 워밍업 이후 RSS 메모리, 고루틴 수, 열린 파일 디스크립터(FD), p99 지연시간의 선형 열화 여부를 판단하는 자동 판정 엔진 내장.
  - WiredTiger 1시간 실측(`39.3만` 행 보존, FD 28개 완전 고정) 및 In-Memory OCC 스모크 완주.
- **`cmd/sqllogictest` & `pkg/slt` (SQLite 호환 블랙박스 오라클 러너)**:
  - 순수 Go 기반 표준 SLT 파일 파서, In-Process 및 PGWire 듀얼 모드, rowsort/valuesort/nosort 정렬 및 MD5 해시 대조 엔진 구현.
  - 코어 로직(`select1`, `where`, `join`, `null`, `aggregate`), 고난도 기능(`case_expr`, `distinct`, `subquery`, `window`, `setops`, `functions`, `dml_mutate`, `between`, `in_list`, `like`, `group_having`, `limit_offset`, `cte`, `arithmetic`, `cast_type`, `boolean_3vl`, `join_advanced`, `json_ops`, `order_upsert`), 공백/경계 조건(`boundary`, `whitespace`) 총 26개 파일(182개 문장, 149개 쿼리) 양쪽 스토리지 엔진 100% PASS.

---

### 2.2. 스토리지 엔진 코어 결함 완치 (Critical Kernel Bug Fixes)
- **WiredTiger 세션 간 커서 비동기 해제 동시성 `SIGSEGV` 완치**:
  - DDL(`DROP TABLE`) 실행 시 타 세션 OS 스레드에 바인딩된 커서를 직접 닫으려다 발생하던 WiredTiger C 커널의 스레드 위반 크래시(`addr=0xd8`) 발굴.
  - 세션별 `toClose` 지연 정리 큐 및 `go_wt_session_drop(..., "force=true")` 안전 드롭 메커니즘을 적용하여 완치.
- **In-Memory OCC `Scan()` 찢긴 읽기(Torn Read) 완치**:
  - 테이블 스캔 중 256개 샤드를 순차 락/해제하며 단일 행 단위로 조회하던 기존 구조로 인해, 동시 이체 중 잔액 합산(`SELECT SUM(balance)`) 시 찢긴 읽기가 발생하던 스냅샷 분리 결함 발굴.
  - 카노니컬 오름차순 샤드 RLock 기반 `snapshotRowsAndKeys()` 일괄 원자적 스냅샷 캡처를 도입하여 동시성 스캔 무결성 100% 확보.
- **WiredTiger `WT_ROLLBACK` 충돌 직렬화 오류 매핑**:
  - C 커널의 쓰기-쓰기 충돌(`WT_ROLLBACK`, -31800)을 `storage.ErrSerializationFailure`로 래핑하여 PGWire `SQLSTATE 40001` 표준 오류로 전달.
- **크래시 복구 엣지 케이스 3건 완치**:
  - WiredTiger 유저 버퍼 유실 방지(`transaction_sync`), `CREATE TABLE` 제약 조건 접두사 충돌(`checksum` 누락 방지), PGWire 텍스트 파라미터 숫자 오인 방지.

---

### 2.3. SQL 파서, AST, 3치 논리 및 표현식 고도화 (Parser & Logic)
- **함수 내 `DISTINCT` 집계 지원**:
  - `COUNT(DISTINCT col)`, `SUM(DISTINCT col)` 지원 및 `FunctionCallExpr.Distinct` AST 필드 반영으로 중복 제거 집계 평가 결함 완치.
- **SQL 3치 논리(Three-Valued Logic) 정합성 완치**:
  - `NULL = NULL`, `val = NULL` 비교뿐만 아니라 `NOT BETWEEN` 및 `NOT IN` (리스트 내 `NULL` 원소 포함) 평가 시 `NULL` 피연산자가 `true`로 오판되지 않고 항상 UNKNOWN(falsy)으로 평가되도록 런타임 비교 로직 정합성 확립.
- **AST Deparser 연산자 결합 우선순위 보존**:
  - `(A OR B) AND C` deparsing 시 연산자 우선순위에 따른 부모 괄호 탈락 버그 완치.
- **화이트스페이스 불변 키워드 스캐너 (`sqlutil`)**:
  - 탭/개행/다중 공백이 포함된 복합 키워드(`IF EXISTS`, `WITHIN GROUP`, `CREATE TABLE` 등)를 식별자 분리 없이 안전하게 토큰화하는 `ScanKeyword`, `ContainsKeyword`, `HasPrefixKeyword` 단일 소스(SSOT) 구축.
  - 코드베이스 전반의 단순 문자열 매칭(`strings.Contains(" OR ")`) 안티패턴 전면 제거.
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
- **PGWire Prepared Statement VDBE 직결 바인딩 (`PERF-PGWIRE-PREP-TX`)**: 트랜잭션 내 다중 실행 쿼리의 파싱 오버헤드 제거.
- **PGWire 32KB 출력 버퍼 풀링 & `SELECT 1` 제로 파싱 패스트패스**.
- **TPC-C 3.6 클라이언트 대칭 벤치마크 매트릭스**: 32/100/200 워커 대상 WiredTiger vs In-Memory OCC 정밀 실측치 갱신.

---

## 3. 검증 지표 요약

| 검증 영역 | 테스트 스위트 | 실행 규모 | 주요 지표 / 결과 | 판정 |
| :--- | :--- | :--- | :--- | :---: |
| **크래시 복구** | `crashtest` (WiredTiger) | 50 라운드 (kill -9) | 170,477 tx 복구 / 데이터 손실 0건 | **100% PASS** |
| **크래시 복구** | `crashtest` (In-Memory OCC) | 50 라운드 (kill -9) | 170,457 tx 복구 / 데이터 손실 0건 | **100% PASS** |
| **장시간 소크** | `soaktest` (WiredTiger) | 8 워커 연속 부하 | 잔액 불변 유지 / 열린 FD 28개 고정 (누수 0) | **100% PASS** |
| **장시간 소크** | `soaktest` (In-Memory OCC) | 8 워커 연속 부하 | 잔액 불변 유지 / RSS 비율 1.00 (누수 0) | **100% PASS** |
| **블랙박스 오라클** | `sqllogictest` | 26개 파일 / 149개 쿼리 | 0 불일치 / 11대 엔진 결함 완치 | **100% PASS** |
| **데이터 레이스** | `go test -race ./...` | 전체 패키지 전수 | 데이터 레이스 0건 / 전 패키지 통과 | **100% PASS** |
