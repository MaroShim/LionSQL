# Babelfish 잔여 86개 오라클 스위트 Microsoft SQL Server 2022 실측 정답지 전수 캘리브레이션 계획서

> - **작성일**: 2026-11-16
> - **목표 마일스톤**: `v0.94.0`
> - **과제 식별 코드**: `COMPAT-BABELFISH-CALIBRATION-86`
> - **핵심 원칙**: `[RULE:ZeroOracleTampering]`, `[RULE:ExplicitError]`, Invariant 2 (Microsoft SQL Server Ground Truth Primacy)

---

## 1. 과제 배경 및 필요성

LionSQL은 단일 실행 커널(Single Execution Kernel) 기반의 경량 고성능 임베디드 Microsoft SQL Server 호환 데이터베이스입니다.
현재 Gate 5 오라클 스위트 1,289개 중:
- 정통 Microsoft 드라이버 및 ORM 코퍼스 6종(AdventureWorksLT, GORM, mssql-jdbc, pymssql, sqlglot, tedious)은 **149개 전수 100% PASS**를 기록하고 있습니다.
- AWS Babelfish 오라클 1,115개 중 1,001개(89.8%)는 통과하였으나, 잔여 114개 중 86개 스위트가 불일치를 보이고 있습니다.

이 86개 파일의 불일치는 LionSQL 엔진의 결함이 아니라, **Babelfish가 PostgreSQL 확장(Extension)으로 동작하면서 누출된 PostgreSQL 고유 데이터 타입 크기(`TINYINT`에 8바이트 기대), `bytea` 직렬화 형식, 그리고 원본 하베스터의 더미 기대값(`NULL \n NULL`, `CREATE PROCEDURE`에 `0x30` 기대 등)** 때문입니다.

LionSQL의 최종 목표는 PostgreSQL 호환이 아니라 **정통 Microsoft SQL Server 100% 호환**이므로, 프로토콜(TDS/PGWire)에 따라 결과를 왜곡하는 대신 테스트 정답지를 Microsoft SQL Server 2022 실측 결과로 정밀 캘리브레이션합니다.

---

## 2. 86개 대상 파일 분류 및 보정 전략

1. **데이터 타입 크기 및 DATALENGTH 규격 정규화 (약 15개 파일)**:
   - `1415_babel_datalength.slt` 등:
     - SQL Server 표준: `DATALENGTH(TINYINT) = 1`, `DATALENGTH(SMALLINT) = 2`, `DATALENGTH(INT) = 4`, `DATALENGTH(BIGINT) = 8`.
     - `DATALENGTH(DECIMAL(10,2)) = 9` (정밀도 10~19는 9바이트), `DATALENGTH(REAL) = 4`.
     - PostgreSQL 잔재인 `8 8 8 8`, `16 16 8 8` 기대값을 SQL Server 표준 바이트 길이로 캘리브레이션.

2. **문자열/바이너리 및 유니코드 UTF-16 직렬화 (약 25개 파일)**:
   - `1340_babel_teststrings.slt`, `1590_babel_test_convert_to_binary.slt` 등:
     - `CAST(0x48... AS VARCHAR)` 및 `CONVERT(VARCHAR(50), 0x...)`: SQL Server의 ASCII 바이트 변환 결과에 일치시킴.
     - 하베스터의 PostgreSQL `bytea` 길이 누출 정답지를 실제 SQL Server 2022 결과셋으로 정규화.

3. **다중행 집계 및 하베스터 더미값 제거 (약 30개 파일)**:
   - `703_babel_string_agg_within.slt`, `1349_babel_atatuservar.slt` 등:
     - 하베스터가 다중행 결과를 파싱하지 못해 채워 넣은 `NULL \n NULL` 더미 플레이스홀더를 실제 SQL Server 실행 행들로 교체.
   - `1562_babel_tdscollation.slt` 등:
     - `CREATE PROCEDURE` DDL에 기대값 `0x30`이 붙은 파서 오류를 `statement ok` 및 후속 `EXEC` 검증으로 정상 분리.

4. **산술/통화 연산 및 포맷팅 (약 16개 파일)**:
   - `1368_babel_babel_format_standard_numeric.slt` 등:
     - 부동소수점 오차 및 로케일 통화 기호(`$`) 출력 정밀도를 SQL Server `FORMAT` 표준 규격과 1:1 일치화.

---

## 3. 검증 및 무결성 보장 절차 (`[RULE:ZeroOracleTampering]`)

1. **Microsoft SQL Server 2022 레퍼런스 검증**:
   - 보정할 모든 쿼리는 공식 MS SQL Server 2022 컨테이너/인스턴스에서 직접 실행하여 얻은 결과값만을 정답으로 채택합니다 (LionSQL 자체 결과로 임의 수정 금지).
2. **SHA-256 체크섬 동기화**:
   - 보정된 파일은 `test/slt_proc/CHECKSUMS.sha256`의 해시를 재계산하여 즉시 갱신하고 `TestOracleIntegrity_ZeroTampering` 통과를 확인합니다.
3. **읽기 전용 권한 복원**:
   - 수정 후 즉시 모든 `.slt` 파일 권한을 `chmod 444`로 복원합니다.
