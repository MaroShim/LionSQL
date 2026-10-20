# LionSQL Task Identification Code Taxonomy & Catalog (`notes/TAXONOMY.md`)

이 문서는 LionSQL 프로젝트 전반에서 과제 관리, 로드맵 추적, 커밋 메시지 식별용으로 사용되는 **전체 식별 코드 체계(Taxonomy)와 도메인별 과제 일람표**입니다.

과거 완료된 마일스톤부터 현재 진행 중인 과제, 향후 착수할 백로그까지 고유 식별자(ID)를 통해 일관되게 추적할 수 있도록 정의합니다.

---

## 1. 식별 코드 체계 범례 (Taxonomy Architecture)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 체계 | 관리 목적 및 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 및 릴리즈 로드맵 단계 구분 |
| **`P-1` ~ `P-5`** | **엔진 성능 (Performance)** | `P-1` ~ `P-5` | 스토리지 I/O, 와이어 프로토콜, 실행 엔진 5대 성능 극대화 트랙 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (통계 수집, 조인, 선택도, 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |
| **`BUG-PK-1`** | **무결성 결함 수정 (Integrity Bug)** | `BUG-PK-1` | `execUpdate`/`execDelete`/`execTruncate` 및 VDBE의 PK 동적 해석 및 맵 순회 왜곡 방지 |
| **`BUG-CASE-1`** | **컬럼 대소문자 정규화 (Case-Insensitive)** | `BUG-CASE-1` | `TableSchema.FindColumn`의 `strings.EqualFold` 대소문자 무관 탐색 보장 |
| **`BUG-RECORD-1`** | **사일런트 유실 방지 (Silent Loss Bug)** | `BUG-RECORD-1` | DML Record 컬럼명 정규화(`colSchema.Name`)로 `RecordToRow` 업데이트 유실 및 NULL 삽입 방지 |
| **`BUG-ARITH-1`** | **수식 0 초기화 방지 (Arithmetic Bug)** | `BUG-ARITH-1` | `evalUpdateExpr` 수식 평가 시 컬럼명 대소문자 무관 탐색으로 컬럼 0 리셋 방지 |
| **`BUG-UPSERT-1`** | **Upsert 충돌 정합성 (Upsert Bug)** | `BUG-UPSERT-1` | `ON CONFLICT (col)` 충돌 컬럼 대소문자 정규화로 Upsert 유니크 위반 방지 |
| **`BUG-WHERE-1`** | **WHERE IN 절 정합성 (WHERE IN Bug)** | `BUG-WHERE-1` | `evaluateSingleConditionMap` 내 `WHERE IN (...)` 절 대소문자 무관 탐색 누락 보강 |
| **`BUG-VM-INTO-1`** | **VDBE 변수대입 무작위 순회 방지 (IntoVar Bug)** | `BUG-VM-INTO-1` | VDBE `SELECT ... INTO @var` 무작위 맵 순회 제거 및 `cols[0]` 결정론적 바인딩 |
| **`BUG-SUBQ-1`** | **스칼라 서브쿼리 무작위 반환 방지 (Subquery Bug)** | `BUG-SUBQ-1` | 스칼라 서브쿼리 수식 평가 시 무작위 맵 순회 제거 및 `cols[0]` 대소문자 무관 탐색 |
| **`BUG-BOUNDS-1`** | **인덱스 바운드 무작위 추출 방지 (Index Bound Bug)** | `BUG-BOUNDS-1` | `ParseIndexCond` 무작위 맵 순회 제거 및 인덱스 대상 컬럼 바운드 정밀 매칭 |
| **`BUG-EXP-1`** | **EXPLAIN 서브쿼리 무작위 치환 방지 (Explain Bug)** | `BUG-EXP-1` | `EXPLAIN` 서브쿼리 평탄화 시 무작위 맵 순회 제거 및 서브쿼리 컬럼 키 정합 |
| **`BUG-DDL-PK-1`** | **PK 임의 추측 방지 (DDL PK Guess Bug)** | `BUG-DDL-PK-1` | PK 미지정 테이블 생성 시 `Columns[0]`을 멋대로 PK로 승격하는 안티패턴 제거 및 가상 PK(`_row_id`) 보장 |
| **`BUG-SELECT-INTO-PK-1`** | **SELECT INTO PK 임의 추측 방지 (Select Into PK Bug)** | `BUG-SELECT-INTO-PK-1` | `SELECT INTO` 및 파생 테이블의 일시적 고유 컬럼 임의 PK 지정 및 0건 시 `Columns[0]` PK 지정 제거 |
| **`BUG-JOIN-IDX-1`** | **조인 인덱스 임의 선택 방지 (Join Index Guess Bug)** | `BUG-JOIN-IDX-1` | `IndexNestedLoopJoin` 구성 시 인덱스 미일치 시 `Indexes[0]`을 임의 선택하여 조인 오염시키는 결함 방지 (Hash Join 안전 전환) |
| **`BUG-WT-INDEX-COMP-1`** | **WiredTiger 복합 인덱스 정합성 (WT Composite Index Bug)** | `BUG-WT-INDEX-COMP-1` | WiredTiger 복합 인덱스 스캔 시 첫 번째 컬럼만 비교하고 컬럼 누락 시 TypeUnknown 0(동일) 판정하는 결함 수정 |
| **`BUG-VM-COMP-IDX-1`** | **VDBE 복합 인덱스 인코딩 정합성 (VM Composite Index Bug)** | `BUG-VM-COMP-IDX-1` | VDBE VM에서 복합 인덱스를 단일 인덱스로 착각하고 비인코딩 스캔하여 0건 반환되는 결함 수정 |
| **`BUG-ORDER-BY-SPLIT-1`** | **ORDER BY 다중 컬럼 및 대소문자 정합성 (Order By Bug)** | `BUG-ORDER-BY-SPLIT-1` | `strings.Fields` 분해로 인한 다중 정렬 누락, 콤마 포함 시 DESC 무력화 및 대소문자 미매칭 무력화 원천 해결 |
| **`BUG-JOIN-PARSE-1`** | **조인 조건 단순 Split 파싱 결함 수정 (Join Parse Bug)** | `BUG-JOIN-PARSE-1` | `strings.Split(cond, "=")` 다중 조건 분해 실패 시 빈 컬럼명 전달 및 PGWire FROM 분해 오작동 방지 |
| **`BUG-TABLE-CASE-1`** | **테이블명 대소문자 무관 탐색 보장 (Table Case Bug)** | `BUG-TABLE-CASE-1` | `GetTable`/`DropTable` 맵 엄격 일치로 테이블 미인식 에러 방지 (`EqualFold` 폴백 보장) |
| **`BUG-WT-FK-1`** | **WiredTiger 외래키 무결성 검증 구축 (WT FK Bug)** | `BUG-WT-FK-1` | WiredTiger `Insert`/`Update`/`Delete` 시 외래키 참조 무결성 검사 및 CASCADE/RESTRICT 완전 연동 |
| **`BUG-FK-CASE-1`** | **외래키 부모 컬럼 대소문자 정규화 (FK Case Bug)** | `BUG-FK-CASE-1` | `findMatchingChildPKsUnderLock` 대소문자 불일치로 자식 참조 0건 오판 및 부모 삭제 허용 결함 수정 |
| **`BUG-SPLIT-VALUES-1`** | **VALUES 튜플 중첩 수식 콤마 분해 정합성 (Split Values Bug)** | `BUG-SPLIT-VALUES-1` | `splitValues` 괄호 깊이(`depth`) 추적으로 `COALESCE(a, b)` 등 다중 인자 함수 사용 시 컬럼 수 불일치 에러 방지 |
| **`BUG-MERGE-SET-1`** | **MERGE SET 절 다중 인자 수식 보존 (Merge Set Bug)** | `BUG-MERGE-SET-1` | `parseSetPairs` 괄호 고려 토큰 분해로 `COALESCE` 등 수식 내 콤마 분해 시 후속 구문 사일런트 유실 방지 |
| **`BUG-COPY-PK-1`** | **COPY FROM 정수 PK 0 보존 (Copy PK Zero Bug)** | `BUG-COPY-PK-1` | PGWire `COPY FROM` 시 정수 기본키 `0`을 미지정으로 오해하여 Unix 에포크 타임스탬프로 위조하는 결함 수정 |
| **`BUG-STR-NULL-1`** | **문자열 함수 NULL 전파 보장 (String NULL Bug)** | `BUG-STR-NULL-1` | `UPPER`/`LOWER`/`TRIM`/`SUBSTRING`/`REPLACE` 등 NULL 입력 시 빈 문자열/변조 대신 `NULL` 정규 반환 |
| **`BUG-VM-UPDATE-1`** | **VDBE UPDATE 컬럼명 정규화 (VM Update Silent Loss)** | `BUG-VM-UPDATE-1` | VDBE `OpUpdate`/`execUpdateWhere` 컬럼명 대소문자 미정규화로 인한 업데이트 사일런트 유실 방지 |
| **`BUG-FK-CHILD-CASE-1`** | **외래키 자식 컬럼 대소문자 정규화 (FK Child Case Bug)** | `BUG-FK-CHILD-CASE-1` | `checkForeignKeyUnderLock` 대소문자 불일치 시 가짜 외래키 삽입이 무단 허용되는 결함 수정 |
| **`BUG-PGWIRE-PARAM-1`** | **PGWire 파라미터 경계 인식 치환 (PGWire Param Bug)** | `BUG-PGWIRE-PARAM-1` | 단순 문자열 치환으로 `$10` 이상의 다중 파라미터가 `$1` 치환 시 깨지는 결함 수정 |
| **`BUG-APPLY-REGEX-1`** | **CROSS/OUTER APPLY 정규식 및 맵 순회 왜곡 방지 (Apply Regex Bug)** | `BUG-APPLY-REGEX-1` | `buildParameterizedApplyQuery` 단순 정규식 치환 및 무작위 맵 순회로 키워드/리터럴 오염 방지 |
| **`BUG-STRING-SPLIT-SEP-1`** | **STRING_SPLIT 빈 구분자 콤마 임의 간주 제거 (String Split Sep Bug)** | `BUG-STRING-SPLIT-SEP-1` | 빈 문자열 구분자를 콤마로 멋대로 대체하는 안티패턴 제거 및 표준 규격 에러 반환 |
| **`BUG-ADD-COL-NOT-NULL-1`** | **기본값 없는 NOT NULL 컬럼 추가 시 NULL 오염 방지 (Add Column Null Bug)** | `BUG-ADD-COL-NOT-NULL-1` | 데이터 존재 테이블에 NOT NULL 컬럼 추가 시 기존 행을 NULL로 채워 무결성을 파괴하는 결함 방지 |
| **`BUG-DML-PK-RANDOM-1`** | **비-id PK 테이블 DML 시 무작위 컬럼 PK 간주 해결 (DML Random PK Bug)** | `BUG-DML-PK-RANDOM-1` | `ExecuteUpdate`/`ExecuteDelete` 폴백의 `rec["id"]` 하드코딩 및 맵 순회 임의 컬럼 PK 간주 결함 해결 |
| **`BUG-UPDATE-SET-SPLIT-1`** | **UPDATE SET 절 다중 인자 수식 사일런트 유실 방지 (Update Set Split Bug)** | `BUG-UPDATE-SET-SPLIT-1` | `strings.Split(setClause, ",")` 단순 분해 제거로 다중 인자 함수 및 리터럴 콤마 분해 유실 방지 |
| **`BUG-HTTP-TABLE-PK-1`** | **HTTP API 테이블 생성 시 임의 PK 승격 제거 (HTTP Table PK Bug)** | `BUG-HTTP-TABLE-PK-1` | `cols[0].PrimaryKey = true` 임의 지정 제거 및 미지원 타입 전달 시 400 에러 정규 반환 |
| **`BUG-STORAGE-UPDATE-CASE-1`** | **스토리지 Update 레코드 컬럼명 대소문자 정규화 (Storage Update Case Bug)** | `BUG-STORAGE-UPDATE-CASE-1` | `updateLocked`에서 입력 레코드 키를 스키마 컬럼명으로 정규화하여 대소문자 차이 업데이트 유실 방지 |
| **`BUG-TUPLE-RECORD-CASE-1`** | **RecordToRow 대소문자 일치로 인한 NULL 덮어쓰기 방지 (Tuple Case Bug)** | `BUG-TUPLE-RECORD-CASE-1` | `rec[col.Name]` 엄격 조회로 대소문자가 다른 레코드 입력 시 컬럼 값이 NULL로 훼손되는 결함 해결 |
| **`BUG-INDEX-SCAN-ORDERBY-1`** | **인덱스 스캔 다중 정렬 무시 및 정렬 누락 방지 (Index Scan Order By Bug)** | `BUG-INDEX-SCAN-ORDERBY-1` | 첫 번째 컬럼만 일치 시 다중 정렬 전체가 만족된 것으로 오판하여 정렬을 건너뛰는 결함 해결 |
| **`BUG-COPY-DELIM-1`** | **COPY FROM DELIMITER 후속 옵션 따옴표 변조 방지 (Copy Delimiter Bug)** | `BUG-COPY-DELIM-1` | DELIMITER 뒤 후속 옵션 존재 시 닫는 따옴표(`'`)를 구분자로 파싱하여 CSV 파싱이 깨지는 결함 해결 |
| **`BUG-PGWIRE-DESCRIBE-1`** | **PGWire Describe 단순 문자열 분해 OID 왜곡 방지 (PGWire Describe Bug)** | `BUG-PGWIRE-DESCRIBE-1` | 쿼리를 FROM/콤마로 단순 분해하여 서브쿼리 및 함수 콤마가 파괴되고 OID 25로 왜곡되는 결함 해결 |
| **`BUG-VM-SUBQ-NIL-1`** | **VDBE 스칼라 서브쿼리 nil 패닉 및 컬럼 침묵 버림 방지 (VM Subquery Nil Bug)** | `BUG-VM-SUBQ-NIL-1` | `(SELECT 1)` 등 FROM 없는 서브쿼리 nil 포인터 패닉 제거 및 다중 컬럼 서브쿼리 문법 에러 반환 |
| **`BUG-CATALOG-ORDERBY-1`** | **가상 카탈로그 ORDER BY 콤마 잔존 정렬 무력화 방지 (Catalog Order By Bug)** | `BUG-CATALOG-ORDERBY-1` | `strings.Fields` 분해로 인해 `orderCol`에 콤마가 남아(`"col1,"`) 카탈로그 정렬이 무력화되던 결함 해결 |
| **`BUG-WT-INDEX-DESC-1`** | **WiredTiger IndexScanDesc 역정렬 스텁 결함 해결 (WT Index Desc Bug)** | `BUG-WT-INDEX-DESC-1` | `WTTx.IndexScanDesc` 정방향 스캔 반환 결함 수정으로 `ORDER BY DESC` 인덱스 스캔 역정렬 보장 |
| **`BUG-CTE-MATERIALIZE-1`** | **CTE 임의 PK 강제 및 VARCHAR 타입 파괴 해결 (CTE Materialize Bug)** | `BUG-CTE-MATERIALIZE-1` | 첫 번째 컬럼 임의 PK 선언으로 인한 중복 키 에러 방지 및 원본 쿼리 데이터 타입 메타데이터 보존 |
| **`BUG-TDS-ROW-CASE-1`** | **TDS 행 직렬화 대소문자 정규화 (TDS Row Case Bug)** | `BUG-TDS-ROW-CASE-1` | `WriteRowDirect`/`WriteNBCRowDirect` 대소문자 무관 탐색 지원으로 올바른 데이터 전송 보장 |
| **`BUG-CATALOG-IDENT-1`** | **가상 카탈로그 3-part/4-part 식별자 파싱 결함 해결 (Catalog Ident Bug)** | `BUG-CATALOG-IDENT-1` | 점 단위 분할 후 최종 식별자 정규 트림 적용으로 `[master].[sys].[tables]` 정상 인식 보장 |
| **`BUG-PROJ-CAST-AS-1`** | **수식 내 AS 오분해 방지 (Project Cast AS Bug)** | `BUG-PROJ-CAST-AS-1` | `CAST(x AS INT)` 등 수식 내부 `" AS "` 오분해로 인한 표현식 절단 및 NULL 반환 결함 해결 |
| **`BUG-DECORRELATE-TRIM-1`** | **비상관화 and 접미 단순 Trim 결함 해결 (Decorrelate Trim Bug)** | `BUG-DECORRELATE-TRIM-1` | `removeClause`에서 `"and"` 단순 슬라이스로 `Poland` ➔ `Pol` 등 리터럴 손상 방지 토큰 경계 검사 적용 |
| **`BUG-INDEX-KEY-CASE-1`** | **세컨더리 인덱스 키 추출 대소문자 정규화 (Index Key Case Bug)** | `BUG-INDEX-KEY-CASE-1` | `extractIndexKey`의 대소문자 미정규화로 인한 세컨더리 인덱스 NULL 오염 및 스캔 0건 결함 해결 |
| **`BUG-FK-PARENT-TABLE-CASE-1`** | **외래키 부모 테이블 대소문자 정규화 (FK Parent Table Case Bug)** | `BUG-FK-PARENT-TABLE-CASE-1` | 부모 테이블 대소문자 불일치 시 `ON DELETE RESTRICT` 및 외래키 연쇄 검사 완전 우회 결함 해결 |
| **`BUG-PLANNER-ORDERBY-1`** | **CBO/SetOp 다중 정렬 키 콤마 잔존 해결 (Planner OrderBy Bug)** | `BUG-PLANNER-ORDERBY-1` | CBO 및 SetOp 플래너 다중 정렬 시 `strings.Fields` 콤마 잔존(`"a,"`)으로 인한 플랜 왜곡 해결 |
| **`BUG-WT-SWEEP-1`** | **WiredTiger 60분 유휴 세션 경고 방지 및 세션 리셋 (WT Sweep Server Bug)** | `BUG-WT-SWEEP-1` | `startPeriodicSync` 고아 세션 상시 점유 제거, `session->reset` 바인딩 및 `SessionPool` TTL 리퍼 연동 |
| **`BUG-API-PK-GEN-1`** | **HTTP API 레코드 밀리초 PK 충돌 및 UUID 누락 해결 (API PK Gen Bug)** | `BUG-API-PK-GEN-1` | 나노초/카운터 결합 유니크 채번 및 UUID PK 자동 생성 지원으로 PK 충돌 및 NULL 방지 |
| **`BUG-WAL-TORN-BOOT-1`** | **MemEngine WAL 테일 절단 시 기동 실패 방지 (WAL Torn Write Boot Bug)** | `BUG-WAL-TORN-BOOT-1` | 비정상 종료 시 손상된 미완료 레코드 이전 정상 지점까지 복구 완료 후 트렁케이트하여 정상 기동 보장 |
| **`BUG-CATALOG-INTERCEPT-BRACKET-1`** | **가상 카탈로그 대괄호 식별자 인터셉트 보장 (Catalog Bracket Bug)** | `BUG-CATALOG-INTERCEPT-BRACKET-1` | 정규화 단계 대괄호 및 스키마 접두사 트림으로 SSMS/sqlcmd 가상 뷰 조인 쿼리 100% 정상 인터셉트 |
| **`BUG-SELECT-AGG-CASE-1`** | **GROUP BY 및 집계함수 대소문자 정합성 보장 (Select Agg Case Bug)** | `BUG-SELECT-AGG-CASE-1` | `computeAggregates` 행 맵 대소문자 무관 탐색으로 `GROUP BY <nil>` 및 집계함수 NULL 변조 방지 |
| **`BUG-SP-PARAM-DECIMAL-1`** | **sp_executesql 복합 파라미터 타입 파싱 보존 (SP Param Decimal Bug)** | `BUG-SP-PARAM-DECIMAL-1` | 괄호 중첩 추적으로 `DECIMAL(10, 2)` 등 다중 인자 타입 콤마 분해 절단 및 유령 파라미터 생성 방지 |
| **`BUG-SP-EXEC-SUFFIX-OUT-1`** | **sp_executesql OUT 접미 변수명 오절단 방지 (SP Suffix OUT Bug)** | `BUG-SP-EXEC-SUFFIX-OUT-1` | `cleanOutputSuffix` 선행 공백/단어 경계 검사로 `@timeout` 등 `OUT` 접미 변수명 3글자 절단 훼손 해결 |
| **`BUG-MEM-INDEX-LOCAL-ORDER-1`** | **MemEngine 로컬 삽입 행 인덱스 정렬 보장 (MemEngine Index Order Bug)** | `BUG-MEM-INDEX-LOCAL-ORDER-1` | 트랜잭션 로컬 삽입 행 Go 맵 무작위 순회 append 제거 및 인덱스 키 병합 정렬 적용 |
| **`BUG-STORAGE-CMP-INT-FLOAT-1`** | **Value.Compare Int-Float 상호 강제변환 보장 (Compare Int-Float Bug)** | `BUG-STORAGE-CMP-INT-FLOAT-1` | `IntValue(100) > FloatValue(200.5)` 등 타입 불일치 비교 시 미초기화 0값 비교 역전 해결 |
| **`BUG-DERIVED-JOIN-STAR-1`** | **파생테이블 SELECT * 조인 타 테이블 프로젝션 보존 (Derived Join Star Bug)** | `BUG-DERIVED-JOIN-STAR-1` | `resolveDerivedTables`의 `reSelectStarFrom` 교체 시 조인 내 타 테이블 컬럼 증발 방지 |
| **`BUG-DDL-SPLIT-QUOTE-1`** | **CREATE TABLE splitColumnDefs 따옴표 추적 (DDL Split Quote Bug)** | `BUG-DDL-SPLIT-QUOTE-1` | `DEFAULT 'hello, world'` 콤마 및 `DEFAULT '('` 괄호로 인한 컬럼 정의 절단/합쳐짐 해결 |
| **`BUG-ARITH-EXPR-ZERO-1`** | **UPDATE 산술식 유연성 및 0 리셋 방지 (Arith Expr Zero Bug)** | `BUG-ARITH-EXPR-ZERO-1` | `[Price] + 10`, `a + b`, `10 + a` 등 수식 매칭 실패로 인한 컬럼 0 리셋 방지 |
| **`BUG-DROP-INDEX-CASE-1`** | **DropIndex 대소문자 정규화 및 좀비 인덱스 방지 (Drop Index Case Bug)** | `BUG-DROP-INDEX-CASE-1` | `td.Indexes` 맵 삭제 시 공식 인덱스 명칭(`foundIdx.Name`) 삭제로 좀비 인덱스 잔존 방지 |
| **`BUG-AGG-MIXED-NUM-DEC-1`** | **AggregateIterator Decimal-정수 혼합 합계 유실 방지 (Agg Mixed Decimal Bug)** | `BUG-AGG-MIXED-NUM-DEC-1` | 그룹 내 Decimal 출현 시 비-Decimal 수치 Decimal 변환 및 합산으로 수치 누락 방지 |
| **`BUG-PROC-SIMPLE-CASE-1`** | **단순 CASE 문(Simple CASE) 파싱 및 컴파일 보장 (Simple CASE Bug)** | `BUG-PROC-SIMPLE-CASE-1` | `CaseExpr`에 `Operand` 필드 신설 및 VDBE 피연산자-조건값 비교 분기 바이트코드 방출 |
| **`BUG-EVAL-NEQ-NULL-1`** | **evalRowBinaryOp 불일치 비교 NULL 3가 논리 보장 (Eval NEQ NULL Bug)** | `BUG-EVAL-NEQ-NULL-1` | `TokenNeq` 연산자 NULL 피연산자 시 `nil`(UNKNOWN) 반환으로 WHERE 절 누수 원천 차단 |
| **`BUG-DATEADD-NULL-1`** | **DATEADD/EOMONTH 증분 NULL 시 NULL 정규 반환 (DateAdd NULL Bug)** | `BUG-DATEADD-NULL-1` | 증분 파라미터 NULL 검사 및 NULL 즉시 반환, 미지원 datepart 에러 정규화 |
| **`VPERF-1` ~ `VPERF-14`** | **수직 핫패스 최적화 (Vertical Perf)** | `VPERF-1` ~ `VPERF-14` | TDS 와이어 프로토콜 ➔ 코디네이터 ➔ VDBE VM ➔ 스토리지 엔진(OCC/WiredTiger) 전 계층 수직 성능 최적화 트랙 |

---

## 2. 우선순위 레벨 체계 (Priority Levels: `P1` ~ `P3`)

| 우선순위 | 영역 | 대상 과제 식별자 | 대상 소스코드 위치 | 상태 및 주요 목표 |
| :---: | :---: | :--- | :--- | :---: |
| **P1** | **최적화** | **`D-9`**, **`B-3`** | `pkg/planner`, `pkg/execution` | **완료**: CBO 옵티마이저 힌트 오버라이드 및 소트-머지 조인 완비 |
| **P2** | **성능** | **`P-5`** | `pkg/engine`, `pkg/storage` | **완료**: 단순 PK 조회 고속 바이패스 캐시 완비 (TPS 50%+ 향상) |
| **P2** | **프로토콜** | **TDS NBCROW** | `pkg/wire/tds` | **완료**: TDS 행 압축(`0xD2`) 기반 네트워크 대역폭 절감 및 NULL 비트맵 지원 |
| **P3** | **생태계** | **드라이버 E2E** | `pkg/wire`, `pkg/wire/tds` | **진행 예정**: Python(`psycopg3`, `pymssql`), Node.js, .NET 드라이버 E2E 하네스 |

---

## 3. 스토리지 & 엔진 성능 5대 트랙 (`P-1` ~ `P-5`)

스토리지 엔진 트랜잭션, 직렬화/역직렬화, 네트워크 와이어 I/O, 메모리 캐시 전반의 병목을 제거하기 위한 핵심 고속화 과제입니다.

| 코드 | 전략 분야 | 핵심 구현 과제 | 상태 | 구현 상세 및 검증 지표 | 대상 패키지 |
| :---: | :--- | :--- | :---: | :--- | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 & 무경합 Fast-Path** | **완료** | `memengine` WAL 및 `wiredtiger` 리더/팔로워 그룹 커밋 구현. `FsyncAlways` 마이크로벤치마크 100배 가속(0.038ms/op), TPC-C 2.8K TPS 검증. | `pkg/storage/memengine`, `pkg/storage/wiredtiger` |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 (Lazy Decoding)** | **대기** | 풀 스캔 시 WHERE 절 컬럼만 선별 디코딩하고 조건 통과 행만 전체 언패킹하여 CPU 및 힙 할당 70% 삭감. `CREATE INDEX ... INCLUDE` 커버링 인덱스 결합. | `pkg/storage`, `pkg/execution`, `pkg/catalog` |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩 & I/O 버퍼 결합** | **대기** | 중간 `[]any` 레코드 슬라이스 박싱을 제거하고 `sync.Pool` 버퍼에 바이너리 튜플 데이터를 직접 포맷팅하여 소켓 syscall 횟수 최소화. | `pkg/wire`, `pkg/storage` |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next)** | **대기** | Volcano 반복자에서 1행 단위 함수 호출 대신 64~1024건 청크 단위 배치 실행을 적용하여 대량 집계 및 스캔 지연 60% 단축. | `pkg/execution` |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱 & 단순 PK 조회 바이패스** | **완료** | `SELECT * FROM tbl WHERE id = ?` 단순 PK 점조회에 대해 FNV-1a 지문 해싱 및 바운디드 캐시 기반으로 파서/플래너/VDBE 우회 스토리지(`tx.Get`) 직결 반환. DDL 시 자동 무효화. | `pkg/engine`, `pkg/storage` |

