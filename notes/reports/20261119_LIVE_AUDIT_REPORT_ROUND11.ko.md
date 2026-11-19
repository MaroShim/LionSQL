# LionSQL 실시간 엔진 감사 보고서 - Round 11 (근본 원인 압축 결함 목록)

**대상**: LionSQL v0.1.0-alpha (Git: 53b48865, 스토리지 엔진: WiredTiger & In-Memory OCC)  
**감사 중점 영역**: 시퀀스 엔진, Prepared Statement 무효화, 스토리지 인덱스 제약(Option B), 세션 설정 및 트랜잭션 충돌  
**식별된 고유 근본 결함 수**: 7건  
**감사 일시**: 2026-11-19  
**원본 출처**: `/private/tmp/20261119_lionsql_live_audit_report_round11.txt`

---

## 1. 개요 및 총평

Round 6~8의 시정 조치에 이어, Round 11에서는 그동안 다루어지지 않았던 엔진의 핵심 미탐 영역을 집중 조사하였습니다:
1. 시퀀스 생성기(Sequence Generator) 구현 및 ACID 단조 증가 보장 여부
2. 인덱스 생성 시 Option B 물리 스토리지 제약(32컬럼, 1,700바이트) 적용 여부
3. 스키마 변경(`DROP COLUMN`) 시 Prepared Statement 플랜 캐시 무효화 여부
4. 스냅샷 격리 수준에서의 동시 쓰기-쓰기 충돌(Write-Write Conflict) 감지 여부
5. 세션 설정값(메모리, 타임아웃 파라미터)의 음수/유효성 검증

포트 5433 실시간 프로브를 통해 총 7건의 아키텍처 근본 결함이 실측 검증되었으며, 테스트에 사용된 임시 객체는 안전하게 정리되었습니다.

---

## 2. Round 11 결함 상세 카탈로그 (7건)

### [ENG-011] 시퀀스 엔진이 미구현 스텁 상태이며, `NEXTVAL`이 항상 고정값 1을 반환하는 결함
- **대상 컴포넌트**: 시퀀스 엔진 및 내장 함수
- **관련 파일**: `pkg/proc/builtins_sys.go:350-351`, `pkg/engine/sqlexec.go:1928-1935`
- **심각도**: 치명적 (CRITICAL)
- **근본 원인**:
  `sqlexec.go:1928`에서 `CREATE SEQUENCE`, `ALTER SEQUENCE`, `DROP SEQUENCE` 구문이 "PG 호환성 허용 스텁"으로 처리되어 카탈로그나 영구 시퀀스 생성기를 할당하지 않고 성공을 반환합니다. 또한 `builtins_sys.go:350`에서 `NEXTVAL` 호출 시 항상 고정 정수 `storage.IntValue(1)`을 반환하도록 하드코딩되어 있습니다. 이에 따라 `NEXTVAL('seq')`를 반복 호출하더라도 항상 1만 반환되며, 표준 T-SQL 구문인 `NEXT VALUE FOR seq` 역시 평가되지 않고 리터럴 문자열로 반환됩니다.
- **재현 쿼리**:
  ```sql
  CREATE SEQUENCE test_seq START WITH 1 INCREMENT BY 1;
  SELECT NEXTVAL('test_seq'); -- 1 반환
  SELECT NEXTVAL('test_seq'); -- 1 반환 (기대값: 2)
  SELECT NEXTVAL('test_seq'); -- 1 반환 (기대값: 3)
  ```
- **정상 동작 (Expected)**: `NEXTVAL`은 시퀀스 생성기 상태에 따라 단조 증가(monotonic advance)해야 함.
- **실제 동작 (Actual)**: 항상 정수 1만 고정 반환.

---

### [ENG-012] `CreateIndex` 시 `IndexSchema.Validate` 호출이 누락되어 Option B 인덱스 제한이 미적용되는 결함
- **대상 컴포넌트**: 스토리지 엔진 / 인덱스 생성 (Option B 명세)
- **관련 파일**: `pkg/storage/wiredtiger/wt_engine.go:2019-2065`, `pkg/storage/memengine/ddl.go:110-145`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  Option B 물리 제약(`MaxIndexColumns = 32`, `MaxIndexKeyBytes = 1,700B`)을 위한 `IndexSchema.Validate(tableSchema)` 메서드가 구현되었으나, `WTEngine.CreateIndex` 및 `MemEngine.CreateIndex` 내부에서 인덱스 생성 전 이를 호출하지 않아 33개 이상의 컬럼이나 1,700바이트를 초과하는 인덱스가 생성 통과되는 결함입니다. (※ 직전 커밋 `6d32c4bd`에서 유효성 검증 로직 연결 완료).
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_idx (c0 INT, c1 INT, ... c34 INT);
  CREATE INDEX idx_35 ON t_idx (c0, c1, ... c34); -- 35개 컬럼
  ```
- **정상 동작 (Expected)**: 에러 발생: "index has 35 columns (max 32)" (`ErrTooManyIndexColumns`, Msg 1904).
- **실제 동작 (Actual)**: 인덱스 생성이 성공함.

---

### [ENG-013] `DROP COLUMN` 후 Prepared Statement 캐시가 무효화되지 않고 오래된 컬럼 오프셋을 참조하여 빈 문자열을 반환하는 결함
- **대상 컴포넌트**: 쿼리 플랜 캐시 & Prepared Statements
- **관련 파일**: `pkg/engine/sqlexec_prepare.go`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  `ALTER TABLE ... DROP COLUMN`으로 컬럼이 삭제되었을 때, 세션에 이미 준비된 문장(`PREPARE p AS SELECT dropped_col ...`)이 무효화되거나 재컴파일되지 않습니다. 삭제된 컬럼을 조회하는 Prepared Statement 실행 시 컬럼 부재 오류를 내지 않고 오래된 튜플 오프셋으로 실행되어 빈 문자열 또는 손상된 프로젝션 결과를 조용히 반환합니다.
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_prep (id INT PRIMARY KEY, c1 INT, c2 INT);
  INSERT INTO t_prep VALUES (1, 10, 20);
  PREPARE p_sel AS SELECT c2 FROM t_prep WHERE id = $1;
  EXECUTE p_sel(1); -- 20 반환
  ALTER TABLE t_prep DROP COLUMN c2;
  EXECUTE p_sel(1); -- 에러 대신 빈 문자열 "" 반환!
  ```
