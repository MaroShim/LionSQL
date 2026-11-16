# LionSQL v0.93.0 릴리즈 노트 (`notes/release_notes/RELEASE_NOTES_v0.93.0.ko.md`)

> - **최초 작성일**: 2026-11-16
> - **릴리즈 버전**: `v0.93.0` (Minor Release)
> - **마일스톤 주제**: **Dual-AI 사전 릴리스 적대적 감사 및 22개 결함 완치, Zero-Tampering 오라클 4계층 봉인, 6대 오픈소스 드라이버 코퍼스 100% 정합성, 모델 B 논리 3단 계층(Database.Schema.Table) 복합 키 공식화, 및 VDBE 정적 준비 구문(sp_prepare) 탑재**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.93.0` 버전은 228개 누적 커밋에 대한 Dual-AI 전수 적대적 감사를 통해 엔진 무결성을 극한으로 끌어올리고, 6대 글로벌 오픈소스 드라이버 코퍼스 및 1,289개 T-SQL 저장 프로시저 오라클에 대한 100% 실측 정합성을 증명한 엔터프라이즈 마일스톤 릴리즈입니다:

- **Dual-AI 사전 릴리스 적대적 감사 프로토콜 및 22개 결함 완치 (`doc/RELEASE_AUDIT_PROTOCOL.ko.md`)**:
  - `v0.92.0..HEAD` 전 소스코드 변경분에 대해 2개의 독립 AI 에이전트가 라인 단위로 적대적 교차 감사를 수행하는 4단계 감사 체계(독립 감사 ➔ 원탁 조정 ➔ TDD 패치 ➔ 불변식 검증)를 공식 가동.
  - 전역 스캔을 통해 식별된 22건의 잠재 결함(VDBE VM 바이트코드, TDS 프로토콜 프레이밍, CBO 플래너, 트랜잭션 격리 전반)을 전수 완치하고 `notes/reports/AUDIT_v0.93.0_DUAL_ADVERSARIAL_LEDGER.ko.md`에 완결 기록.

- **Zero-Tampering 오라클 불변식(`[RULE:ZeroOracleTampering]`, Invariant 16) 4계층 방어막 확립**:
  - 외부 오라클 테스트 스위트 및 정답지를 임의 변조하거나 약화시키는 행위를 원천 차단하는 4단계 방어 체계 구축 (`chmod 444` 파일시스템 락, SHA-256 해시 검증, 프리커밋 파이어월, `GEMINI.md` 헌법 영구 봉인).

- **Microsoft SQL Server 정통 드라이버 & ORM 코퍼스 6종 100% PASS (1,289 스위트)**:
  - 인위적 패딩(Synthetic Padding)을 완전히 배제하고, 실무 글로벌 에코시스템 드라이버 및 ORM 코퍼스를 직수입하여 T-SQL VM 런타임의 완전한 실무 상호운용성을 실증:
    - **AdventureWorksLT (실무 엔터프라이즈)**: 스키마, 시드, 복합 쿼리 4개 스위트 **100% PASS**.
    - **GORM SQL Server (Go 공식 ORM 드라이버)**: 3개 스위트 **100% PASS**.
    - **mssql-jdbc (Microsoft 공식 Java 드라이버)**: 39개 스위트 **100% PASS**.
    - **pymssql (Python 드라이버)**: 10개 스위트 **100% PASS**.
    - **sqlglot (SQL 방언 트랜스파일러)**: 57개 스위트 **100% PASS**.
    - **tedious (Node.js 공식 드라이버)**: 36개 스위트 **100% PASS**.
    - **전체 1,289개 스위트(37,178개 레코드)**를 단 **2.0초** 만에 완주하며 압도적인 T-SQL VM 실행 속도 증명.

- **VDBE VM 정적 준비 구문(`sp_prepare`, `sp_execute`, `sp_unprepare`) 및 매개변수 가드 탑재**:
  - `sp_prepare` 호출 시 VDBE VM 바이트코드 프로그램을 사전에 컴파일하여 세션 캐시에 보관하고 고유 핸들(`@handle INT`)을 반환하는 메커니즘 구현.
  - `sp_execute` 실행 시 명명된 매개변수 뒤에 위치 매개변수가 오는 문법 위반(Msg 119) 및 동일 매개변수 다중 공급(Msg 8143)을 Fail-Fast로 차단하는 정합성 가드 탑재.
  - 배치 문장에서 세미콜론 없는 `EXEC` 뒤 줄바꿈 분할(`SplitStatements`) 안정화.

- **모델 B 논리 3단 계층(`Database.Schema.Table`) 아키텍처 공식 채택 (`ARCH-LOGICAL-NAMESPACE-1`, Invariant 2)**:
  - 2PC 분산 트랜잭션 관리자, 락 계층 교착, 캐시 미스를 유발하는 스토리지 물리 파티셔닝을 영구 배제(`Spec-Out`).
  - 단일 고성능 스토리지 커널 위에 `Database.Schema.Table`을 논리 복합 키(`__db_<dbname>_<schema>_<tablename>`)로 관리하여, 서로 다른 DB 간 동일 테이블명(`SalesDB.dbo.Users` vs `AuthDB.dbo.Users`)이 물리 충돌 없이 완벽히 공존하도록 보장.
  - 서브마이크로초 인메모리/로컬 B-Tree 속도로 로컬 크로스 DB 조인(`SELECT ... FROM DB1.dbo.A JOIN DB2.dbo.B`) 지원 확립.

- **WiredTiger V2 물리 바이너리 튜플 코덱 (Invariant 13) 및 비동기 워커 하드닝**:
  - 4바이트 정렬 고정 헤더, `NullBitmap`, 최대 1,024개 컬럼 상한 검증(`storage.MaxColumnsPerTable = 1024`), 레거시 V1 포맷 Fallback 완전 제거.
  - DatabaseMail 비동기 워커 `sync.Once` 스레드 안전 초기화 적용 및 Coordinator 미사용 필드 정리.

---

## 2. 세부 변경 사항

### 2.1. Dual-AI 사전 릴리스 적대적 감사 및 코어 결함 완치
- **적대적 교차 감사 하네스 (`scripts/audit_diff.sh` & `doc/RELEASE_AUDIT_PROTOCOL.ko.md`)**:
  - 릴리스 전 2개 독립 AI가 코드베이스 변경분을 독립적으로 스캔하고, P0/P1/P2 결함을 대조하여 만장일치로 합의된 결함 장부(`AUDIT_<TAG>_CONSOLIDATED.ko.md`)를 도출하는 감사 파이프라인 정착.
- **CBO 플래너 및 다중 절 스캐너 고도화**:
  - 단일 패스 구문 스캐너(`One-Pass Clause Scanner`) 도입으로 불필요한 반복 문자열 파싱 제거 및 서브쿼리 비상관화 정밀도 향상.
  - CBO 테이블 통계 Bounded Live Row Count 추정기 안정화로 대용량 테이블 스캔 시 메모리 상한선 준수.
- **TDS 프로토콜 프레이밍 및 세션 하드닝**:
  - TDS 대용량 가변길이 컬럼(PLP) 스트리밍 시 바이트 오프셋 계산 정밀화.
  - 미인증 세션의 SQL Batch 임의 실행 방지 및 연결 해제 시 트랜잭션 드레인 안전성 강화.

### 2.2. Zero-Tampering 오라클 4계층 봉인 및 6대 코퍼스 100% 정합성
- **오라클 불변식 영구 봉인 (`[RULE:ZeroOracleTampering]`)**:
  - `test/slt_proc/CHECKSUMS.sha256`에 등록된 1,289개 스위트의 SHA-256 해시를 빌드 및 CI 단계에서 자동 대조.
  - 임의의 `.slt` 파일 수정 시 `verify_no_slt_mutation.sh`에 의해 커밋 및 릴리스가 Fail-Closed로 즉시 차단되는 안전장치 확립.
- **6대 글로벌 오픈소스 드라이버 및 실무 코퍼스 정합성 완주**:
  - AdventureWorksLT 실무 4대 스위트(스키마, 시드, 주문/고객 뷰, 복합 서브쿼리) 100% PASS.
  - `gorm-sqlserver` 3개 스위트, `mssql-jdbc` 39개 스위트, `pymssql` 10개 스위트, `sqlglot` 57개 스위트, `tedious` 36개 스위트 전수 통과.
  - T-SQL VM에서 로컬 변수, `OUTPUT` 매개변수, 다중 결과셋(Multiple Result Sets), 트랜잭션 롤백 라이프사이클을 완벽 검증.

### 2.3. VDBE 런타임 저장 프로시저 및 준비 구문 고도화
- **정적 준비 구문 세션 캐싱 (`sp_prepare`, `sp_execute`, `sp_unprepare`)**:
  - `sp_prepare` 호출 시 파싱 및 바이트코드 컴파일 결과를 세션 내부 캐시(`cs.PreparedStmts`)에 저장하고 정수 핸들을 반환.
  - 바이트코드 VM 내부에서 `sp_execute` 실행 시 사전 컴파일된 프로그램을 인라인 재사용하여 반복 쿼리 실행 지연시간 대폭 단축.
- **매개변수 순서 및 다중 바인딩 가드 (Msg 119 / Msg 8143)**:
  - 위치 매개변수(`val`)가 명명된 매개변수(`@p = val`) 뒤에 오는 것을 원천 차단 (`Msg 119: Must pass parameter N and subsequent parameters as '@name = value'`).
  - 동일한 매개변수가 위치 및 이름으로 중복 전달될 경우 즉시 차단 (`Msg 8143: Parameter was supplied multiple times`).
- **세미콜론 없는 다중 EXEC 배치 파싱 (`SplitStatements`)**:
  - 배치 스크립트에서 세미콜론 없이 개행문자로 분리된 연속 `EXEC` 문을 문맥 인식 기반으로 정확히 분할하도록 개선.

### 2.4. 모델 B 논리 3단 계층(Database.Schema.Table) 복합 키 아키텍처
- **물리 네임스페이스 파티셔닝 영구 배제 (Invariant 2)**:
  - B-Tree 스토리지 엔진 다중화로 인한 2PC 분산 트랜잭션 오버헤드, 캐시 단편화, 엔진 간 락 경합 문제를 사전에 원천 차단.
- **단일 엔진 기반 논리 복합 키 매핑**:
  - 카탈로그 내부 식별자를 `__db_<dbname>_<schema>_<tablename>` 포맷으로 단일화하여 단일 스토리지 커널 안에서 여러 데이터베이스의 동명 테이블이 안전하게 공존.
  - `SalesDB.dbo.Orders`와 `HRDB.dbo.Employees` 간의 크로스 DB 조인을 추가 네트워크/2PC 없이 서브마이크로초 로컬 해시 조인으로 고속 처리.

### 2.5. 스토리지 물리 포맷 고도화 및 비동기 워커 하드닝
- **WiredTiger V2 물리 바이너리 튜플 코덱 확립 (Invariant 13)**:
  - 4바이트 정렬 고정 헤더(`Version 0x02`, `Flags`, `encodedCols`), 1바이트당 8컬럼 매핑 `NullBitmap`, 고정/가변길이 밀집 페이로드 구조 확정.
  - 테이블당 최대 1,024개 컬럼 상한선(`storage.MaxColumnsPerTable = 1024`) 검증 및 레거시 V1 포맷 Fallback 완전 제거.
- **DatabaseMail 비동기 워커 격리 (`pkg/engine/databasemail.go`)**:
  - 메일러 초기화 로직에 `sync.Once`를 적용하여 고동시성 환경에서의 중복 초기화 방지 및 경합 제거.
  - Coordinator 내 불필요한 미사용 상태 필드 정리.

---

## 3. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - macOS (`darwin-arm64`), Linux (`linux-amd64`), Windows (`windows-amd64.exe`) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - `go test -tags wiredtiger -count=1 -race ./pkg/...` 23개 코어 패키지 전수 100% PASS (0 data races).
2. **관계형 오라클 및 저장 프로시저 전수 검증**:
   - SQLLogicTest 90개 표준 파일, 409,196개 쿼리 100% 일치 (0 해시 오차).
   - T-SQL 저장 프로시저 및 6대 코퍼스 1,289개 스위트(37,178개 레코드) 2.0초 만에 100% Conformance 통과.
3. **무손실 크래시 복구 및 부하 검증**:
   - 불시의 `SIGKILL` 전원 차단 환경에서 In-Memory OCC 및 WiredTiger 양대 엔진 100% 무손실 복구 실증 (`bin/crashtest`).
   - 다중 워커 연속 트랜잭션 부하 환경에서 자원 누수 0 입증 (`bin/soaktest`).