---

## 4. CBO 비용 기반 옵티마이저 4대 발전 트랙 (`A` ~ `D`, 총 9개 과제)

비용 기반 옵티마이저(Cost-Based Optimizer)의 통계 수집, 조인 플래닝, 선택도 평가, 운영 제어 고도화 과제입니다.

### Track A: 통계 수집 & 영속화 (Statistics Collection & Persistence)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :---: | :--- | :--- |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료** | 테이블 내 컬럼별 정렬 및 64-버킷 히스토그램 수집을 `sync.WaitGroup` 고루틴 풀로 병렬화(`BuildTableStatsParallel`). 대용량 테이블 분석 시간 단축. | `pkg/catalog`, `pkg/engine` |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료** | 원자적 임시 파일 스왑을 통해 `cbo_stats.json` 파일에 카디널리티, NDV, 히스토그램을 디스크 저장하고 엔진 기동 시 무손실 자동 복원. | `pkg/catalog` |

### Track B: 조인 알고리즘 & 순서 탐색 (Join Algorithms & Enumeration)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :---: | :--- | :--- |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델** | **완료** | 양측 입력이 사전 정렬(인덱스 순서 또는 `ORDER BY`)된 경우 해시 테이블 빌드 오버헤드를 제거하는 $O(M+N)$ `MergeJoinIterator` 스트리밍 조인 구현. 중복 키 Mark & Restore 슬라이딩 윈도우 지원. | `pkg/execution`, `pkg/planner` |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger)** | **완료** | 5개 이상 다중 테이블 조인 시 팩토리얼($N!$) 전수 탐색 대신 Left-Deep DP 및 Bushy Tree 동적 계획법(DPccp) 탐색을 적용하여 최적 조인 트리 도출. | `pkg/planner` |