- **정상 동작 (Expected)**: Msg 207: "Invalid column name 'c2'." 또는 스키마 버전 불일치 오류.
- **실제 동작 (Actual)**: 성공적으로 실행되며 빈 문자열 반환.

---

### [ENG-014] 동시 쓰기-쓰기 충돌(Write-Write Conflict) 발생 시 직렬화 실패 없이 덮어쓰는 결함
- **대상 컴포넌트**: 스토리지 동시성 제어 & MVCC
- **관련 파일**: `pkg/storage/wiredtiger/wt_engine.go`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  PostgreSQL / MSSQL 스냅샷 격리(Snapshot Isolation) 수준에서는 트랜잭션 A와 B가 동일한 행(`WHERE id = 1`)을 동시에 수정하려 할 때, 두 번째 트랜잭션은 대기하거나 직렬화 오류(`could not serialize access due to concurrent update`, SQLSTATE 40001)로 중단되어야 합니다. 현재 엔진은 두 동시 트랜잭션이 순차적으로 커밋되는 것을 허용하여, 늦게 커밋된 트랜잭션이 먼저 커밋된 쓰기를 덮어쓰는 갱신 손실(lost update)이 발생합니다.
- **재현 쿼리**:
  ```sql
  -- 세션 1: BEGIN; UPDATE t SET val = 101 WHERE id = 1; SELECT sleep(1); COMMIT;
  -- 세션 2: BEGIN; UPDATE t SET val = 202 WHERE id = 1; COMMIT;
  ```
- **정상 동작 (Expected)**: 세션 2는 세션 1의 충돌을 감지하고 직렬화 실패 오류를 반환해야 함.
- **실제 동작 (Actual)**: 두 세션 모두 정상 커밋되며 세션 2가 세션 1의 데이터를 조용히 덮어씀.

---

### [ENG-015] `SET work_mem`, `statement_timeout`, `lock_timeout`이 음수 및 잘못된 값을 에러 없이 수용하는 결함
- **대상 컴포넌트**: 세션 설정 & 메모리 거버너
- **관련 파일**: `pkg/engine/sqlexec.go:1150-1175`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  세션 `SET` 명령 처리기가 범위나 타입 검증 없이 값을 직접 `cs.Vars`에 저장합니다. `work_mem = '-100MB'`, `statement_timeout = -5000`, `statement_timeout = 'garbage'`와 같이 유효하지 않은 설정값을 지정하더라도 오류(SQLSTATE 22023)를 내지 않고 정상 "SET" 응답을 반환합니다.
- **재현 쿼리**:
  ```sql
  SET work_mem = '-100MB';
  SET statement_timeout = 'not_a_number';
  ```
- **정상 동작 (Expected)**: ERROR 22023: `"invalid value for parameter \"work_mem\""`
- **실제 동작 (Actual)**: 에러 없이 "SET" 성공 반환.

---

### [ENG-016] `ALTER TABLE ADD CONSTRAINT` 실행 시 동일 테이블 내 중복된 제약조건 이름을 허용하는 결함
- **대상 컴포넌트**: DDL 테이블 제약조건
- **관련 파일**: `pkg/engine/sqlexec_ddl.go:1300-1400`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  `ALTER TABLE t ADD CONSTRAINT name CHECK (...)` 실행 시 동일한 `name`의 제약조건이 이미 해당 테이블이나 스키마에 존재하는지 확인하지 않고 추가합니다. 동일한 이름의 제약조건이 중복 등록되면 ANSI SQL 고유성 요건을 위반하며 `DROP CONSTRAINT` 시 결정론적 처리가 불가능해집니다.
- **재현 쿼리**:
  ```sql
  ALTER TABLE t ADD CONSTRAINT chk_id CHECK (id > 0);
  ALTER TABLE t ADD CONSTRAINT chk_id CHECK (id < 100); -- 성공함!
  ```
- **정상 동작 (Expected)**: Msg 2714: `"There is already an object named 'chk_id' in the database."`
- **실제 동작 (Actual)**: 동일한 이름으로 두 제약조건이 모두 추가됨.

---

### [ENG-017] 존재하지 않는 테이블에 대해 `LOCK TABLE`이 조용히 성공하는 결함
- **대상 컴포넌트**: 허용적(Tolerant) DDL 엔진
- **관련 파일**: `pkg/engine/sqlexec.go:1937-1945`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  `sqlexec.go:1937`의 `LOCK TABLE` 처리가 무조건 성공을 반환하는 PG 호환성 스텁으로 구현되어 있어, 대상 테이블이 실제 존재하는지 확인하지 않고 `LOCK TABLE executed successfully.` 메시지를 반환합니다.
- **재현 쿼리**:
  ```sql
  LOCK TABLE non_existent_table_xyz IN ACCESS EXCLUSIVE MODE;
  ```
- **정상 동작 (Expected)**: ERROR 42P01: `"relation \"non_existent_table_xyz\" does not exist"`
- **실제 동작 (Actual)**: 성공 메시지 반환: `"LOCK TABLE executed successfully."`
