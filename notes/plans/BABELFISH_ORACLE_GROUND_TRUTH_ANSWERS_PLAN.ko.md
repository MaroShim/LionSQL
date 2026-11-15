# Babelfish Oracle 정답셋 보강 및 무결성 동기화 설계서 (BABELFISH_ORACLE_GROUND_TRUTH_ANSWERS_PLAN.ko.md)

> **작성일**: 2026-11-15  
> **상태**: In Progress  
> **목적**: AWS Babelfish T-SQL 코퍼스에서 하베스팅된 Gate 5 오라클 테스트 스위트(`test/slt_proc/babelfish_oracle/`) 중, 실행 성공 여부만 확인하는 `statement ok`로 방치되었거나 기댓값(Answer)이 비어 있던 쿼리들에 대해 Microsoft SQL Server 2019/2022 공식 명세 기준의 정밀 정답(Expected Results)을 산출하여 `query` 블록으로 승격하고, `CHECKSUMS.sha256` 해시 및 `[RULE:ZeroOracleTampering]` 불변식을 엄격히 동기화한다.

---

## 1. 배경 및 현황 분석

1. **배경**:
   - AWS Babelfish 원본 T-SQL 코퍼스(`test/babelfish/raw`)는 SQLLogicTest(SLT)와 달리 기대 결과셋 없이 SQL 스크립트만 제공되었음.
   - 1,115개 Babelfish 스위트를 SLT 규격으로 변환하는 과정에서, 많은 `SELECT` 쿼리들이 정답 검증 없이 `statement ok`(구문 통과 확인)로 남겨졌거나 기대 결과가 0건인 경우 `statement ok`로 우회되었음.
   - 특히 `665_babel_like_expression.slt`의 경우 원본 작성자가 코드 인라인 주석으로 `-- 1`(1행 반환) 또는 `-- no row`(0행 반환)을 명시해 두었음에도 불구하고 `statement ok`로 일괄 변환되어 실제 반환값 검증이 누락되어 있었음.

2. **대상 파일 및 쿼리 현황**:
   - **`665_babel_like_expression.slt`**: T-SQL 정규 표현식 및 LIKE 패턴 매칭(`[a-z]`, `[^a-z]`, 이스케이프) 28개 쿼리에 주석 명시값(`-- 1`, `-- no row`)에 기반한 정답셋 누락.
   - **`427_babel_babel_json_funcs.slt`**: `JSON_VALUE`, `JSON_QUERY` 경로 탐색 37개 쿼리가 `statement ok`로 실행되고 있어 실제 추출된 JSON 스칼라/객체 문자열 미검증.
   - **`609_babel_cast_eliminate.slt`**: 빈 테이블 대상 25개 캐스트 제거 쿼리가 `statement ok`로 남아 있음 (기대값: 0행).
   - **`478_babel_babel_726.slt`**: 빈 테이블 대상 56개 COALESCE 타입 쿼리가 `statement ok`로 남아 있음 (기대값: 0행).

---

## 2. 해결 원칙 및 Invariant 16 준수 절차

1. **Microsoft SQL Server 공식 명세 준수**:
   - 자가 채점(LionSQL 엔진이 출력하는 값을 맹목적으로 믿는 순환 검증)을 배제하고, 공식 Microsoft SQL Server 2019/2022 사양 및 인라인 주석 그라운드 트루스를 기준으로 기댓값을 확정한다.
2. **SLT 문법 표준화**:
   - 1행 정답 반환:
     ```
     query I
     <query>
     ----
     1
     ```
   - 0행 (Empty Result Set):
     ```
     query I
     <query>
     ----
     (빈 줄)
     ```
   - JSON 스칼라/문자열 반환:
     ```
     query TTT
     <query>
     ----
     <expected values>
     ```
3. **`CHECKSUMS.sha256` 및 파일 권한 동기화**:
   - `.slt` 파일 수정 시 일시적으로 쓰기 권한을 부여하고, 수정 완료 즉시 `test/slt_proc/CHECKSUMS.sha256`의 SHA-256 체크섬을 재산출하여 갱신.
   - `test/slt_proc/slt_proc_test.go`의 `TestOracleIntegrity_ZeroTampering` 및 `scripts/verify_no_slt_mutation.sh`를 통과하도록 4계층 방어막을 완벽하게 유지.

---

## 3. 세부 작업 단계 (Step-by-Step Execution)

1. **Step 1**: `665_babel_like_expression.slt`의 28개 LIKE 패턴 쿼리에 대해 주석(`-- 1`, `-- no row`) 기반 정답값 주입 및 `query I` 승격.
2. **Step 2**: `427_babel_babel_json_funcs.slt`의 JSON 스칼라 함수들에 대해 T-SQL 표준 정답값 산출 및 주입.
3. **Step 3**: `609_babel_cast_eliminate.slt` 및 `478_babel_babel_726.slt`의 빈 테이블 쿼리들에 대해 `query` 및 0행 정답 블록 표준화.
4. **Step 4**: `test/slt_proc/CHECKSUMS.sha256` 갱신 및 `go test -v -race ./test/slt_proc/...` 100% PASS 검증.
5. **Step 5**: 3-Stage Pipeline에 따라 커밋 완료.