### Track C: 선택도 & 통계 정밀화 (Selectivity & Distribution Modeling)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :--- :--- | :--- |
| **C-5** | **다중 컬럼 확장 통계 (Extended Statistics)** | **완료** | 컬럼 간 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 수집 및 결합 선택도 산출. | `pkg/catalog`, `pkg/planner` |
| **C-6** | **OR 조건 및 불리언 트리 재귀 선택도 계산** | **완료** | `TableStats.EstimateExprSelectivity()` 신설. 동일 컬럼 배타 합집합 $P(A)+P(B)$ 및 독립 합집합 $P(A)+P(B)-P(A)P(B)$, NOT, IN, BETWEEN 재귀 트리 평가 완비. | `pkg/planner` |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **완료** | 왜곡(Skew)이 심한 상위 N개 최빈값은 MCV 목록으로 별도 보관하고 균등 분포는 히스토그램으로 이원화 처리하여 선택도 오차 최소화. | `pkg/catalog`, `pkg/planner` |

### Track D: 운영 & 실행 제어 (Operations & Optimizer Control)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :--- :--- | :--- |
| **D-8** | **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze)** | **완료** | 테이블 DML 변경 비율(예: 10% 이상 Row 변동 발생)을 추적하여 백그라운드 워커에서 비동기로 `ANALYZE` 자동 트리거. | `pkg/catalog`, `pkg/engine` |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **완료** | 테이블 힌트 `WITH (INDEX(...))` 및 쿼리 힌트 `OPTION (HASH/MERGE/LOOP JOIN, FORCE ORDER)` 파싱 및 CBO 플래너 비용 계산 강제 오버라이드. | `pkg/planner`, `pkg/proc` |

---

## 5. T-SQL & ANSI SQL 문법 완비 트랙 (`S-1` ~ `S-7`, 전 항목 완료)

LionSQL 네이티브 파서 및 바이트코드 엔진에서 공식 지원하는 7대 핵심 SQL 문법 체계입니다.

| 코드 | 과제명 | 상태 | 구현 상세 및 지원 문법 | 대상 패키지 |
| :---: | :--- | :---: | :--- | :--- |
| **S-1** | **T-SQL & ANSI 표준 페이징 정규화** | **완료** | `OFFSET n ROWS FETCH NEXT m ROWS ONLY`, `OFFSET n ROWS`, `LIMIT` 및 `TOP (n)` AST 정규 통합 및 VDBE 바이트코드 컴파일 완비. | `pkg/proc`, `pkg/engine` |
| **S-2** | **복합 한정자 & 인라인 별칭 파싱** | **완료** | `[db].[schema].[tbl].[col]`, `"schema"."tbl"` 4-part 식별자 및 `AS` 없는 인라인 컬럼/테이블 별칭(`FROM accounts basetbl`) 파싱 정밀화. | `pkg/proc`, `pkg/engine` |
| **S-3** | **FROM 절 파생 테이블 & 인라인 서브쿼리** | **완료** | `FROM (SELECT ...) AS t`, `JOIN (SELECT ...) AS o` 서브쿼리 AST 노드화, 재귀 스키마 추론 및 임시 격리 구체화(`__dt_*`) 완비. | `pkg/proc`, `pkg/engine` |
| **S-4** | **스칼라 함수 & 조건부 수식 AST화** | **완료** | `COALESCE`, `ISNULL` 단락 평가(Short-circuit), `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT`, `::` 타입 캐스팅 및 `CASE WHEN` 완비. | `pkg/proc`, `pkg/engine` |
| **S-5** | **고급 조인 문법 확장** | **완료** | `FULL [OUTER] JOIN`, `RIGHT [OUTER] JOIN`, `CROSS JOIN`, T-SQL 특화 `CROSS/OUTER APPLY` 볼케이노 해시조인 파이프라인 완비. | `pkg/proc`, `pkg/execution` |
| **S-6** | **세션 제어문(SET Statements) 정식 바인딩** | **완료** | `SetOptionStmt` AST, VDBE 컴파일, 다중 옵션 쉼표 구분 및 세션 플래그(`SET NOCOUNT ON`, `SET ANSI_NULLS ON` 등) 반영. | `pkg/proc`, `pkg/engine` |
| **S-7** | **EXISTS / NOT EXISTS 상관 서브쿼리** | **완료** | `WHERE [NOT] EXISTS (...)` 조건의 Semi-Join / Anti-Join CBO 변환 및 비상관 서브쿼리 조기 단락 평가 완비. | `pkg/planner`, `pkg/execution` |

---

## 6. 연계 문서 및 진실 공급원 (SSOT)

- **액티브 백로그 및 단기 우선순위**: [`TODO.ko.md`](../TODO.ko.md)
- **중장기 거시 릴리즈 로드맵 (Phase 1~6)**: [`ROADMAP.md`](../ROADMAP.md)
- **엔지니어링 불변식 및 개발 규약**: [`notes/ENGINEERING_CONTEXT.md`](ENGINEERING_CONTEXT.md)
- **실전 개발 함정 및 디커플링 패턴**: [`notes/PITFALLS_AND_TRICKS.md`](PITFALLS_AND_TRICKS.md)
- **아키텍처 설계 문서**: [`doc/ARCHITECTURE.md`](../doc/ARCHITECTURE.md)
