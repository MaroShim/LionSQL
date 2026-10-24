# LionSQL 데이터 무결성 107대 결함 조치 아카이브 (TODO-BUGS.ko.md)

본 문서는 LionSQL 코드베이스의 안정화, SQL 표준 식별자 대소문자 정규화, 그리고 무작위 순회/임의 추측 안티패턴을 원천 제거하기 위해 추진되었던 **107대 치명적 무결성 결함(`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`) 전수 조치 기록 및 아카이브**입니다.

> [!NOTE]
> 본 결함 조치 트랙은 초기 코드베이스의 급속한 무결성 안정화를 위해 사용된 중간 방법론(Interim Methodology)이며, 현재 107건 전수 조치 및 100% 회귀 검증(Build & Race-Test)이 완료되었습니다.
> 이후의 신규 결함 추적, 정적 분석 및 구조적 아키텍처 개편은 다음의 전용 대시보드를 통해 진행됩니다:
> - **코드 리뷰 및 결함 감사 트래커**: [`CODE_REVIEW.ko.md`](CODE_REVIEW.ko.md)
> - **아키텍처 추상화 및 리팩토링 명세**: [`REFACTORING.ko.md`](REFACTORING.ko.md)
> - **전체 과제 분류 체계**: [`notes/TAXONOMY.ko.md`](notes/TAXONOMY.ko.md)
> - **활성 엔지니어링 백로그**: [`TODO.ko.md`](TODO.ko.md)

---

## 1. 107대 결함 식별 코드 일람표 (Taxonomy & Catalog)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 체계 | 관리 목적 및 설명 |
| :---: | :--- | :---: | :--- |
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
| **`BUG-VM-INSERT-DEFAULT-1`** | **VDBE OpInsert 상수 리터럴 기본값 주입 보장 (VM Insert Default Bug)** | `BUG-VM-INSERT-DEFAULT-1` | 6대 내장 함수 외 상수 리터럴 기본값(`DEFAULT 0`, `DEFAULT 'active'`) 누락 및 NULL 삽입 해결 |
| **`BUG-VM-DML-COL-CASE-1`** | **VDBE DML 컬럼명 대소문자 정규화 및 값 보존 (VM DML Col Case Bug)** | `BUG-VM-DML-COL-CASE-1` | Record 컬럼 대소문자 차이로 인한 `RecordToRow`/`updateLocked` 값 유실 및 NULL 삽입 해결 |
| **`BUG-DML-PK-RANDOM-GUESS-1`** | **DML/DDL PK 식별 실패 시 임의 순회 행 훼손 방지 (DML PK Guess Bug)** | `BUG-DML-PK-RANDOM-GUESS-1` | `Values[0]` 및 `for range rec` 무작위 순회 완전 삭제, 공식 PK 엄격 조회로 엉뚱한 행 훼손 원천 차단 |
| **`BUG-AUTH-ADMIN-PW-BYPASS-1`** | **내장 슈퍼유저 비밀번호 인증 완전 우회 백도어 해결 (Auth Admin PW Bypass Bug)** | `BUG-AUTH-ADMIN-PW-BYPASS-1` | PGWire 및 TDS에서 비밀번호가 설정된 `sa`, `postgres`, `admin`의 무암호 로그인 우회 차단 |
| **`BUG-UPDATE-SET-COMMA-SPLIT-1`** | **코디네이터 UPDATE SET 절 쉼표 오절단 방지 (Update Set Comma Bug)** | `BUG-UPDATE-SET-COMMA-SPLIT-1` | 따옴표 및 괄호 상태 추적 토큰 분해기로 문자열 리터럴/함수 인자 콤마 오분해 및 수식 파괴 해결 |
| **`BUG-EVAL-IN-COMMA-SPLIT-1`** | **WHERE IN 절 문자열 리터럴 쉼표 오절단 방지 (Eval IN Comma Bug)** | `BUG-EVAL-IN-COMMA-SPLIT-1` | 따옴표 상태 인식 스마트 IN 분해기로 콤마 포함 문자열 리터럴 절단 및 매칭 영구 실패 해결 |
| **`BUG-STORAGE-CMP-DATETIME-STR-1`** | **DateTime-VarChar 상호 변환 누락 비교 역전 방지 (Storage Cmp DateTime Bug)** | `BUG-STORAGE-CMP-DATETIME-STR-1` | 날짜/시간과 문자열 간의 상호 파싱/강제변환으로 올바른 시간 축 비교 및 필터 왜곡 해결 |
| **`BUG-STORAGE-EQUAL-FLOAT-STR-1`** | **실수 6자리 문자열 포맷팅 동등 비교 왜곡 방지 (Storage Equal Float Bug)** | `BUG-STORAGE-EQUAL-FLOAT-STR-1` | 실수 문자열 포맷팅 제거 및 부동소수점 비트/엡실론 비교로 Compare/Equal 정합성 보장 |
| **`BUG-MEM-TX-TABLE-CASE-1`** | **트랜잭션 커밋 시 테이블 대소문자 사일런트 쓰기 증발 방지 (MemEngine Tx Case Bug)** | `BUG-MEM-TX-TABLE-CASE-1` | 변경 레코드 테이블명 스키마 공식 명칭 정규화로 커밋 시 샤드 미반영 쓰기 유실 차단 |
| **`BUG-MEM-TX-INDEX-CASE-1`** | **MemEngine IndexScan 인덱스명 대소문자 무관 탐색 보장 (MemEngine Index Case Bug)** | `BUG-MEM-TX-INDEX-CASE-1` | 인덱스 맵 1차 실패 시 대소문자 무관 순회 보강으로 `ErrIndexNotFound` 및 풀스캔 강제 방지 |
| **`BUG-MERGE-IDENTITY-PK-1`** | **MERGE WHEN NOT MATCHED IDENTITY 자동 채번 보장 (Merge Identity PK Bug)** | `BUG-MERGE-IDENTITY-PK-1` | MERGE INSERT 시 IDENTITY 자동 채번 및 세션/테이블 식별자 동기화 연동 |
| **`BUG-API-CREATE-DEFAULT-1`** | **HTTP API 레코드 생성 시 NOT NULL DEFAULT 필드 주입 (API Create Default Bug)** | `BUG-API-CREATE-DEFAULT-1` | 생략된 필드 대상 스키마 기본값(리터럴 및 함수 기본값) 자동 주입으로 400 에러 해결 |
| **`BUG-CATALOG-MSSQL-REGEX-UNICODE-1`** | **가상 카탈로그 N'...' 리터럴 및 대괄호 식별자 매칭 (Catalog Regex Unicode Bug)** | `BUG-CATALOG-MSSQL-REGEX-UNICODE-1` | `N'...'` 접두사 및 대괄호 허용 견고한 카탈로그 정규식으로 SSMS/DBeaver 전수 덤프 방지 |
| **`BUG-WIRE-COPY-TX-BYPASS-1`** | **COPY FROM STDIN 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Copy Tx Bypass Bug)** | `BUG-WIRE-COPY-TX-BYPASS-1` | 활성 세션 컨텍스트 바인딩으로 트랜잭션 범위 내 원자적 COPY 및 롤백 보장 |
| **`BUG-WIRE-COPY-SILENT-CORRUPT-1`** | **COPY FROM STDIN 파싱 에러 침묵 및 0값/현재시각 변조 방지 (Copy Silent Corrupt Bug)** | `BUG-WIRE-COPY-SILENT-CORRUPT-1` | 파싱 실패 시 명시적 SQL 구문 에러 반환 및 전체 데이터 타입 정밀 파싱 지원 |
| **`BUG-WIRE-COPY-OUT-UNESCAPED-1`** | **COPY TO STDOUT 텍스트 포맷 탭/줄바꿈 이스케이프 보장 (Copy Out Unescaped Bug)** | `BUG-WIRE-COPY-OUT-UNESCAPED-1` | 표준 PostgreSQL 텍스트 포맷 이스케이프 규칙(`\t`, `\n`, `\\`) 적용으로 덤프 정합성 보장 |
| **`BUG-EXEC-JOIN-STR-CASE-1`** | **HashJoin/MergeJoin 문자열 조인 키 대소문자 무관 매칭 보장 (Join Str Case Bug)** | `BUG-EXEC-JOIN-STR-CASE-1` | 조인 키 해싱 및 비교 시 대소문자 무관 정규화 적용으로 SQL 표준 조인 매칭 보장 |
| **`BUG-PIVOT-COUNT-NULL-1`** | **PIVOT COUNT 집계 시 빈 카테고리 0 정규 반환 (Pivot Count NULL Bug)** | `BUG-PIVOT-COUNT-NULL-1` | `COUNT` 집계에 대해 빈 그룹 시 `0`을 정규 방출하도록 보강 |
| **`BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1`** | **FOR JSON PATH 테이블 별칭 중첩 객체 오해석 방지 (For JSON Alias Bug)** | `BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1` | 테이블 별칭과 컬럼 별칭 구분으로 순수 컬럼 프로젝션 시 불필요 래핑 제거 |
| **`BUG-DDL-COMPOSITE-PK-PARSE-1`** | **CREATE TABLE 복합 기본키 콤마 분해 및 스키마 보존 (DDL Composite PK Bug)** | `BUG-DDL-COMPOSITE-PK-PARSE-1` | 괄호 내부 콤마 분해 루프로 선언된 모든 복합 PK 컬럼 플래그 정상 부여 |
| **`BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1`** | **RBAC 사용자 커스텀 롤 테이블 권한 전수 누락 방지 (RBAC Role Grant Bug)** | `BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1` | `HasTablePermission`에서 내장 롤만 검사하고 사용자 소속 커스텀 롤(`u.Roles`) 권한 누락으로 인한 인가 실패 해결 |
| **`BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1`** | **WHERE 절 이스케이프 따옴표 및 대괄호 식별자 AND 오절단 방지 (Planner Split AND Bug)** | `BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1` | `splitAndConditions`의 중첩 따옴표(`''`) 및 대괄호 식별자 미인식으로 인한 WHERE 조건 파괴 및 플래너 에러 해결 |
| **`BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1`** | **SkipList 유니크 인덱스 재인덱싱 시 커버링 컬럼 복사본 갱신 방지 (SkipList Unique Include Bug)** | `BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1` | `curr.Items` 값 복사 순회(`for _, item`)로 인한 커버링 컬럼(`IncludeValues`) 갱신 누락 및 인덱스 조회 왜곡 해결 |
| **`BUG-MEM-SHARD-INIT-RACE-1`** | **TableData 샤드 지연 초기화 동시성 경쟁 런타임 패닉 방지 (MemEngine Shard Init Race Bug)** | `BUG-MEM-SHARD-INIT-RACE-1` | `ensureShardsInit`의 락 없는 맵 초기화로 인한 동시 읽기 시 `fatal error: concurrent map writes` 크래시 방지 (`sync.Once`) |
| **`BUG-DATE-TODATETIMEOFFSET-INT-MINS-1`** | **TODATETIMEOFFSET 정수 분 단위 오프셋 시간 오해석 방지 (ToDateTimeOffset Mins Bug)** | `BUG-DATE-TODATETIMEOFFSET-INT-MINS-1` | T-SQL 규격 정수 오프셋(분 단위)을 시간 단위로 60배 왜곡 포맷팅하던 `parseOffsetLocation` 파싱 정합성 보장 |
| **`BUG-CATALOG-FK-MULTI-COL-ACTIONS-1`** | **가상 카탈로그 복합 외래키 쉼표 병합 및 액션 왜곡 방지 (Catalog Multi Col FK Bug)** | `BUG-CATALOG-FK-MULTI-COL-ACTIONS-1` | `execDbGateMSSQLForeignKeySelect` 복합 외래키 단일 행 쉼표 결합 및 CASCADE/SET NULL 액션 누락 방지 |
| **`BUG-VM-DML-PK-NOT-FOUND-CRASH-1`** | **VDBE OpUpdate/OpDelete PK 미존재 시 ErrKeyNotFound 예외 크래시 방지 (VM DML PK Not Found Bug)** | `BUG-VM-DML-PK-NOT-FOUND-CRASH-1` | VDBE 단일 PK 수정/삭제 시 대상 행 미존재 시 에러 중단 대신 표준 SQL 규격 준수 0건 처리(`@@ROWCOUNT = 0`) 보장 |
| **`BUG-EXEC-PROC-TX-BYPASS-1`** | **EXEC 프로시저 호출 시 외부 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Exec Proc Tx Bypass Bug)** | `BUG-EXEC-PROC-TX-BYPASS-1` | `execProcedureCall`에서 활성 트랜잭션(`cs.Tx`) 공유로 프로시저 내 수정 사항의 트랜잭션 원자성 및 ROLLBACK 보장 |
| **`BUG-TDS-RPC-PREP-TX-BYPASS-1`** | **TDS RPC 사전 컴파일 플랜 실행 시 세션/트랜잭션 우회 방지 (TDS RPC Prep Tx Bypass Bug)** | `BUG-TDS-RPC-PREP-TX-BYPASS-1` | `sp_execute`/`sp_prepexec` 패스트패스에서 독립 스토리지 세션 대신 세션 컨텍스트 및 `inTx` 트랜잭션 바인딩 |
| **`BUG-TDS-RPC-PARAM-NAME-ORDER-1`** | **TDS sp_executesql 파라미터명 미인식 순서 왜곡 방지 (TDS RPC Param Name Bug)** | `BUG-TDS-RPC-PARAM-NAME-ORDER-1` | `handleRPCExecuteSQL`에서 파라미터명(`@stmt`, `@params`) 검사로 JDBC/.NET 클라이언트 명명 파라미터 도치 오류 해결 |
| **`BUG-PARSER-SIMPLE-CASE-OMISSION-1`** | **Pratt 파서 Simple CASE 피연산자 누락 및 문법 거부 방지 (Parser Simple Case Bug)** | `BUG-PARSER-SIMPLE-CASE-OMISSION-1` | `parseCase`에서 `CASE expr WHEN ...` 단순 CASE 피연산자 인식 및 `CaseExpr.Operand` AST 필드 보강 |
| **`BUG-PIVOT-ORDER-BY-SPLIT-1`** | **PIVOT/UNPIVOT ORDER BY 절 쉼표 분해 왜곡 방지 (Pivot Order By Split Bug)** | `BUG-PIVOT-ORDER-BY-SPLIT-1` | `strings.Fields` 대신 스마트 정렬 파서 적용으로 쉼표 포함 정렬 키(`col,`, `DESC,`) 파괴 및 정렬 무력화 해결 |
| **`BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1`** | **코디네이터 UPDATE 산술식 미지원 및 0값/타입변조 방지 (Eval Update Arith Bug)** | `BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1` | 0으로 나누기 예외 처리 및 컬럼 간 연산 시 문자열 폴백 방지로 데이터 사일런트 변조 차단 |
| **`BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1`** | **WAL 복구 시 CREATE TABLE 인라인 인덱스 등록 누락 방지 (WAL Create Table Index Bug)** | `BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1` | `OpCreateTable` 재생 시 `op.Schema.Indexes` 순회 초기화로 충돌 복구 후 인덱스 갱신 누락 해결 |
| **`BUG-WINDOW-RANGE-PEER-EVAL-1`** | **윈도우 함수 RANGE 피어 프레임 집계 왜곡 방지 (Window Range Peer Bug)** | `BUG-WINDOW-RANGE-PEER-EVAL-1` | `ORDER BY` 시 표준 기본값인 `RANGE` 피어 구간 확장을 구현하여 동순위 행 간 누적 집계 왜곡 해결 |
| **`BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1`** | **단일 트랜잭션 내 다중 유니크 충돌 사일런트 커밋 방지 (OCC Intra Tx Unique Bug)** | `BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1` | `tx.changes` 내부 상호 유니크 충돌 사전 검증 및 `idxData.Insert` 에러 전파로 중복 커밋 원천 차단 |
| **`BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1`** | **MemEngine updateLocked 유니크 인덱스 사전 검증 누락 방지 (OCC Update Unique Bug)** | `BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1` | 레코드 수정(`Update`) 시 유니크 인덱스 즉각 검증 로직 추가로 트랜잭션 내 중복 키 갱신 방어 |
| **`BUG-API-JSON-DATETIME-EMPTY-STR-1`** | **HTTP API 레코드 조회 시 DATETIME 빈 문자열 직렬화 방지 (API JSON DateTime Bug)** | `BUG-API-JSON-DATETIME-EMPTY-STR-1` | `recordToJSONMap`에서 `v.SVal` 대신 `v.String()` / `v.TVal` 정규 포맷팅으로 일시 데이터 유실 해결 |
| **`BUG-API-JSON-DECIMAL-PRECISION-LOSS-1`** | **HTTP API 레코드 직렬화 시 DECIMAL 실수 부동소수점 정밀도 손실 방지 (API Decimal Loss Bug)** | `BUG-API-JSON-DECIMAL-PRECISION-LOSS-1` | `strconv.ParseFloat` 변환을 제거하고 정밀 문자열 또는 `json.Number`로 직렬화하여 금융 데이터 왜곡 방지 |
| **`BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1`** | **0건 파생테이블 Columns[0] 임의 PK 지정 및 제약 파괴 방지 (Derived Table PK Bug)** | `BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1` | 0건 반환 시 임의 컬럼 PK 승격을 중단하고 항상 가상 기본키(`_dt_row_id`)를 부여하여 스키마 정합성 보장 |
| **`BUG-PITR-DDL-SILENT-SWALLOW-1`** | **PITR WAL 재생 시 DDL 실행 에러 침묵 및 복구 실패 은폐 방지 (PITR DDL Swallow Bug)** | `BUG-PITR-DDL-SILENT-SWALLOW-1` | DDL 실행 실패(`CreateTable`, `DropTable` 등) 시 에러 즉시 전파로 후속 데이터 파괴 및 복구 왜곡 방지 |
| **`BUG-STORAGE-TABLE-CASE-SENSITIVITY-1`** | **스토리지 엔진 테이블명 대소문자 미구분 맵 조회 실패 및 중복 생성 방지 (Storage Table Case Bug)** | `BUG-STORAGE-TABLE-CASE-SENSITIVITY-1` | MemEngine/WiredTiger의 `e.tables` 원시 맵 조회를 대소문자 무시 정규화(`EqualFold` / `ToLower`)하여 `ErrTableNotFound` 및 동명이인 테이블 방어 |
| **`BUG-EXEC-AGG-DISTINCT-IGNORED-1`** | **AggregateIterator DISTINCT 집계 무시 및 전체 행 집계 왜곡 방지 (Agg Distinct Ignored Bug)** | `BUG-EXEC-AGG-DISTINCT-IGNORED-1` | 정규식 `(?:DISTINCT\s+)?` 일치 시 `isDistinct` 플래그 보존 및 `groupAccumulator` 고유값 셋 추적으로 중복 집계 왜곡 차단 |
| **`BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1`** | **코디네이터 집계 MIN/MAX 비숫자형 파싱 실패 시 NULL 반환 방지 (Coord Agg MinMax Bug)** | `BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1` | `strconv.ParseFloat` 전용 파싱을 문자열/일시/불리언 비교 인터페이스로 확장하여 문자열 및 날짜 컬럼의 극값 유실 해결 |
| **`BUG-PGWIRE-PREP-EXEC-TX-BYPASS-1`** | **PGWire 사전 컴파일 바이트코드 실행 시 트랜잭션 우회 방지 (PGWire Prep Tx Bypass Bug)** | `BUG-PGWIRE-PREP-EXEC-TX-BYPASS-1` | `handleExecute` 패스트패스에서 신규 독립 세션(`NewSession`) 강제 생성을 중단하고 활성 세션 컨텍스트 및 트랜잭션 바인딩 |
| **`BUG-WIRE-PG-PARAM-SUBSTITUTION-INJECTION-1`** | **PGWire 파라미터 치환 시 재귀 치환 왜곡 및 숫자형 문자열 따옴표 탈락 방지 (PG Param Substitute Bug)** | `BUG-WIRE-PG-PARAM-SUBSTITUTION-INJECTION-1` | `strings.ReplaceAll` 대신 단일 패스 토큰화 치환 적용 및 파라미터 실제 데이터 타입 보존으로 파라미터 재귀 오염 해결 |
| **`BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`** | **서버 재기동 시 프로시저/함수/뷰/트리거 영구 증발 방지 (Catalog Object Persistence Bug)** | `BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1` | 인메모리 맵 보관 객체들을 WAL 레코드 또는 `catalog_objects.json`으로 영속화하여 데이터베이스 재시작 후 메타데이터 복원 보장 |
| **`BUG-WT-UNIQUE-INDEX-UNENFORCED-1`** | **WiredTiger 스토리지 엔진 보조 유니크 인덱스 제약 검증 및 색인 누락 방지 (WT Unique Index Bug)** | `BUG-WT-UNIQUE-INDEX-UNENFORCED-1` | `insertLocked`/`updateLocked` 시 유니크 인덱스 제약 검증 및 `IndexScan`의 풀 테이블 스캔 폴백을 B-Tree 커서 검색으로 개편 |
| **`BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1`** | **복합 조인 조건 '=' 분해 파괴 및 무제한 카테시안 곱 왜곡 방지 (Join Cond Split Bug)** | `BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1` | `parseJoinCond`에서 다중 복합 조건(`k1=v1 AND k2=v2`) 지원으로 빈 문자열("") 해시 매칭 및 카테시안 왜곡 해결 |
| **`BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1`** | **세션 임시테이블 스냅샷/WAL 영속화 및 재기동 후 영구 누수 방지 (Temp Table Persist Bug)** | `BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1` | `saveDiskLocked` 및 WAL 재생 시 `__temp_*` / `__tv_*` 필터링으로 종료 세션 임시 객체의 영구 카탈로그 오염 차단 |
| **`BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`** | **MERGE 구문 내 신규 삽입 행의 SOURCE 미일치 오인 즉각 삭제 방지 (Merge Self Destruct Bug)** | `BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1` | `WHEN NOT MATCHED` 삽입 행의 `matchedTargetKeys` 등록으로 동일 MERGE 문 내 `NOT MATCHED BY SOURCE`의 자가 삭제 방어 |
| **`BUG-STR-CHARINDEX-NULL-ZERO-1`** | **CHARINDEX/STRPOS의 NULL 인자 수신 시 0 반환 삼치논리 왜곡 방지 (CharIndex NULL Zero Bug)** | `BUG-STR-CHARINDEX-NULL-ZERO-1` | `CHARINDEX`와 `STRPOS`에서 NULL 인자 입력 시 0(검색실패) 대신 `NULL` 정상 전파로 ANSI/T-SQL 삼치논리 정합성 보장 |
| **`BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1`** | **CBO 선택도 추정 시 컬럼명 대소문자 불일치 0.33 휴리스틱 추락 방지 (CBO Selectivity Case Bug)** | `BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1` | `EstimateSelectivity`의 `ts.Columns` 직접 맵 조회를 `findColumnStats`로 대체하여 대소문자 무관 통계 조회 보장 |
| **`BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1`** | **OR 조건 하위 서브쿼리 불법 SEMI 조인 변환 및 'and' 식별자 파괴 방지 (Subquery Decorrelate Bug)** | `BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1` | OR 분기 내 서브쿼리 무단 변환 차단 및 `removeClause`의 경계 인식 토큰화로 `brand` 등 컬럼명 절단 및 문법 오류 해결 |
| **`BUG-BACKUP-RESTORE-VIEW-OMISSION-1`** | **백업 아카이브 생성 및 복원 시 뷰(View) 메타데이터 전무 누락 방지 (Backup Restore View Bug)** | `BUG-BACKUP-RESTORE-VIEW-OMISSION-1` | `CreateBackup`/`RestoreBackup`에 `catalog/views.json` 직렬화 및 복원 파이프라인을 추가하여 뷰 정의 영구 유실 방어 |

---

## 2. 107대 결함 세부 원인 분석 및 해결 명세 (Detailed Action Records)

#### 8. 데이터 무결성, 대소문자 정합성, 임의 추측 및 무작위 맵 순회 107대 치명적 결함 전수 조치 (`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`)
* **과제 개요**: SQL 표준의 식별자 대소문자 무관성(Case-insensitivity)과 Go 맵/스칼라의 대소문자 엄격 비교 불일치, 그리고 식별자/인덱스 조회 실패 시의 취약한 무작위 순회 폴백(`for _, v := range rec/row/map { break }`) 및 첫 번째 요소 임의 대입(`[0]`), 외래키 무검증/오판, NULL 전파 누락, WAL 복구 절단 처리 실패, Int-Float 및 DateTime-VarChar 비교 역전, 파생테이블 조인 프로젝션 유실, 혼합타입 집계합 누락, 기본값(DEFAULT) 누락, 관리자 인증 우회, MERGE IDENTITY 누락, 복합 PK 파싱 절단, COPY 프로토콜 세션 우회 및 데이터 변조, 쉼표 오절단, RBAC 커스텀 롤 누락, 따옴표 이스케이프 분해 파괴, 샤드 초기화 경쟁 크래시, VDBE 미존재 PK 크래시, EXEC 프로시저 및 TDS RPC 트랜잭션 우회, Simple CASE 누락, 산술식 0값 변조, 윈도우 함수 RANGE 피어 누락, OCC 동일 트랜잭션 유니크 충돌 사일런트 커밋, HTTP API DATETIME 빈 문자열 방출, PITR DDL 에러 침묵 등으로 인해 발생하는 107대 치명적 무결성 결함을 원천 해결.
* **세부 결함 명세 및 수락 기준**:
  1. **`BUG-PK-1` (`pkg/engine/sqlexec_dml.go:1071, 1228`, `sqlexec_ddl.go:363`, `pkg/proc/runtime/vm.go:940, 1076`)**:
     - `execUpdate`, `execDelete`, `execTruncate`, VDBE `execUpdateWhere`, `execDeleteWhere`에서 PK 미확정 시 `rec["id"]`를 하드코딩하고 Go 맵을 무작위 순회(`for _, v := range rec { break }`)하여 엉뚱한 행을 갱신/삭제하던 치명적 결함.
     - `schema.PrimaryKeyColumn()`을 통해 실제 PK 컬럼명을 정규 조회하고, 무작위 순회 코드를 완전 삭제하여 PK를 식별할 수 없는 경우 안전하게 에러를 반환.
  2. **`BUG-CASE-1` (`pkg/storage/engine.go:317`)**:
     - `TableSchema.FindColumn`이 `s.Columns[i].Name == name` 엄격 비교로 작성되어 DML, DDL, MERGE, ON CONFLICT, PK 바이패스, 복합 인덱스 범위 플래너에서 대소문자 불일치 시 컬럼 조회 실패를 초래하던 결함.
     - `strings.EqualFold`를 적용하여 대소문자 무관 탐색을 보장.
  3. **`BUG-RECORD-1` (`pkg/engine/sqlexec_dml.go:415, 1080`, `pkg/storage/tuple.go:63`)**:
     - `execUpdate`, `execInsert`에서 DML Record 맵을 구성할 때 쿼리 원문 `colName`(예: `"Name"`)을 키로 저장하여, 스토리지 병합 후 `RecordToRow`가 스키마 컬럼(`"name"`)만 읽어 업데이트 값이 사일런트 유실(Silent Loss)되거나 NULL로 삽입되던 결함.
     - `rec[colSchema.Name] = ...`으로 스키마 공식 컬럼명으로 정규화하여 저장하고, `RecordToRow`에서도 대소문자 방어 로직 추가.
  4. **`BUG-ARITH-1` (`pkg/engine/sqlexec_eval.go:925`)**:
     - `evalUpdateExpr`에서 `SET price = Price + 10`과 같은 수식 평가 시 `rec[colName]` 대소문자 불일치로 조회가 실패하면 `parseLiteralValue`로 떨어져 컬럼 값이 0으로 강제 리셋되던 결함.
     - `rec` 조회 시 대소문자 무관 탐색을 보장하여 정상 연산 값 반환.
  5. **`BUG-UPSERT-1` (`pkg/engine/sqlexec_dml.go:492, 841`)**:
     - `execInsert` 및 `execInsertSelect`의 `ON CONFLICT (col)` 충돌 행 검사 시 `r[cc]`와 `rec[cc]`가 대소문자 차이로 매칭에 실패하여 중복 키 위반 에러가 발생하던 결함.
     - 충돌 컬럼을 스키마 공식 명칭으로 정규화하고 대소문자 무관 비교 적용.
  6. **`BUG-WHERE-1` (`pkg/engine/sqlexec_eval.go:339`)**:
     - `evaluateSingleConditionMap`의 다른 조건들과 달리 `WHERE col IN (...)` 블록에서만 대소문자 무관 루프가 누락되어 쿼리 결과가 0건으로 누락되던 결함.
     - `strings.EqualFold` 대소문자 무관 탐색 루프 보강.
  7. **`BUG-VM-INTO-1` (`pkg/proc/runtime/vm_dispatcher.go:1046, 1128`)**:
     - VDBE `SELECT ... INTO @var` 및 `OpIndexScan INTO @var` 처리 시 `for _, v := range rows[0] { break }` 무작위 맵 순회로 변수에 프로젝션된 첫 번째 컬럼이 아닌 엉뚱한 임의의 컬럼 값이 대입되던 결함.
     - `cols[0]` 프로젝션 명칭을 통한 결정론적 조회(`toStorageValue(rows[0][cols[0]])`)로 수정.
  8. **`BUG-SUBQ-1` (`pkg/proc/eval.go:316-318`)**:
     - 스칼라 서브쿼리 수식 평가 시 `cols[0]` 조회가 실패할 경우 `for _, v := range firstRow { return v, nil }` 무작위 맵 순회로 임의 컬럼 값이 서브쿼리 결과로 반환되던 결함.
     - `cols[0]` 대소문자 무관 탐색 및 무작위 순회 완전 삭제.
  9. **`BUG-BOUNDS-1` (`pkg/planner/bounds.go:389`)**:
     - Volcano `IndexScanIterator` 생성 시 `ParseIndexCond`가 `for colName, valStr := range eqColVals { return &parsed, &parsed }` 무작위 맵 순회를 수행하여, 다중 등호 조건 시 인덱스 컬럼과 무관한 임의의 컬럼 값으로 인덱스를 검색하여 결과가 파괴되던 결함.
     - 대상 인덱스 컬럼과의 정합성 매칭으로 수정.
  10. **`BUG-EXP-1` (`pkg/engine/sqlexec_explain.go:703, 723, 760`)**:
     - `EXPLAIN` 서브쿼리 평탄화 시 `for _, v := range r { break }` 무작위 맵 순회로 서브쿼리 IN 리스트 및 스칼라 비교 연산자에 엉뚱한 컬럼 값이 대입되던 결함.
     - `innerRes.Columns[0]` 대소문자 무관 매칭 및 무작위 순회 완전 삭제.
  11. **`BUG-DDL-PK-1` (`pkg/engine/sqlexec_ddl.go:185`, `pkg/api/handlers_tables.go:162`)**:
     - `CREATE TABLE` 시 사용자가 PK를 지정하지 않은 경우 `if !hasPK && len(schema.Columns) > 0 { schema.Columns[0].PrimaryKey = true }`를 통해 첫 번째 컬럼(예: `created_at` 또는 `category`)을 임의로 Primary Key로 강제 승격시켜, 이후 동일 값의 행 INSERT 시 중복 키 위반 에러를 일으키는 치명적 결함.
     - 임의 컬럼 PK 승격을 영구 제거하고, 스토리지 내부 가상 PK(`_row_id`)를 투명하게 생성하도록 정규화.
  12. **`BUG-SELECT-INTO-PK-1` (`pkg/engine/sqlexec_select.go:930-965`, `pkg/engine/sqlexec_derived.go:94-118`)**:
     - `SELECT ... INTO` 및 파생 테이블 구체화 시, 초기 조회 결과에서 우연히 중복이 없는 컬럼이 있으면 해당 컬럼을 새 테이블의 영구 PK로 임의 지정하거나, 조회 결과가 0건일 때 `selRes.Columns[0]`을 PK로 지정하여 차후 DML 실패를 유발하는 결함.
     - 원본 테이블의 공식 PK가 보존되지 않은 경우 무조건 합성 PK(`_row_id` / `_dt_row_id`)를 생성하도록 일원화.
  13. **`BUG-JOIN-IDX-1` (`pkg/execution/builder.go:256`)**:
     - `IndexNestedLoopJoin` 구성 시 `innerPlan.IndexName`을 찾지 못하면 `else if len(innerSchema.Indexes) > 0 { innerIdx = &innerSchema.Indexes[0] }`로 조인 조건과 무관한 임의의 첫 번째 인덱스(예: `idx_email`)를 가져와 외부 조인 키(예: `user_id = 1001`)로 검색하여 조인 결과를 완전히 파괴하던 결함.
     - 일치하는 조인 인덱스가 없는 경우 안전하게 `HashJoin` 또는 `MergeJoin`으로 Fallback 전환.
  14. **`BUG-WT-INDEX-COMP-1` (`pkg/storage/wiredtiger/wt_engine.go:1588-1590, 1617-1620`)**:
     - WiredTiger `wtFilteredIndexCursor`에서 복합 인덱스 스캔 시 첫 번째 컬럼(`c.idx.Columns[0]`)만 필터링하고 나머지 복합 컬럼을 무시하며, `FindColumn` 실패 시 초기화되지 않은 `storage.Value{}`(TypeUnknown)가 `Compare`에서 0(동일함)을 반환하여 엉뚱한 행이 필터를 통과하던 결함.
     - 복합 인덱스 키 디코딩 및 미초기화 값의 비교 실패(Fail) 명시화.
  15. **`BUG-VM-COMP-IDX-1` (`pkg/proc/runtime/vm.go:1232-1237`)**:
     - VDBE `findMatchingIndex`에서 첫 번째 컬럼만 일치하면 복합 인덱스를 선택하여 스칼라 단일 값을 `tx.IndexScan`에 전달함으로써, `memengine` 복합 인덱스 NUL 인코딩과의 불일치로 인해 0건이 반환되던 결함.
     - 단일 컬럼 인덱스 우선 매칭 및 복합 인덱스 프리픽스 인코딩 정합성 보장.
  16. **`BUG-ORDER-BY-SPLIT-1` (`pkg/catalog/virtual/result.go:34`, `pkg/engine/sqlexec_dbcc.go:310`, `pkg/engine/sqlexec_pivot.go:421, 946`, `pkg/engine/sqlexec_select.go:1857, 2253`)**:
     - `orderParts := strings.Fields(orderByClause)`로 공백 분해하여 다중 컬럼 정렬(`ORDER BY a, b DESC`) 시 `b` 이후가 누락되고, `ORDER BY a DESC, b` 시 `"DESC,"` 콤마로 인해 `EqualFold`가 거짓이 되어 DESC가 ASC로 반전되며, 대소문자 미매칭으로 정렬이 무력화되던 6개 파일 공통 결함.
     - 표준 토큰 분해기 및 대소문자 무관 다중 정렬 슬라이스 비교기 적용.
  17. **`BUG-JOIN-PARSE-1` (`pkg/execution/builder.go:315`, `pkg/wire/server.go:1064, 1586`)**:
     - `strings.Split(cond, "=")`로 다중 조건(`a = b AND c = d`) 분해 실패 시 `"", ""`를 반환하여 빈 컬럼명 `" ASC"` 소트 이터레이터를 생성하고, PGWire에서 `strings.Split(upper, "FROM")`로 `valid_from` 컬럼명 등에서 조기 분리되어 테이블명 조회가 파괴되던 결함.
     - 조인 조건 AST 분해 및 PGWire 쿼리 키워드 경계 정규식 기반 정합 분해.
  18. **`BUG-TABLE-CASE-1` (`pkg/storage/memengine/ddl.go:458`, `pkg/storage/wiredtiger/wt_engine.go:745`)**:
     - `GetTable(tableName)` 및 `DropTable(tableName)`이 Go 맵의 엄격 일치(`e.tables[tableName]`)로만 작성되어, `CREATE TABLE Users` 후 `SELECT * FROM users` 또는 `INSERT INTO users` 실행 시 `table not found: users` 에러를 반환하는 치명적 대소문자 결함.
     - 1차 룩업 실패 시 `strings.EqualFold` 순회 또는 소문자 인덱싱을 통해 대소문자 무관 테이블 탐색 보장.
  19. **`BUG-WT-FK-1` (`pkg/storage/wiredtiger/wt_engine.go:1195-1380`)**:
     - WiredTiger 스토리지 백엔드의 `Insert`, `Update`, `Delete` 함수에 외래키(FK) 제약조건 검증 및 CASCADE/RESTRICT 처리 로직이 전무하여, 가짜 외래키 삽입 및 참조 부모 행 무단 삭제로 고아 데이터가 무제한 생성되던 치명적 결함.
     - WiredTiger CGO 트랜잭션 계층에 외래키 참조 무결성 검증 및 부모 행 삭제/갱신 시 연쇄 처리 연동.
  20. **`BUG-FK-CASE-1` (`pkg/storage/memengine/tx.go:940-946`)**:
     - `memengine`의 `findMatchingChildPKsUnderLock`에서 `parentRecord[pCol]` 부모 컬럼 조회가 대소문자 차이로 실패하면 "참조 자식 행이 0건"이라고 오판하여 `nil, nil`을 사일런트 반환함으로써, `ON DELETE RESTRICT` 및 CASCADE를 완전 우회하고 부모 행 삭제를 허용하던 결함.
     - `parentRecord` 룩업 시 `strings.EqualFold` 대소문자 무관 매칭 보장.
  21. **`BUG-SPLIT-VALUES-1` (`pkg/engine/sqlexec_eval.go:84-106`, `sqlexec_dml.go:405`)**:
     - `splitValues`가 괄호 중첩 깊이(`depth`)를 계산하지 않고 콤마(`,`)를 단순 분리하여, `INSERT INTO t VALUES (1, COALESCE(a, b))`와 같은 다중 인자 함수 사용 시 내부 콤마에서 쪼개져 "column count does not match value count" 에러를 내던 결함.
     - 괄호 중첩 깊이(`depth`)를 추적하여 최상위 콤마만 분해하도록 수정.
  22. **`BUG-MERGE-SET-1` (`pkg/engine/sqlexec_merge.go:646-658`)**:
     - MERGE 문의 `UPDATE SET` 파싱 시 `strings.Split(s, ",")` 단순 분해로 인해 `SET col = COALESCE(a, 'N/A'), col2 = 1` 등 함수 내 콤마 분해 시 후속 구문이 `=` 미포함으로 조용히 버려지고 문법이 깨지던 결함.
     - 괄호 중첩을 고려한 토큰 분해 적용으로 다중 인자 수식 보존.
  23. **`BUG-COPY-PK-1` (`pkg/wire/copy.go:338-342`)**:
     - PGWire `COPY FROM` 처리 시 `curPK.IVal == 0` 조건을 "PK 미지정"으로 오해하여, 사용자가 적법하게 임포트한 정수 PK `0`을 현재 Unix 에포크 타임스탬프(`time.Now().UnixNano()...`)로 덮어써 데이터를 훼손하던 결함.
     - 컬럼 누락(`!exists`) 또는 `IsNull`인 경우에만 자동 생성을 허용하고 수치 `0`은 온전히 보존.
  24. **`BUG-STR-NULL-1` (`pkg/proc/builtins_string.go:316-435`)**:
     - `UPPER`, `LOWER`, `TRIM`, `LTRIM`, `RTRIM`, `LEFT`, `RIGHT`, `SUBSTRING`, `REVERSE`에 `NULL` 전달 시 `NULL` 대신 빈 문자열(`""`)을 반환하고, `LEN(NULL)`은 `0`을 반환하며, `REPLACE('abc', NULL, 'x')`는 `"xaxbxcx"`로 문자열을 변조하는 결함.
     - 모든 문자열 함수에서 인자가 `IsNull`일 경우 `storage.NullValue(storage.TypeVarChar)`를 정규 반환하도록 표준화.
  25. **`BUG-VM-UPDATE-1` (`pkg/proc/runtime/vm_dispatcher.go:668`, `pkg/proc/runtime/vm.go:959`)**:
     - VDBE `OpUpdate` 및 `execUpdateWhere`에서 `rec[a.Column] = ...` 대입 시 쿼리 원문 컬럼명을 그대로 사용하여, 스키마 공식 컬럼명과의 대소문자 불일치 시 스토리지 병합에서 무시되어 업데이트가 사일런트 유실되던 결함.
     - `schema.FindColumn`을 통해 스키마 공식 컬럼명으로 정규화하여 `rec`에 기록.
  26. **`BUG-FK-CHILD-CASE-1` (`pkg/storage/memengine/tx.go:853-860`)**:
     - `memengine`의 `checkForeignKeyUnderLock`에서 `childRecord[cCol]` 조회가 대소문자 차이로 실패하면 "외래키가 NULL이므로 만족됨(`return true, nil`)"으로 오판하여 부모에 없는 가짜 외래키 삽입을 무단 허용하던 결함.
     - `childRecord` 룩업 시 `strings.EqualFold` 대소문자 무관 매칭 보장.
  27. **`BUG-PGWIRE-PARAM-1` (`pkg/wire/server.go:992`)**:
     - PGWire `buildPreviewSQL`에서 `strings.ReplaceAll(res, "$1", replacement)` 단순 치환으로 인해 `$10` 이상의 파라미터가 `$1` 치환 시 앞부분이 잘려 문법 에러를 유발하던 결함.
     - 단어 경계 인식 및 단일 패스 파서 기반 파라미터 치환 엔진 적용.
  28. **`BUG-APPLY-REGEX-1` (`pkg/engine/sqlexec_apply.go:214-222`)**:
     - TVF 실행 시 `buildParameterizedApplyQuery`에서 컬럼명을 `\b%s\b` 정규식으로 단순 문자열 치환하여, `id` 컬럼이 SQL 키워드나 리터럴을 훼손하거나 컬럼 치환 순서가 Go 맵 순회에 의존하는 결함.
     - 토큰 인식 파라미터 치환 및 결정론적 컬럼 순회 적용.
  29. **`BUG-STRING-SPLIT-SEP-1` (`pkg/engine/sqlexec_string_split.go:116-118`)**:
     - `STRING_SPLIT(str, sep)`에서 `if sep == "" { sep = "," }` 안티패턴으로 인해 빈 문자열 구분자 전달 시 조용히 콤마(`,`)로 대체하여 분해하는 임의 추측 결함.
     - 빈 문자열 구분자 전달 시 표준 규격 에러 반환.
  30. **`BUG-ADD-COL-NOT-NULL-1` (`pkg/storage/memengine/ddl.go:500-502`)**:
     - 기존 데이터가 존재하는 테이블에 기본값 없는 `NOT NULL` 컬럼 추가 시 기존 행을 무조건 `storage.NullValue`로 채워 NOT NULL 제약조건을 사일런트 파괴하는 결함.
     - 데이터 존재 시 기본값 없는 `NOT NULL` 컬럼 추가를 거부하거나 기본값 평가 연동.
  31. **`BUG-DML-PK-RANDOM-1` (`pkg/engine/sqlexec_dml.go:1071-1078, 1228-1235`)**:
     - `ExecuteUpdate` 및 `ExecuteDelete` 폴백에서 `rec["id"]`를 하드코딩하고 없을 때 `for _, v := range rec { pkVal = v; break }`로 무작위 컬럼을 PK로 간주하여 `tx.Update`/`tx.Delete`를 호출함으로써 엉뚱한 행을 수정/삭제하는 치명적 데이터 파괴 결함.
     - `schema.PrimaryKeyColumn()`을 통한 정규 PK 룩업 보장.
  32. **`BUG-UPDATE-SET-SPLIT-1` (`pkg/engine/sqlexec_dml.go:1045`)**:
     - `ExecuteUpdate`의 `SET` 절 분해 시 `strings.Split(setClause, ",")` 단순 분해로 인해 `CONCAT(a, b)` 등 함수 내 콤마 분해 시 후속 구문이 조용히 유실되던 결함.
     - 괄호 및 따옴표 중첩 추적 기반 최상위 콤마 토큰 분해 적용.
  33. **`BUG-HTTP-TABLE-PK-1` (`pkg/api/handlers_tables.go:147, 160-163`)**:
     - HTTP API 테이블 생성(`CreateTableHandler`) 시 PK 미지정 시 첫 번째 컬럼을 멋대로 PK로 강제 지정(`cols[0].PrimaryKey = true`)하고, 알 수 없는 타입을 조용히 `TypeVarChar`로 대체하는 안티패턴.
     - 임의 PK 승격 제거 및 미지원 타입 전달 시 400 Bad Request 에러 반환.
  34. **`BUG-STORAGE-UPDATE-CASE-1` (`pkg/storage/memengine/tx.go:502`, `pkg/storage/wiredtiger/wt_engine.go:1298`)**:
     - `memengine`과 `wiredtiger`의 `updateLocked`에서 입력 `record`의 키를 대소문자 정규화 없이 `updated`에 그대로 복사하여, 스키마 컬럼명과 대소문자가 다를 경우 구값을 그대로 유지하고 신규 값을 고아 키로 방치하여 업데이트가 사일런트 유실되는 결함.
     - 스키마 공식 컬럼명으로 정규화하여 대입.
  35. **`BUG-TUPLE-RECORD-CASE-1` (`pkg/storage/tuple.go:63, 98`)**:
     - `RecordToRow`, `RecordToRowWithArena`, `ApplyRecordToRow`에서 `rec[col.Name]` 엄격 일치 조회로 인해 대소문자가 다른 레코드 입력 시 컬럼 값을 `NullValue`로 강제 대체하거나 갱신을 누락하여 바이너리 튜플에 데이터 손실을 유발하던 결함.
     - 대소문자 무관 탐색 보장.
  36. **`BUG-INDEX-SCAN-ORDERBY-1` (`pkg/engine/sqlexec_eval.go:661, 692, 814`)**:
     - `isIndexAscendingOrder`/`isIndexDescendingOrder`가 `ORDER BY`의 첫 번째 컬럼만 비교하고 다중 정렬(`ORDER BY a ASC, b DESC`) 전체가 만족된 것으로 오판하여 인덱스 스캔 후 정렬을 건너뛰고 잘못된 순서로 결과를 반환하던 결함.
     - `ORDER BY`의 모든 컬럼이 인덱스 순서 및 방향과 일치하는지 전수 검증.
  37. **`BUG-COPY-DELIM-1` (`pkg/wire/copy.go:136-145`)**:
     - PGWire `COPY FROM`의 `DELIMITER` 파싱 시 `rest[dIdx+10:]`을 단순 슬라이스하여 `WITH (FORMAT CSV, DELIMITER '|', HEADER)`와 같이 후속 옵션이 올 때 `dSub[0]`이 닫는 따옴표(`'`)를 구분자로 파싱하여 CSV 전체가 깨지던 결함.
     - 토큰 단위 파싱 및 따옴표 제거 정합화.
  38. **`BUG-PGWIRE-DESCRIBE-1` (`pkg/wire/server.go:1064, 1099, 1586`)**:
     - PGWire `writeSchemaDescription` 및 `inferColumnOIDs`에서 쿼리를 `"FROM"`과 `","`로 단순 분해하여 서브쿼리, 콤마 포함 함수(`COALESCE`, `SUBSTRING`), 리터럴이 테이블/컬럼 추출을 파괴하고 미매칭 수식을 OID 25(VARCHAR)로 왜곡 전달하는 결함.
     - 안전한 쿼리 파싱 및 표현식 타입 추론 연동.
  39. **`BUG-VM-SUBQ-NIL-1` (`pkg/proc/parser_expr.go:321`, `pkg/proc/compiler.go:1358`)**:
     - `(SELECT 1)` 등 `FROM` 절 없는 서브쿼리에서 `sel.From.Name`을 nil 검사 없이 역참조하여 런타임 패닉을 유발하고, `(SELECT a, b)`와 같은 다중 컬럼 서브쿼리를 에러 없이 첫 번째 컬럼만 남기고 침묵 버리는 결함.
     - nil 가드 및 다중 컬럼 스칼라 서브쿼리 문법 에러 반환.
  40. **`BUG-CATALOG-ORDERBY-1` (`pkg/catalog/virtual/result.go:34`, `pkg/engine/sqlexec_dbcc.go:310`)**:
     - `sys.tables` 등 가상 뷰 및 DBCC 명령에서 `strings.Fields(orderByClause)`로 정렬 컬럼을 파싱하여 다중 컬럼 정렬 시 `orderCol`에 콤마가 포함(`"col1,"`)되어 정렬이 완전히 무력화되던 결함.
     - 콤마 분해 및 개별 컬럼 트림 적용.
  41. **`BUG-WT-INDEX-DESC-1` (`pkg/storage/wiredtiger/wt_engine.go:1532-1534`)**:
     - `WTTx.IndexScanDesc`가 커서를 역방향으로 순회하지 않고 정방향 `tx.IndexScan`을 그대로 반환하는 스텁으로 작성되어 있어, WiredTiger에서 `ORDER BY col DESC` 인덱스 쿼리 시 사후 정렬이 생략되어 오름차순(ASC)으로 반대 결과를 반환하던 결함.
     - WiredTiger 커서 역방향 순회 연동 또는 슬라이스 반전 적용.
  42. **`BUG-CTE-MATERIALIZE-1` (`pkg/engine/sqlexec_explain.go:862-868`)**:
     - CTE 구체화 임시 테이블(`#__cte_*`) 생성 시 `if i == 0 { pk = " PRIMARY KEY" }`로 첫 번째 컬럼을 멋대로 PK로 선언하여 중복 값이 있을 때 `duplicate key violation` 에러로 실패하고, 모든 컬럼을 `VARCHAR(255)`로 선언해 데이터 타입을 파괴하던 결함.
     - 임의 PK 지정 제거 및 원본 쿼리 데이터 타입 메타데이터 보존.
  43. **`BUG-TDS-ROW-CASE-1` (`pkg/wire/tds/token.go:774, 912, 923`)**:
     - TDS의 `WriteRowDirect`와 `WriteNBCRowDirect`가 `row[col.Name]` 엄격 일치 조회만 수행하여, 행 맵의 키와 컬럼명의 대소문자가 다를 때 `val`이 `nil`이 되어 실제 데이터가 존재함에도 MSSQL 클라이언트에 NULL을 전송하던 결함.
     - `strings.EqualFold` 대소문자 무관 행 값 매칭 적용.
  44. **`BUG-CATALOG-IDENT-1` (`pkg/catalog/virtual/result.go:372`)**:
     - 가상 카탈로그 `cleanIdentifier`에서 `strings.Index(s, ".")`를 사용하여 첫 번째 점을 기준으로 슬라이스함으로써, `[master].[sys].[tables]`와 같은 3-part/4-part 쿼리 시 `sys].[tables`라는 깨진 문자열이 남아 카탈로그 조회가 깨지던 결함.
     - 점 단위 분할 후 최종 식별자 정규 트림 적용.
  45. **`BUG-PROJ-CAST-AS-1` (`pkg/execution/project.go:82`, `pkg/wire/server.go:1106`)**:
     - `ProjectionIterator`에서 `" AS "` 문자열을 괄호 중첩 검사 없이 단순 검색하여 `CAST(expr AS type)` 수식 내부의 `" AS "`를 별칭 구분자로 오인해 수식을 두 동강 내고(`"CAST(price"`, `"INT)"`) 결과 행에 NULL을 반환하던 결함.
     - 괄호 중첩 깊이 인식 기반 최상위 `AS` 별칭 분해 적용.
  46. **`BUG-DECORRELATE-TRIM-1` (`pkg/planner/decorrelate.go:501-508, 473`)**:
     - `removeClause`에서 `strings.TrimSuffix(before, "and")` 단순 슬라이스로 인해 `country = 'Poland'` 등 `"and"`로 끝나는 리터럴의 뒷부분을 잘라(`'Pol'`) 문법 에러 및 조건 훼손을 유발하고, `findKeywordIndex`의 따옴표 미추적으로 리터럴 내 키워드를 오인하던 결함.
     - 토큰 경계 검사 및 따옴표 상태 추적 적용.
  47. **`BUG-INDEX-KEY-CASE-1` (`pkg/storage/memengine/ddl.go:172, 211, 222`)**:
     - `extractIndexKey`에서 `rec[c]` 맵 조회가 대소문자 차이로 실패할 경우 키를 `TypeUnknown` NULL 또는 `"NULL"`로 기록하여 세컨더리 인덱스를 영구 오염시키고 차후 인덱스 조회를 0건으로 만드는 결함.
     - `strings.EqualFold` 대소문자 무관 레코드 컬럼 탐색 보장.
  48. **`BUG-FK-PARENT-TABLE-CASE-1` (`pkg/storage/memengine/tx.go:932`)**:
     - `tx.engine.tables[fk.ParentTable]` 맵 조회가 대소문자 불일치로 실패할 경우 부모 컬럼 목록이 비어 `rowMatchesFK`가 모든 자식에 대해 false를 반환함으로써 부모 삭제를 무단 허용하던 결함.
     - 대소문자 무관 테이블 탐색으로 정규 부모 스키마 조회 보장.
  49. **`BUG-PLANNER-ORDERBY-1` (`pkg/planner/planner.go:1327`, `pkg/planner/setop.go:326`)**:
     - CBO 플래너 및 SetOp 플래너가 `strings.Fields(OrderByClause)`로 정렬 키를 파싱하여 다중 컬럼 정렬 시 `SortKey`에 콤마가 포함(`"a,"`)되고 후속 컬럼이 버려져 실행 계획 텍스트 및 정렬 플랜 노드가 왜곡되던 결함.
     - 콤마 분해 기반 다중 컬럼 정렬 키 정합화.
  50. **`BUG-WT-SWEEP-1` (`pkg/storage/wiredtiger/wt_engine.go:265`, `pkg/engine/session_pool.go:184`)**:
     - `startPeriodicSync` 고아 세션 상시 점유 및 유휴 세션 미회수로 인해 기동 60분 후 WiredTiger `sweep-server`로부터 `Session did not run a sweep for 60 minutes` 경고가 발생하는 결함.
     - `WT_SESSION::reset` CGO 바인딩 및 주기적 세션 리셋, `SessionPool` 유휴 세션 TTL 리퍼 도입으로 60분 와치독 방지.
  51. **`BUG-API-PK-GEN-1` (`pkg/api/handlers_records.go:449-455`)**:
     - `CreateRecord`에서 정수 PK 미제공 시 나노초가 아닌 밀리초(`UnixNano() / int64(time.Millisecond)`)로 채번하여 동일 밀리초 내 동시 요청 시 PK 충돌 에러가 발생하고, VarChar/Int 외 타입(예: UUID) 미처리로 `storage.NullValue`가 PK로 강제 삽입되는 결함.
     - 나노초 타임스탬프 또는 원자적 카운터 결합 유니크 채번 및 UUID PK 자동 생성(`uuid.New().String()`) 보장.
  52. **`BUG-WAL-TORN-BOOT-1` (`pkg/storage/memengine/wal.go:349`, `pkg/storage/memengine/memengine.go:311`)**:
     - 시스템 비정상 종료(Crash, 정전)로 인해 WAL 파일의 마지막 레코드가 중간에 잘린(Torn Write) 경우, `ReplayWAL`이 치명적 에러(`truncated WAL frame payload`)를 반환하여 `OpenWithOptions` 엔진 기동 전체를 영구 중단시키는 결함.
     - 마지막 손상된 미완료 레코드 발견 시 이전 정상 커밋 지점까지 복구 완료 후 트렁케이트하고 정상 기동하도록 무결성 보장 (Golden Rule 1, 8).
  53. **`BUG-CATALOG-INTERCEPT-BRACKET-1` (`pkg/catalog/virtual/interceptor.go:113-160`)**:
     - `IsVirtualCatalogJoinQuery`에서 대괄호(`[`와 `]`)를 제거하지 않고 `"SYS.TABLES"`, `"SYS.COLUMNS"` 등의 문자열을 매칭하여, SSMS/sqlcmd 표준 쿼리(`FROM [sys].[tables] t JOIN [sys].[columns] c`) 인입 시 가상 카탈로그 조인 인터셉터가 동작하지 않고 물리 테이블 조회로 떨어져 `table not found` 에러가 발생하는 결함.
     - 정규화 단계에서 대괄호(`[` 및 `]`) 및 스키마 접두사를 정규 트림하여 가상 카탈로그 조인 쿼리 100% 정상 인터셉트 보장.
  54. **`BUG-SELECT-AGG-CASE-1` (`pkg/engine/sqlexec_select.go:2020-2224`)**:
     - `computeAggregates`에서 `GroupByCols` 및 집계함수(`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, 비집계 프로젝션)가 행 맵을 `r[gClean]`, `r[bare]`, `r[item.ArgCol]` 엄격 대소문자로 조회하여, 쿼리 컬럼명의 대소문자가 다를 경우 모든 행이 `<nil>` 단일 버킷으로 합쳐지거나 집계 결과가 `NULL` 또는 0으로 왜곡 반환되는 결함.
     - 행 맵 조회 시 `strings.EqualFold` 대소문자 무관 탐색 보장.
  55. **`BUG-SP-PARAM-DECIMAL-1` (`pkg/engine/sqlexec_procs.go:1285-1300`)**:
     - `parseSpParamDeclarations`에서 `strings.Split(declStr, ",")` 단순 분해로 인해 `@param DECIMAL(10, 2)` 등 다중 인자 데이터 타입 내부의 콤마에서 쪼개져 타입이 `DECIMAL(10`으로 절단되고 이름이 `"2)"`인 유령 파라미터가 생성되는 결함.
     - 괄호 중첩 추적 기반 콤마 분해 엔진 적용으로 복합 타입 매개변수 선언문 보존.
  56. **`BUG-SP-EXEC-SUFFIX-OUT-1` (`pkg/engine/sqlexec_procs.go:1204-1209`)**:
     - `cleanOutputSuffix`에서 공백/단어 경계 검사 없이 `strings.HasSuffix(upper, "OUT")`를 수행하여, `@timeout`, `@layout`, `@logout`, `@callout` 등 `OUT`으로 끝나는 정상 변수명의 뒷부분 3글자를 강제 절단(`@time`, `@lay`)하여 변수 바인딩이 실패하는 결함.
     - 선행 공백 또는 단어 경계가 존재하는 독립 키워드 `OUTPUT`/`OUT`만 제거하도록 수정.
  57. **`BUG-MEM-INDEX-LOCAL-ORDER-1` (`pkg/storage/memengine/tx.go:1264-1277`)**:
     - MemEngine `IndexScan` 시 현재 쓰기 트랜잭션에서 삽입된 로컬 행(`tx.local[tableName]`)을 Go 맵 무작위 순회(`for pk, locRow := range localRows`)로 스캔 결과 꼬리에 무조건 `append`하여, 인덱스 키 정렬 순서를 파괴하고 비결정론적 행 순서를 반환하는 치명적 결함.
     - 트랜잭션 로컬 행에 대해 인덱스 키 추출 후 메인 인덱스 스캔 결과와 병합 정렬(Merge-Sort)하여 인덱스 순서 무결성 보장.
  58. **`BUG-STORAGE-CMP-INT-FLOAT-1` (`pkg/storage/engine.go:734-755`)**:
     - `storage.Value.Compare`에서 `TypeInt`와 `TypeFloat` 간의 상호 타입 변환 로직이 누락되어, Int-Float 비교 시 미초기화된 0값(`other.IVal` 또는 `other.FVal`)과 직접 비교함으로써 `IntValue(100) > FloatValue(200.5)`가 성립되는 등 비교 연산 및 인덱스 범위 검색이 완전히 역전/왜곡되는 결함.
     - `Int <-> Float` 상호 비교 시 `float64(v.IVal)` 강제 변환을 선행하여 정확한 수치 비교 보장.
  59. **`BUG-DERIVED-JOIN-STAR-1` (`pkg/engine/sqlexec_derived.go:210-217`)**:
     - 파생 테이블이 합성 PK(`_dt_row_id`)를 필요로 할 때, `SELECT *`가 사용된 JOIN 쿼리(`SELECT * FROM orders o JOIN (SELECT ...) d`)에서 `reSelectStarFrom.ReplaceAllString`으로 `SELECT * FROM`을 `SELECT d.col1, d.col2 FROM`으로 덮어써 타 조인 테이블(`orders o`)의 모든 프로젝션 컬럼을 완전히 증발시키는 결함.
     - 쿼리 내 타 조인 테이블의 존재 여부를 감지하고 원본 테이블 프로젝션(`o.*`)을 보존하도록 정규화.
  60. **`BUG-DDL-SPLIT-QUOTE-1` (`pkg/engine/sqlexec_ddl.go:737-767`)**:
     - `splitColumnDefs`가 괄호 중첩만 추적하고 문자열 따옴표를 추적하지 않아, `DEFAULT 'hello, world'`와 같이 문자열 내 콤마 분해 시 후속 구문을 새 컬럼으로 오인하거나 `DEFAULT '('`와 같은 괄호로 인해 `depth`가 영구히 >0으로 남아 이후 모든 컬럼이 1개로 뭉개지는 결함.
     - 따옴표 상태 추적기를 결합하여 문자열 리터럴 내부의 콤마 및 괄호 무시 보장.
  61. **`BUG-ARITH-EXPR-ZERO-1` (`pkg/engine/sqlexec_eval.go:19, 920-966`)**:
     - `evalUpdateExpr`의 정규식이 `col <op> num` 형태만 엄격하게 매칭하여, 대괄호 컬럼(`[Price] + 10`), 다중 컬럼 산술(`subtotal + tax`), 상수 선행(`10 + price`) 수식이 들어왔을 때 매칭에 실패하고 `parseLiteralValue`로 떨어져 대상 컬럼 값을 조용히 `0`으로 리셋하는 결함.
     - 대괄호 식별자, 다중 컬럼 참조 및 상수 선행 연산식을 포괄하는 유연한 수식 평가기 도입.
  62. **`BUG-DROP-INDEX-CASE-1` (`pkg/storage/memengine/ddl.go:148-154`)**:
     - `DropIndex`에서 인덱스 스키마 탐색은 `FindIndex`로 대소문자 무관하게 수행한 뒤, 인덱스 맵 삭제 시 인자로 전달된 원문 대소문자(`delete(td.Indexes, indexName)`)를 사용하여 대소문자가 다를 경우 실제 `td.Indexes`에서 삭제되지 않고 좀비 인덱스로 영구 잔존하는 결함.
     - 정규화된 공식 인덱스 명칭(`foundIdx.Name`)을 키로 `delete`하여 완벽한 인덱스 정리 보장.
  63. **`BUG-AGG-MIXED-NUM-DEC-1` (`pkg/execution/aggregate.go:408-428, 573-594`)**:
     - Volcano `AggregateIterator`에서 단일 그룹 내에 `Decimal`과 일반 숫자(`Int`, `Float`)가 혼합되어 있을 때, 비-Decimal 값들을 `acc.sums`에 누적해두고 최종 결과 방출 시 `acc.isDecimal` 플래그로 인해 `acc.decSums`만 단독 반환함으로써 비-Decimal 수치 합계를 전량 사일런트 유실시키는 결함.
     - 그룹 내 Decimal 출현 시 기존 및 후속 비-Decimal 수치를 `Decimal`로 변환하여 누적 합산함으로써 수치 누락 방지.
  64. **`BUG-PROC-SIMPLE-CASE-1` (`pkg/proc/parser_expr.go:445-478`, `pkg/proc/ast.go:486`, `pkg/proc/compiler.go:1276-1315`)**:
     - LionSQL 프로시저 엔진이 Searched CASE(`CASE WHEN cond THEN ...`)만 지원하고 단순 CASE(`CASE @val WHEN 1 THEN ...`) 구문 파싱 및 컴파일을 지원하지 않아, 피연산자(Operand)가 뒤따르는 T-SQL 표준 CASE 문 사용 시 nil 파싱 실패 및 컴파일 불가 구문 에러를 유발하는 결함.
     - `CaseExpr`에 `Operand Expr` 필드를 신설하고, 파서 및 VDBE 컴파일러에서 피연산자-조건값 간의 동등 비교 분기 바이트코드 방출 보장.
  65. **`BUG-EVAL-NEQ-NULL-1` (`pkg/proc/eval.go:341-347`)**:
     - `evalRowBinaryOp`에서 불일치 비교(`TokenNeq`: `<>`, `!=`) 시 피연산자 중 하나가 `nil`일 때 `true`를 반환하도록 작성되어(`if left == nil || right == nil { return true }`), SQL 3가 논리(3VL)를 위반하고 `WHERE status <> 'completed'` 조건 등에서 NULL 행들이 필터를 무단 통과하는 결함.
     - `TokenNeq` 역시 다른 비교 연산자와 동일하게 NULL 피연산자 시 `nil`(UNKNOWN)을 반환하여 WHERE 절 누수 원천 차단.
  66. **`BUG-DATEADD-NULL-1` (`pkg/proc/builtins_date.go:567-584, 635-645`)**:
     - `DATEADD` 및 `EOMONTH`에서 증분 파라미터(`n`, `monthsToAdd`)의 NULL 여부를 검사하지 않아, `DATEADD(day, NULL, '2024-01-01')`이나 `EOMONTH(date, NULL)` 호출 시 `n=0`으로 오판되어 NULL 대신 원본 날짜를 그대로 반환하는 결함.
     - 인자 목록 중 하나라도 NULL일 경우 표준 규격에 따라 `storage.NullValue`를 즉시 정규 반환하도록 수정.
  67. **`BUG-VM-INSERT-DEFAULT-1` (`pkg/proc/runtime/vm_dispatcher.go:611-626`)**:
     - VDBE `OpInsert`에서 기본값(`DEFAULT`) 평가 시 6종의 하드코딩된 날짜/UUID 함수(`NEWID()`, `GETDATE()` 등)만 switch로 검사하고, `DEFAULT 0`, `DEFAULT 'active'`, `DEFAULT 1.0` 등 상수 리터럴 기본값을 전면 무시하여 생략된 컬럼에 `NULL`이 강제 삽입되던 결함.
     - 상수 리터럴 기본값 표현식 및 수식 평가 파이프라인을 연동하여 스키마 기본값 100% 정상 주입 보장.
  68. **`BUG-VM-DML-COL-CASE-1` (`pkg/proc/runtime/vm_dispatcher.go:598-608, 665-671`, `pkg/storage/tuple.go:63`, `pkg/storage/memengine/tx.go:502-508`)**:
     - VDBE `OpInsert` 및 `OpUpdate`에서 AST 컬럼명을 그대로 `rec[col] = stVal`, `rec[a.Column] = stVal`로 맵에 대입하고, `RecordToRow` 및 `updateLocked`에서는 스키마 컬럼명(`col.Name`)으로 엄격 대소문자 조회를 수행하여, 쿼리 컬럼 대소문자가 스키마와 다를 경우(`INSERT INTO t (Name) ...` vs 스키마 `name`) 값이 유실되거나 NULL이 삽입/업데이트되던 결함.
     - DML Record 생성 및 스토리지 레코드 변환 시 스키마 공식 컬럼명으로 정규화 또는 대소문자 무관 탐색 보장.
  69. **`BUG-DML-PK-RANDOM-GUESS-1` (`pkg/proc/runtime/vm.go:940-945, 1076-1081`, `pkg/engine/sqlexec_dml.go:1071-1078, 1228-1235`, `pkg/engine/sqlexec_ddl.go:349, 363-366`)**:
     - VDBE(`execUpdateWhere`, `execDeleteWhere`), 코디네이터(`execUpdate`, `execDelete`), DDL(`TRUNCATE TABLE`)에서 대상 행의 PK를 식별하지 못할 때 `row.Values[0]`으로 임의 추측하거나 Go 맵의 `for _, v := range rec { pkVal = v; break }` 무작위 순회로 첫 번째 컬럼 값을 PK로 임의 간주하여 무관한 엉뚱한 행을 갱신/삭제/트렁케이트하던 치명적 무결성 결함.
     - 임의 컬럼 추측 및 무작위 Go 맵 순회를 완전 삭제하고, 스키마 공식 기본키(`schema.PrimaryKeyColumn()`)를 엄격 조회하며 미지정 시 에러를 반환하도록 정규화.
  70. **`BUG-AUTH-ADMIN-PW-BYPASS-1` (`pkg/wire/server.go:561-565`, `pkg/wire/tds/server.go:702-706`)**:
     - 글로벌 인증 플래그가 비활성화된 상태에서 PGWire 및 TDS 리스너가 `authSvc.GetLogin(user)`로 등록된 계정 비밀번호 해시가 존재함에도 `if !auth.IsBuiltinAdminLogin(user) { needsPassword = true }`를 두어, `sa`, `postgres`, `admin` 등 슈퍼유저 계정에 명시적 비밀번호가 설정되어 있어도 패스워드 검증 없이 무조건 무암호 접속을 허용하던 보안 백도어 결함.
     - 관리자가 슈퍼유저 비밀번호를 설정(`login.PasswordHash != ""`)한 경우 내장 관리자 계정이라도 반드시 비밀번호 인증을 거치도록 보안 가드레일 수정.
  71. **`BUG-UPDATE-SET-COMMA-SPLIT-1` (`pkg/engine/sqlexec_dml.go:1045-1052`)**:
     - 코디네이터 `execUpdate`에서 SET 절을 `strings.Split(setClause, ",")`로 단순 콤마 분해하여, `SET note = 'hello, world'`와 같은 문자열 리터럴 내부 콤마나 `SET full_name = CONCAT(first, ', ', last)` 등 함수 인자 내부 콤마까지 절단되어 수식이 파괴되고 컬럼 대입이 유실되던 결함.
     - 따옴표 및 괄호 중첩 상태를 추적하는 정밀 SQL 절 토큰 분해기를 적용하여 콤마 오절단 방지.
  72. **`BUG-EVAL-IN-COMMA-SPLIT-1` (`pkg/engine/sqlexec_eval.go:360`)**:
     - 조건 평가기 `evaluateWhereCondition`의 `IN (...)` 절에서 `strings.Split(valList, ",")`로 리스트를 단순 분해하여, `WHERE city IN ('Seoul, KR', 'Tokyo, JP')`와 같이 콤마를 포함한 문자열 리터럴이 `'Seoul`, `KR'`로 쪼개져 IN 조건 매칭이 영구 실패하던 결함.
     - 문자열 리터럴 이스케이프 및 따옴표 상태를 인식하는 스마트 IN 리스트 분해기 도입으로 정합성 보장.
  73. **`BUG-STORAGE-CMP-DATETIME-STR-1` (`pkg/storage/engine.go:466, 742-772`)**:
     - `DateTimeValue`가 `TVal`만 설정하고 `SVal`을 빈 문자열로 남겨둠에 따라, `TypeVarChar`와 `TypeDateTime` 상호 비교 시 `Value.Compare`가 미초기화된 0값 시간(`0001-01-01`) 또는 빈 문자열과 비교하여 비교 결과가 역전되고, `WHERE date_col >= '2024-01-01'` 조건 등의 조회가 0건으로 왜곡되던 결함.
     - `Value.Compare`에 DateTime/Date와 문자열 간의 상호 파싱/강제변환(Coercion)을 보강하여 올바른 시간 축 비교 보장.
  74. **`BUG-STORAGE-EQUAL-FLOAT-STR-1` (`pkg/storage/engine.go:666`)**:
     - `Value.Equal`에서 실수(`TypeFloat`) 동등 비교 시 `fmt.Sprintf("%f", v.FVal)`을 호출하여 소수점 6자리까지만 포맷팅 비교함으로써, 7자리 이후가 다른 서로 다른 두 실수가 참으로 판정되고, 핫패스 비교마다 힙 할당을 유발하며, `Compare`($\ne 0$)와 `Equal`($== true$) 간의 불일치가 발생하는 결함.
     - 문자열 포맷팅을 제거하고 네이티브 부동소수점 비트 또는 엡실론 정밀도 비교로 단일화.
  75. **`BUG-MEM-TX-TABLE-CASE-1` (`pkg/storage/memengine/tx.go:244, 1479, 1546, 1622`)**:
     - MemEngine 트랜잭션의 `getTableLocked`, OCC 충돌 검증, 커밋 시 샤드 반영 루프가 `tx.engine.tables[ch.table]` 엄격 대소문자 조회로 작성되어, 등록 시와 다른 대소문자(`Users` vs `users`)로 테이블을 조작한 트랜잭션이 커밋할 때 `if !exists { continue }`로 통과되어 쓰기 데이터가 메모리에 반영되지 않고 사일런트 유실되던 결함.
     - 변경 레코드(`rowChange`)의 테이블명을 스키마 공식 명칭으로 정규화하고 대소문자 무관 탐색 보장.
  76. **`BUG-MEM-TX-INDEX-CASE-1` (`pkg/storage/memengine/tx.go:1229`)**:
     - MemEngine `IndexScan`에서 `td.Indexes[indexName]`으로 엄격 대소문자 조회를 수행하여, 쿼리 또는 플래너가 대소문자가 다른 인덱스명(예: `idx_name` vs `IDX_NAME`)으로 요청할 경우 `storage.ErrIndexNotFound`를 반환하고 쿼리를 실패시키거나 풀스캔으로 강제 후퇴하던 결함.
     - 인덱스 맵 탐색 시 1차 실패 시 대소문자 무관 순회 탐색을 보강하여 인덱스 히트 보장.
  77. **`BUG-MERGE-IDENTITY-PK-1` (`pkg/engine/sqlexec_merge.go:180-205`)**:
     - `MERGE` 문의 `WHEN NOT MATCHED THEN INSERT` 절에서 대상 테이블의 `IDENTITY` 컬럼 자동 생성 처리가 완전히 누락되어, IDENTITY PK 테이블에 MERGE 수행 시 `missing primary key` 에러로 연산이 실패하고, 커밋 후 `@@identity`가 갱신되지 않던 결함.
     - MERGE INSERT 시 IDENTITY 컬럼 자동 채번 및 세션/테이블 식별자 동기화 파이프라인 연동.
  78. **`BUG-API-CREATE-DEFAULT-1` (`pkg/api/handlers_records.go:437-462`)**:
     - HTTP API `POST /api/records/:table` 처리 시 `col.Default`를 검사하지 않아, `NOT NULL DEFAULT ...` 컬럼이 요청 페이로드에서 생략되면 400 에러(`field cannot be null`)로 거부되고, 기본값이 있는 nullable 컬럼은 기본값 대신 `NULL`이 삽입되던 결함.
     - 누락된 컬럼에 대해 스키마 기본값(리터럴 및 함수 기본값)을 자동 주입하도록 보강.
  79. **`BUG-CATALOG-MSSQL-REGEX-UNICODE-1` (`pkg/catalog/virtual/mssql.go:17-18`)**:
     - 가상 카탈로그의 `reMssqlTableNameEq` 및 `reMssqlSchemaNameEq` 정규식이 일반 문자열(`='...'`)만 기대하여, SSMS/sqlcmd/DBeaver 등 표준 클라이언트가 전송하는 유니코드 리터럴(`WHERE t.name = N'users'`) 및 대괄호 식별자(`WHERE [t].[name] = ...`) 매칭에 실패, 전체 테이블의 메타데이터를 불필요하게 덤프 반환하던 결함.
     - `N'...'` 접두사 및 대괄호 식별자를 허용하는 견고한 카탈로그 정규식으로 고도화.
  80. **`BUG-WIRE-COPY-TX-BYPASS-1` (`pkg/wire/copy.go:183, 302-358`)**:
     - `handleCopyIn`이 호출 인자로 전달된 세션 식별자(`sessID`)를 무시하고 독자적인 새 세션을 열어 트랜잭션을 즉시 커밋(`tx.Commit()`)함으로써, 클라이언트가 명시적 트랜잭션 블록(`BEGIN` ... `ROLLBACK`) 내에서 `COPY FROM STDIN`을 실행하더라도 롤백되지 않고 강제 영속화되는 트랜잭션 원자성 파괴 결함.
     - 활성 세션 컨텍스트 및 트랜잭션 핸들(`cs.Tx`)을 바인딩하여 트랜잭션 범위 내 원자적 COPY 실행 및 롤백 보장.
  81. **`BUG-WIRE-COPY-SILENT-CORRUPT-1` (`pkg/wire/copy.go:518-545`)**:
     - `parseFieldVal`이 파싱 에러를 조용히 삼키고 `IntValue(0)`, `FloatValue(0)`, `DateTimeValue(time.Now())` 등 임의의 기본값으로 치환하며, `Decimal`/`UUID`/`Date` 등 기타 데이터 타입을 전부 `StringValue`로 떨어뜨려, `COPY FROM STDIN` 시 잘못된 입력 데이터가 에러 없이 0이나 현재 시각으로 무단 변조되어 저장되는 치명적 데이터 오염 결함.
     - 파싱 실패 시 명시적 SQL 구문 에러를 클라이언트에 반환하고 전체 데이터 타입 정밀 파싱 지원.
  82. **`BUG-WIRE-COPY-OUT-UNESCAPED-1` (`pkg/wire/copy.go:488-497`)**:
     - `handleCopyOut`이 텍스트 포맷 필드를 `strings.Join(fields, delimiter)`로 단순 결합하면서 필드 내 탭(`\t`), 줄바꿈(`\n`, `\r`), 백슬래시(`\\`)를 전혀 이스케이프하지 않아, 탭이나 줄바꿈이 포함된 문자열 데이터 내보내기 시 행/열 경계가 무너져 파일이 손상되고 이후 재가져오기 시 컬럼이 밀리는 결함.
     - 표준 PostgreSQL 텍스트 포맷 이스케이프 규칙(`\t`, `\n`, `\r`, `\\`)을 적용하여 데이터 덤프 정합성 보장.
  83. **`BUG-EXEC-JOIN-STR-CASE-1` (`pkg/execution/join.go:184, 291`, `pkg/execution/merge_join.go:297`)**:
     - `HashJoinIterator`와 `MergeJoinIterator`가 문자열 조인 키 비교 시 Go 맵의 엄격 일치(`hashTable[kStr]`) 및 바이트 단위 `strings.Compare`를 사용하여, 대소문자가 다른 동일 의미의 문자열 키(예: `'admin'` vs `'ADMIN'`)가 일치하지 않는 것으로 판정되어 유효한 조인 결과 행들이 대거 누락되는 결함.
     - 조인 키 해싱 및 비교 시 대소문자 무관 정규화 키를 적용하여 SQL 표준 조인 매칭 보장.
  84. **`BUG-PIVOT-COUNT-NULL-1` (`pkg/engine/sqlexec_pivot.go:292-357`)**:
     - `parsePivotQuery`에서 `len(vals) > 0`일 때만 집계 연산을 수행하고 빈 그룹은 `aggResult = nil`로 방치하여, 매칭 건수가 0건인 카테고리에 대해 표준 SQL 규격인 `0` 대신 `NULL`이 반환되는 집계 왜곡 결함.
     - `COUNT` 집계에 대해 빈 그룹 시 `0`을 정규 방출하도록 보강.
  85. **`BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1` (`pkg/engine/sqlexec_pivot.go:1071-1084`)**:
     - `execForJSONSelect`가 컬럼명에 점(`.`)이 포함되어 있으면 무조건 중첩 JSON 객체 경로로 해석하여, `SELECT u.id, u.name FROM users u FOR JSON PATH`와 같이 테이블 별칭이 붙은 일반 쿼리에서 불필요하게 `{"u": {"id": 1, ...}}` 형태로 래핑되어 출력되는 결함.
     - 테이블 별칭과 명시적 컬럼 별칭(`AS [path.prop]`)을 구분하여 순수 컬럼 프로젝션 시 불필요한 래핑 객체 생성 방지.
  86. **`BUG-DDL-COMPOSITE-PK-PARSE-1` (`pkg/engine/sqlexec_ddl.go:89-98`)**:
     - `CREATE TABLE` 시 테이블 레벨 복합 기본키(`PRIMARY KEY (col1, col2)`) 구문을 파싱할 때 괄호 내부를 콤마로 분해하지 않고 `"col1, col2"` 전체를 단일 컬럼명으로 취급하여 어떤 컬럼도 PK로 매칭되지 못하고, 결국 첫 번째 컬럼(`Columns[0]`)만 임의 PK로 지정되어 복합 PK 테이블 스키마가 파괴되던 결함.
     - 괄호 내부 콤마 분해 루프를 적용하여 선언된 모든 복합 PK 컬럼에 `PrimaryKey = true` 플래그 정상 부여.
  87. **`BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1` (`pkg/auth/rbac.go:371-432`)**:
     - `HasTablePermission`이 하드코딩된 내장 역할(`db_owner`, `db_datareader`, `db_datawriter`), 사용자 개별 권한(`normUser`), 및 `public` 권한만 검사하고, 사용자가 부여받은 사용자 정의 데이터베이스 롤(`u.Roles`)을 전혀 검사하지 않아 커스텀 롤에 부여된 모든 테이블 권한(`GRANT ... TO role_name`)이 해당 롤 소속 사용자에게 무효화되던 치명적 인가 결함.
     - `u.Roles`의 모든 커스텀 역할에 대해 `checkSubject(r)`를 순회 검사하여 역할 기반 테이블 권한 정상 부여.
  88. **`BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1` (`pkg/planner/decorrelate.go:429-434`, `pkg/planner/pushdown.go:172`)**:
     - `splitAndConditions`가 연속 작은따옴표 이스케이프(`''`)나 대괄호 식별자(`[col AND val]`)를 인식하지 못하고, 따옴표를 단순 토글 처리하여 문자열 리터럴 내부나 식별자 내부에 `AND`가 포함된 경우 조건을 경계 밖으로 무단 절단하여 WHERE 절 문법을 파괴하던 플래너 결함.
     - `''` 이스케이프 연속 토큰 건너뛰기 및 대괄호(`[...]`) 깊이 추적을 보강하여 조건 절단 방어.
  89. **`BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1` (`pkg/storage/memengine/skiplist.go:86-90`)**:
     - `SkipList.Insert`에서 유니크 인덱스의 기존 키 갱신 시 `for _, item := range curr.Items` 값 복사 순회를 수행하며 `item.IncludeValues = inc`를 대입하여, 실제 슬라이스 원소(`curr.Items[i]`)가 전혀 갱신되지 못하고 커버링 인덱스 데이터가 유실/과거값으로 방치되던 결함.
     - 비유니크 분기와 동일하게 인덱스 인덱싱(`curr.Items[i].IncludeValues = inc`)으로 수정하여 커버링 인덱스 갱신 보장.
  90. **`BUG-MEM-SHARD-INIT-RACE-1` (`pkg/storage/memengine/table_data.go:87-116, 120`)**:
     - `TableData.ensureShardsInit()`이 뮤텍스 락 없이 `td.initShards()`를 호출하여, 기동 후나 역직렬화 직후 복수의 동시 읽기 트랜잭션이 `getRowAndVer` 또는 `hasKey`를 호출할 때 `td.shards[sIdx].rows` 맵에 동시 쓰기를 발생시켜 `fatal error: concurrent map writes` 런타임 크래시를 유발하던 결함.
     - `sync.Once`를 적용하여 샤드 지연 초기화의 원자적/멱등적 단 1회 실행 보장.
  91. **`BUG-DATE-TODATETIMEOFFSET-INT-MINS-1` (`pkg/proc/builtins_date.go:441-458, 733`)**:
     - T-SQL 표준 규격에서 `TODATETIMEOFFSET(dt, tz)`의 두 번째 인자가 정수일 경우 '분(Minute)' 단위 오프셋(예: `60` = 1시간 = `+01:00`, `-120` = -2시간)을 의미함에도 불구하고, `parseOffsetLocation`이 콜론이 없는 경우 이를 시간(`hours`)으로 취급하여 `60`을 `+60:00`(60시간)으로 60배 왜곡 변환하던 결함.
     - 인자가 정수형 문자열(콜론 미포함)일 경우 분 단위 오프셋으로 정규 변환하도록 보정.
  92. **`BUG-CATALOG-FK-MULTI-COL-ACTIONS-1` (`pkg/catalog/virtual/mssql.go:369-375`)**:
     - `execDbGateMSSQLForeignKeySelect`가 복합 외래키 컬럼들을 단일 행에 콤마(`strings.Join(..., ", ")`)로 결합하고, `updateAction`과 `deleteAction`을 무조건 `"NO ACTION"`으로 하드코딩하여 클라이언트(DbGate, SSMS, DBeaver) 스키마 분석 시 복합 외래키가 단일 이상 컬럼(`"col1, col2"`)으로 인식되고 실제 `CASCADE`/`SET NULL` 제약이 은폐되던 결함.
     - 복합 외래키 컬럼 쌍마다 개별 메타데이터 행 방출 및 `fk.OnUpdate.String()`, `fk.OnDelete.String()` 실측 액션 바인딩.
  93. **`BUG-VM-DML-PK-NOT-FOUND-CRASH-1` (`pkg/proc/runtime/vm_dispatcher.go:671, 719`)**:
     - VDBE 컴파일러가 최적화한 단일 PK 기반 `OpUpdate` 및 `OpDelete` 실행 시, 스토리지 계층(`f.tx.Update`, `f.tx.Delete`)이 반환한 `storage.ErrKeyNotFound`를 정상적인 0건 매칭(`@@ROWCOUNT = 0`)으로 처리하지 않고 치명적 런타임 에러로 판단하여 프로시저를 강제 중단(Catch 점프)시키던 SQL 규격 위반 결함.
     - `ErrKeyNotFound` 수신 시 에러 전파 대신 `f.rowCount = 0` 설정 후 정상 완료로 분기.
  94. **`BUG-EXEC-PROC-TX-BYPASS-1` (`pkg/engine/sqlexec_procs.go:988-992, 1026`)**:
     - `execProcedureCall`(`EXEC procName @args`) 실행 시 활성 클라이언트 세션의 트랜잭션(`cs.Tx`)을 확인하지 않고 무조건 `s.engine.NewSession()`으로 독립 스토리지를 생성하여 프로시저를 실행함으로써, 선행 트랜잭션 변경사항을 읽지 못하고 프로시저 수정분이 즉시 자동 커밋되어 외부 `ROLLBACK`이 무력화되던 트랜잭션 원자성 파괴 결함.
     - `cs.Tx` 존재 시 `ExecuteInTxContext`를 통해 상위 활성 트랜잭션을 공유 바인딩하여 트랜잭션 일관성 보장.
  95. **`BUG-TDS-RPC-PREP-TX-BYPASS-1` (`pkg/wire/tds/server.go:1031-1035, 1114-1117`)**:
     - TDS 프로토콜 `sp_execute` 및 `sp_prepexec`의 VDBE 사전 컴파일 플랜 실행 경로에서 활성 세션 컨텍스트 및 `inTx` 트랜잭션을 무시하고 매번 독립 `s.engine.NewSession()`을 생성하여 실행함으로써, TDS 트랜잭션 내에서 실행된 준비된 문장이 트랜잭션 외부에서 즉시 영구 커밋되던 결함.
     - TDS 세션 트랜잭션 상태를 연동하여 트랜잭션 내에서는 활성 트랜잭션 컨텍스트를 경유하도록 개선.
  96. **`BUG-TDS-RPC-PARAM-NAME-ORDER-1` (`pkg/wire/tds/server.go:1205-1216`)**:
     - `handleRPCExecuteSQL`(`sp_executesql`)이 파라미터명(`p.Name`)을 검사하지 않고 `rpc.Params[0]`을 `@stmt`, `rpc.Params[1]`을 `@params`로 단정하여, JDBC나 .NET 드라이버가 파라미터 순서를 바꾸어 전달할 때 `@stmt`와 `@params`가 뒤바뀌어 SQL 구문 에러를 유발하던 결함.
     - `p.Name`의 대소문자 무관 비교를 통해 `@stmt`와 `@params`를 정확히 식별 바인딩하도록 보강.
  97. **`BUG-PARSER-SIMPLE-CASE-OMISSION-1` (`pkg/proc/parser_expr.go:445-475`, `pkg/proc/ast.go:486`)**:
     - Pratt 파서의 `parseCase`가 Searched CASE(`CASE WHEN cond THEN ...`)만 파싱하고 `CASE` 바로 뒤에 `TokenWhen`이 오지 않는 Simple CASE(`CASE expr WHEN val THEN ...`)를 만나면 즉시 문법 에러(`nil`)를 반환하며, `CaseExpr` AST에도 피연산자(`Operand`) 필드가 결여되어 있던 결함.
     - `CASE` 직후 조건식 존재 시 `Operand`로 파싱하고 `CaseExpr`에 피연산자 필드 추가 및 VDBE/평가기 연동.
  98. **`BUG-PIVOT-ORDER-BY-SPLIT-1` (`pkg/engine/sqlexec_pivot.go:420-425, 946-950`)**:
     - `PIVOT` 및 `UNPIVOT` 연산자 처리 시 `ORDER BY` 절을 단순 공백 분해(`strings.Fields`)하여, 콤마가 붙은 컬럼명(`"col1,"`)이나 방향 키워드(`"DESC,"`)가 매칭에 실패하여 정렬 방향이 뒤바뀌거나 다중 컬럼 정렬이 완전히 무력화되던 결함.
     - 스마트 토큰 분해기를 통해 콤마를 분리하고 모든 다중 정렬 컬럼 및 방향을 온전히 반영하도록 수정.
  99. **`BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1` (`pkg/engine/sqlexec_eval.go:920-965`)**:
     - 코디네이터 폴백 `evalUpdateExpr`에서 단일 숫자 가감승제 정규식만 매칭하고, 0으로 나누기(`delta == 0`)나 컬럼 간 연산(`salary = salary + bonus`) 발생 시 실패를 무시한 채 `parseLiteralValue`로 떨어져 값을 0으로 만들거나 정수 컬럼에 문자열 값을 대입하여 데이터를 사일런트 변조하던 결함.
     - 0으로 나누기 시 명시적 예외 반환 및 컬럼 참조 산술식 평가 기능 보강.
  100. **`BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1` (`pkg/storage/memengine/persistence.go:61-70`)**:
     - MemEngine의 WAL 복구 루프 `applyWALRecordLocked`에서 `OpCreateTable` 재생 시 `td.Indexes` 맵만 생성하고 `op.Schema.Indexes`에 정의된 인라인 인덱스들을 전혀 등록하지 않아, 크래시 복구 후 생성된 테이블의 인덱스들이 증발하여 후속 WAL의 `OpInsert`/`OpUpdate` 시 인덱스가 갱신되지 못하던 결함.
     - `op.Schema.Indexes`를 순회하며 `td.Indexes`에 각 인덱스 구조체를 정상 인스턴스화하도록 복구 로직 보강.
  101. **`BUG-WINDOW-RANGE-PEER-EVAL-1` (`pkg/execution/window.go:303-309, 480-510`, `pkg/planner/planner.go:63, 1713`)**:
     - `WindowIterator`가 `ORDER BY` 지정 시 SQL 표준 기본 프레임인 `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`를 `ROWS` 기반 단순 행 인덱스 슬라이딩으로 축소 처리하여, 동순위 피어(Peer) 행들이 동일한 누적 집계값을 갖지 못하고 임의의 행 순서에 따라 서로 다른 누적값을 반환하는 SQL 표준 규격 위반 결함.
     - `WindowFrame`에 `RANGE` 모드를 명시하고 동순위 키를 가진 피어 행들을 동일 프레임으로 묶어주는 피어 구간 확장 로직 구축.
  102. **`BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1` (`pkg/storage/memengine/tx.go:1557-1588, 1645, 1674`)**:
     - `memengine.Transaction.Commit()`의 5단계 OCC 검증이 커밋된 인덱스 데이터만을 상대로 `ScanRange`를 수행하여, 동일 트랜잭션 내부에서 여러 행이 동일한 유니크 키로 삽입/수정될 경우 이를 감지하지 못하고, 6단계 적용 루프에서 `idxData.Insert`가 반환한 유니크 위반 에러를 `_ =`로 침묵시켜 중복 행들이 스토리지에 무단 커밋되던 결함.
     - `tx.changes` 내부 상호 유니크 키 충돌 사전 검사 및 적용 단계 에러 즉시 전파로 중복 커밋 원천 차단.
  103. **`BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1` (`pkg/storage/memengine/tx.go:481-568`)**:
     - `memengine.Transaction.updateLocked` 실행 시 유니크 인덱스 제약 검증이 완전히 누락되어, 행을 수정하여 다른 행의 고유 키와 충돌하더라도 DML 실행 시점에 에러가 발생하지 않고 트랜잭션 커밋 단계까지 오류 감지가 지연되던 결함.
     - `updateLocked` 시점에 대상 유니크 인덱스 및 로컬 변경본(`tx.local`) 대상 충돌 사전 검증 루프 구축.
  104. **`BUG-API-JSON-DATETIME-EMPTY-STR-1` (`pkg/api/handlers_records.go:53`, `pkg/storage/engine.go:466`)**:
     - `DateTimeValue` 생성 시 `SVal`을 비워두는(`""`) 반면, HTTP API의 `recordToJSONMap` 스키마 분기에서 `default: row[col.Name] = v.SVal`을 직접 읽어냄으로써 REST API로 레코드 조회 시 모든 DATETIME/DATE/TIME 컬럼이 빈 문자열(`""`)로 방출되던 일시 데이터 유실 결함.
     - `v.SVal` 직접 참조 대신 `v.String()` 또는 `v.TVal` 기반 ISO-8601 포맷팅으로 직렬화 수정.
  105. **`BUG-API-JSON-DECIMAL-PRECISION-LOSS-1` (`pkg/api/handlers_records.go:46-51`)**:
     - `recordToJSONMap`이 DECIMAL 컬럼을 직렬화할 때 `strconv.ParseFloat(v.String(), 64)`로 무조건 변환하여 부동소수점(`float64`)으로 변환함으로써, 금융/통화 데이터에서 IEEE-754 이진 부동소수점 오차(예: `0.1000000000000000055511...`)가 발생하던 정밀도 파괴 결함.
     - DECIMAL 컬럼을 고정 소수점 문자열 또는 `json.Number`로 직렬화하여 금융 연산 정밀도 보존.
  106. **`BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1` (`pkg/engine/sqlexec_derived.go:116-118`)**:
     - `resolveDerivedTables`에서 서브쿼리 실행 결과가 0건일 때 `Columns[0]`을 임의로 PRIMARY KEY(`PrimaryKey: true, Nullable: false`)로 지정하여, 첫 번째 컬럼이 NULL 허용이거나 중복값을 갖는 경우 파생 테이블 임시 스키마 제약이 파괴되던 결함.
     - 결과가 0건이거나 고유 컬럼이 확정되지 않은 경우 항상 합성 가상 기본키(`_dt_row_id`)를 부여하도록 일원화.
  107. **`BUG-PITR-DDL-SILENT-SWALLOW-1` (`pkg/backup/pitr.go:112, 121, 129, 137, 146, 155`)**:
     - PITR 및 WAL 재생 엔진 `ApplyWALRecordWithOptions`에서 DDL 작업(`CreateTable`, `DropTable`, `TruncateTable`, `DropPartition`, `CreateIndex`, `DropIndex`) 실행 시 발생하는 에러를 `_ =`로 완전히 침묵시켜, 테이블 생성 실패 등 치명적 오류에도 복구가 성공한 것처럼 가장되던 결함.
     - DDL 호출 에러 즉시 반환 및 롤백 연동으로 복구 실패 시 정밀 진단 보장.


---

## 3. 우선순위 레벨 및 컴포넌트별 조치 현황 (Priority & Component Breakdown)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 상태 | 관련 기술 문서 / 조치 내역 |
| :---: | :---: | :--- | :--- | :---: | :--- |
| **P1** | **엔진/무결성** | **비-`id` 기본키 테이블 및 VDBE의 `UPDATE`/`DELETE` 시 Go 맵 무작위 순회로 엉뚱한 행 삭제/수정 방지 (`BUG-PK-1`)** | `pkg/engine`, `pkg/proc/runtime` | **완료 (`[x]`)** | - |
| **P1** | **엔진/대소문자** | **`TableSchema.FindColumn`의 `strings.EqualFold` 대소문자 무관 탐색 보장 (`BUG-CASE-1`)** | `pkg/storage/engine.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/무결성** | **DML Record 컬럼명 정규화(`colSchema.Name`)로 `RecordToRow` 업데이트 유실 및 NULL 삽입 방지 (`BUG-RECORD-1`)** | `pkg/engine/sqlexec_dml.go`, `pkg/storage/tuple.go` | **완료 (`[x]`)** | - |
| **P1** | **수식/무결성** | **`evalUpdateExpr` 수식 평가 시 컬럼명 대소문자 무관 탐색으로 컬럼 0 강제 리셋 방지 (`BUG-ARITH-1`)** | `pkg/engine/sqlexec_eval.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/무결성** | **VDBE `SELECT ... INTO @var` 무작위 맵 순회 제거 및 `cols[0]` 결정론적 바인딩 (`BUG-VM-INTO-1`)** | `pkg/proc/runtime/vm_dispatcher.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/무결성** | **스칼라 서브쿼리 수식 평가 시 무작위 맵 순회 제거 및 `cols[0]` 대소문자 무관 탐색 (`BUG-SUBQ-1`)** | `pkg/proc/eval.go` | **완료 (`[x]`)** | - |
| **P1** | **플래너/인덱스** | **`ParseIndexCond` 무작위 맵 순회 제거 및 인덱스 대상 컬럼 바운드 정밀 매칭 (`BUG-BOUNDS-1`)** | `pkg/planner/bounds.go` | **완료 (`[x]`)** | - |
| **P2** | **엔진/Upsert** | **`ON CONFLICT (col)` 충돌 컬럼 대소문자 정규화로 Upsert 유니크 위반 방지 (`BUG-UPSERT-1`)** | `pkg/engine/sqlexec_dml.go` | **완료 (`[x]`)** | - |
| **P2** | **엔진/조회** | **`evaluateSingleConditionMap` 내 `WHERE IN (...)` 절 대소문자 무관 탐색 누락 보강 (`BUG-WHERE-1`)** | `pkg/engine/sqlexec_eval.go` | **완료 (`[x]`)** | - |
| **P2** | **엔진/옵티마이저** | **`EXPLAIN` 서브쿼리 평탄화 시 무작위 맵 순회 제거 및 서브쿼리 컬럼 키 정합 (`BUG-EXP-1`)** | `pkg/engine/sqlexec_explain.go` | **완료 (`[x]`)** | - |
| **P1** | **아키텍처/추상화** | **통합 SQL 추상화 레이어 구축 (`storage.Record` 1급 객체화, `SQLRow`, 경계 정규화) (`SQL-ABSTRACT-1`)** | `pkg/storage`, `pkg/engine` | **완료 (`[x]`)** | [`notes/SQL_ABSTRACTION_LAYER_PLAN.md`](notes/SQL_ABSTRACTION_LAYER_PLAN.md) |
| **P1** | **엔진/무결성** | **PK 미지정 테이블 생성 시 `Columns[0]`을 멋대로 PK로 승격하는 안티패턴 제거 및 가상 PK(`_row_id`) 보장 (`BUG-DDL-PK-1`)** | `pkg/engine/sqlexec_ddl.go`, `pkg/api/handlers_tables.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/무결성** | **`SELECT INTO` 및 파생 테이블의 일시적 고유 컬럼 임의 PK 지정 및 0건 시 `Columns[0]` PK 지정 제거 (`BUG-SELECT-INTO-PK-1`)** | `pkg/engine/sqlexec_select.go`, `pkg/engine/sqlexec_derived.go` | **완료 (`[x]`)** | - |
| **P1** | **실행/조인** | **`IndexNestedLoopJoin` 구성 시 인덱스 미일치 시 `Indexes[0]`을 임의 선택하여 조인 오염시키는 결함 방지 (Hash Join 안전 전환) (`BUG-JOIN-IDX-1`)** | `pkg/execution/builder.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/WiredTiger** | **WiredTiger 복합 인덱스 스캔 시 첫 번째 컬럼만 비교하고 컬럼 누락 시 TypeUnknown 0(동일) 판정하는 결함 수정 (`BUG-WT-INDEX-COMP-1`)** | `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | 복합 인덱스 다중 컬럼 인코딩 키 비교 및 누락 컬럼 미초기화 동등 판정 차단 |
| **P2** | **프로시저/VM** | **VDBE VM에서 복합 인덱스를 단일 인덱스로 착각하고 비인코딩 스캔하여 0건 반환되는 결함 수정 (`BUG-VM-COMP-IDX-1`)** | `pkg/proc/runtime/vm.go` | **완료 (`[x]`)** | 단일 컬럼 조건 시 복합 인덱스 오선택 차단 및 다중 조건 일치 시 복합 인코딩 키 스캔 연동 |
| **P2** | **엔진/정렬** | **`strings.Fields` 분해로 인한 다중 정렬 누락, 콤마 포함 시 DESC 무력화 및 대소문자 미매칭 무력화 원천 해결 (`BUG-ORDER-BY-SPLIT-1`)** | `pkg/catalog/virtual/result.go`, `pkg/engine/sqlexec_select.go` | **완료 (`[x]`)** | - |
| **P2** | **실행/파싱** | **`strings.Split(cond, "=")` 다중 조건 분해 실패 시 빈 컬럼명 전달 및 PGWire FROM 분해 오작동 방지 (`BUG-JOIN-PARSE-1`)** | `pkg/execution/builder.go`, `pkg/wire/server.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/스키마** | **`GetTable`/`DropTable` 대소문자 무관 탐색 보장 (`BUG-TABLE-CASE-1`)** | `pkg/storage/memengine`, `pkg/storage/wiredtiger` | **완료 (`[x]`)** | `sqlutil` 공통 추상화 및 MapGetFold/MapDeleteFold 적용 |
| **P1** | **스토리지/WiredTiger** | **WiredTiger 스토리지 엔진 외래키(FK) 무결성 검증 및 CASCADE/RESTRICT 구축 (`BUG-WT-FK-1`)** | `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/MemEngine** | **MemEngine 외래키 검사 시 부모 컬럼 대소문자 정규화 (`BUG-FK-CASE-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/DML** | **`INSERT ... VALUES` 튜플 수식 내 콤마 분해 정합성 보장 (`BUG-SPLIT-VALUES-1`)** | `pkg/engine/sqlexec_eval.go`, `pkg/engine/sqlexec_dml.go` | **완료 (`[x]`)** | - |
| **P2** | **엔진/MERGE** | **MERGE 문 `UPDATE SET` 절 다중 인자 수식 콤마 분해 사일런트 유실 방지 (`BUG-MERGE-SET-1`)** | `pkg/engine/sqlexec_merge.go` | **완료 (`[x]`)** | - |
| **P2** | **와이어/COPY** | **PGWire `COPY FROM` 시 정수 기본키 `0`의 타임스탬프 위조 방지 (`BUG-COPY-PK-1`)** | `pkg/wire/copy.go` | **완료 (`[x]`)** | - |
| **P1** | **프로시저/내장함수** | **`UPPER`/`LOWER`/`TRIM`/`REPLACE` 등 문자열 함수 NULL 입력 시 NULL 정규 반환 (`BUG-STR-NULL-1`)** | `pkg/proc/builtins_string.go` | **완료 (`[x]`)** | - |
| **P1** | **프로시저/VM** | **VDBE `OpUpdate`/`execUpdateWhere` 컬럼명 정규화로 업데이트 사일런트 유실 방지 (`BUG-VM-UPDATE-1`)** | `pkg/proc/runtime/vm_dispatcher.go`, `pkg/proc/runtime/vm.go` | **완료 (`[x]`)** | TableSchema.FindColumn 및 UPDATE 대입 컬럼명 sqlutil.EqualFoldIdent 정규화 적용 |
| **P1** | **스토리지/MemEngine** | **MemEngine 자식 행 삽입 시 대소문자 불일치로 가짜 외래키 삽입 허용 결함 수정 (`BUG-FK-CHILD-CASE-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | - |
| **P2** | **와이어/PGWire** | **PGWire 파라미터 미리보기 시 단순 문자열 치환으로 `$10` 깨짐 방지 (`BUG-PGWIRE-PARAM-1`)** | `pkg/wire/server.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/APPLY** | **`CROSS/OUTER APPLY` TVF 파라미터 치환 시 단어경계 단순 정규식 및 무작위 맵 순회 쿼리 훼손 방지 (`BUG-APPLY-REGEX-1`)** | `pkg/engine/sqlexec_apply.go` | **완료 (`[x]`)** | 토큰 인식 파라미터 치환 및 결정론적 식별자 치환으로 문자열/키워드 오염 방지 |
| **P2** | **엔진/문자열** | **`STRING_SPLIT(str, sep)` 빈 구분자("") 전달 시 콤마(`,`) 임의 간주 제거 및 규격 에러 반환 (`BUG-STRING-SPLIT-SEP-1`)** | `pkg/engine/sqlexec_string_split.go` | **완료 (`[x]`)** | 빈 구분자 전달 시 표준 규격 에러 반환 |
| **P1** | **스토리지/DDL** | **`ALTER TABLE ADD COLUMN` 시 기본값 없는 `NOT NULL` 컬럼에 대해 기존 행 NULL 오염 방지 (`BUG-ADD-COL-NOT-NULL-1`)** | `pkg/storage/memengine/ddl.go`, `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | 데이터 존재 테이블에 DEFAULT 없는 NOT NULL 추가 거부 및 DEFAULT 지정 시 기존 행 정상 채번 보장 |
| **P1** | **엔진/DML** | **`ExecuteUpdate`/`ExecuteDelete` 폴백 시 비-`id` PK 테이블 무작위 컬럼 PK 간주 데이터 파괴 해결 (`BUG-DML-PK-RANDOM-1`)** | `pkg/engine/sqlexec_dml.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/DML** | **`ExecuteUpdate`의 `SET` 절 파싱 시 괄호/따옴표 미추적 콤마 분해로 다중 인자 수식 사일런트 유실 방지 (`BUG-UPDATE-SET-SPLIT-1`)** | `pkg/engine/sqlexec_dml.go` | **완료 (`[x]`)** | - |
| **P2** | **API/테이블생성** | **HTTP API 테이블 생성 시 PK 임의 승격 및 미지원 타입 VARCHAR 임의 간주 안티패턴 제거 (`BUG-HTTP-TABLE-PK-1`)** | `pkg/api/handlers_tables.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/무결성** | **스토리지 엔진 `Update` 시 입력 레코드 컬럼명 대소문자 미정규화로 인한 업데이트 사일런트 유실 방지 (`BUG-STORAGE-UPDATE-CASE-1`)** | `pkg/storage/memengine/tx.go`, `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/튜플** | **`RecordToRow` 및 `ApplyRecordToRow` 대소문자 엄격 일치로 인한 컬럼 값 NULL 덮어쓰기 데이터 훼손 방지 (`BUG-TUPLE-RECORD-CASE-1`)** | `pkg/storage/tuple.go` | **완료 (`[x]`)** | Record Get/Set/Delete 대소문자 및 브래킷/접두사 매칭 완전 정합화 |
| **P1** | **엔진/인덱스** | **`isIndexAscendingOrder`/`isIndexDescendingOrder` 다중 정렬 컬럼 무시로 인한 정렬 누락 왜곡 방지 (`BUG-INDEX-SCAN-ORDERBY-1`)** | `pkg/engine/sqlexec_eval.go` | **완료 (`[x]`)** | ORDER BY 전체 컬럼 및 정렬 방향 일치 검증, 불일치 시 일반 실행/정렬 안전 폴백 |
| **P2** | **와이어/COPY** | **PGWire `COPY FROM`의 `DELIMITER` 파싱 시 후속 옵션 미토큰화로 인한 따옴표 구분자 변조 방지 (`BUG-COPY-DELIM-1`)** | `pkg/wire/copy.go` | **완료 (`[x]`)** | - |
| **P1** | **와이어/PGWire** | **PGWire `writeSchemaDescription` 및 `inferColumnOIDs`의 단순 문자열 분해로 인한 OID 왜곡 방지 (`BUG-PGWIRE-DESCRIBE-1`)** | `pkg/wire/server.go` | **완료 (`[x]`)** | - |
| **P1** | **프로시저/컴파일러** | **VDBE 파서 및 컴파일러에서 `FROM` 없는 스칼라 서브쿼리 nil 패닉 및 다중 컬럼 침묵 버림 방지 (`BUG-VM-SUBQ-NIL-1`)** | `pkg/proc/parser_expr.go`, `pkg/proc/compiler.go` | **완료 (`[x]`)** | FROM 없는 스칼라 서브쿼리 직접 평가 컴파일 및 다중 컬럼 서브쿼리 구문 거부 |
| **P2** | **카탈로그/정렬** | **가상 카탈로그 및 DBCC `ORDER BY` 다중 정렬 시 `strings.Fields` 콤마 잔존으로 인한 정렬 무력화 방지 (`BUG-CATALOG-ORDERBY-1`)** | `pkg/catalog/virtual/result.go`, `pkg/engine/sqlexec_dbcc.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/WiredTiger** | **WiredTiger `IndexScanDesc` 미구현 스텁으로 인한 역순 정렬 파괴 결함 해결 (`BUG-WT-INDEX-DESC-1`)** | `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | wtSliceCursor 기반 역순 정렬 스캔 및 ORDER BY DESC 정합성 보장 |
| **P1** | **엔진/CTE** | **CTE 구체화 시 첫 번째 컬럼 임의 PK 강제 및 전체 VARCHAR(255) 타입 파괴 결함 해결 (`BUG-CTE-MATERIALIZE-1`)** | `pkg/engine/sqlexec_explain.go` | **완료 (`[x]`)** | - |
| **P1** | **와이어/TDS** | **TDS 프로토콜 행 직렬화 시 대소문자 엄격 일치로 인한 광범위한 NULL 변조 결함 해결 (`BUG-TDS-ROW-CASE-1`)** | `pkg/wire/tds/token.go` | **완료 (`[x]`)** | - |
| **P2** | **카탈로그/파싱** | **가상 카탈로그 `cleanIdentifier`의 3-part/4-part 식별자 파싱 파괴 결함 해결 (`BUG-CATALOG-IDENT-1`)** | `pkg/catalog/virtual/result.go` | **완료 (`[x]`)** | 점 분할 후 마지막 세그먼트 정규 식별자 트림 적용 |
| **P1** | **실행/프로젝션** | **`CAST(expr AS type)` 수식 내 `" AS "` 오분해로 인한 전 행 NULL 반환 결함 해결 (`BUG-PROJ-CAST-AS-1`)** | `pkg/execution/project.go`, `pkg/wire/server.go` | **완료 (`[x]`)** | - |
| **P1** | **플래너/서브쿼리** | **서브쿼리 비상관화 시 `"and"` 접미 단순 Trim으로 인한 문자열 리터럴 절단 훼손 해결 (`BUG-DECORRELATE-TRIM-1`)** | `pkg/planner/decorrelate.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/인덱스** | **`extractIndexKey`의 대소문자 미정규화로 인한 세컨더리 인덱스 NULL 오염 결함 해결 (`BUG-INDEX-KEY-CASE-1`)** | `pkg/storage/memengine/ddl.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/외래키** | **FK 부모 테이블명 대소문자 불일치 시 `ON DELETE RESTRICT` 완전 우회 삭제 허용 결함 해결 (`BUG-FK-PARENT-TABLE-CASE-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | - |
| **P2** | **플래너/정렬** | **CBO 및 SetOp 플래너 다중 정렬 시 `strings.Fields` 콤마 잔존 플랜 왜곡 해결 (`BUG-PLANNER-ORDERBY-1`)** | `pkg/planner/planner.go`, `pkg/planner/setop.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/WiredTiger** | **WiredTiger 60분 유휴 세션 방치 sweep-server 경고 방지 및 `session->reset` 연동 (`BUG-WT-SWEEP-1`)** | `pkg/storage/wiredtiger/wt_engine.go`, `pkg/engine/session_pool.go` | **완료 (`[x]`)** | WT_SESSION::reset CGO 바인딩, 커밋/롤백 시 리셋 연동 및 SessionPool 유휴 세션 TTL 리퍼 도입 |
| **P2** | **API/PK생성** | **HTTP API 레코드 생성 시 밀리초 타임스탬프 충돌 및 UUID PK NULL 방치 결함 해결 (`BUG-API-PK-GEN-1`)** | `pkg/api/handlers_records.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/복구** | **MemEngine WAL 테일 절단(Torn Write) 시 기동 실패 방지 및 정상 복구 바운더리 보장 (`BUG-WAL-TORN-BOOT-1`)** | `pkg/storage/memengine/wal.go`, `pkg/storage/memengine/memengine.go` | **완료 (`[x]`)** | ReplayWAL 미완료/손상 레코드 경계 감지, 직전 유효 커밋까지 복구 및 파일 자동 트렁케이트로 기동 보장 |
| **P2** | **카탈로그/인터셉터** | **가상 카탈로그 조인 인터셉터 대괄호 식별자(`[sys].[tables]`) 인식 누락 테이블 미발견 결함 해결 (`BUG-CATALOG-INTERCEPT-BRACKET-1`)** | `pkg/catalog/virtual/interceptor.go` | **완료 (`[x]`)** | 가상 카탈로그 쿼리 식별자 정규화 및 브래킷 허용 인터셉트 연동 |
| **P1** | **엔진/집계** | **`computeAggregates` 컬럼 대소문자 불일치로 인한 `GROUP BY <nil>` 병합 및 집계함수 NULL 변조 결함 해결 (`BUG-SELECT-AGG-CASE-1`)** | `pkg/engine/sqlexec_select.go` | **완료 (`[x]`)** | - |
| **P1** | **프로시저/파라미터** | **`parseSpParamDeclarations` 콤마 단순 분해로 인한 `DECIMAL(10, 2)` 파라미터 파괴 결함 해결 (`BUG-SP-PARAM-DECIMAL-1`)** | `pkg/engine/sqlexec_procs.go` | **완료 (`[x]`)** | - |
| **P2** | **프로시저/파라미터** | **`cleanOutputSuffix` 경계 미검사로 인한 `@timeout` 등 `OUT` 접미 변수명 절단 훼손 결함 해결 (`BUG-SP-EXEC-SUFFIX-OUT-1`)** | `pkg/engine/sqlexec_procs.go` | **완료 (`[x]`)** | 선행 공백/단어 경계 검사 적용으로 접미사 오절단 방지 |
| **P1** | **스토리지/MemEngine** | **`IndexScan` 트랜잭션 내 로컬 삽입 행 무작위 맵 순회 및 정렬 누락으로 인한 인덱스 순서 파괴 결함 해결 (`BUG-MEM-INDEX-LOCAL-ORDER-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | 트랜잭션 로컬 삽입/수정 행 인덱스 키 추출 후 병합 정렬로 인덱스 스캔 순서 보장 |
| **P1** | **스토리지/비교연산** | **`storage.Value.Compare` 정수-실수(Int-Float) 상호 타입 강제변환 누락으로 인한 비교 역전 결함 해결 (`BUG-STORAGE-CMP-INT-FLOAT-1`)** | `pkg/storage/engine.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/파생테이블** | **`resolveDerivedTables`의 `SELECT *` 조인 시 타 테이블 프로젝션 완전 증발 결함 해결 (`BUG-DERIVED-JOIN-STAR-1`)** | `pkg/engine/sqlexec_derived.go` | **완료 (`[x]`)** | 조인 내 타 테이블 프로젝션 보존 및 단일 파생테이블 한정 SELECT * 정규화 |
| **P1** | **엔진/DDL** | **`splitColumnDefs` 따옴표 미추적으로 인한 DEFAULT 문자열 내 콤마/괄호 스키마 파괴 결함 해결 (`BUG-DDL-SPLIT-QUOTE-1`)** | `pkg/engine/sqlexec_ddl.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/산술수식** | **`evalUpdateExpr` 정규식 경직성으로 인한 대괄호/다중컬럼/상수선행 산술식 0 리셋 결함 해결 (`BUG-ARITH-EXPR-ZERO-1`)** | `pkg/engine/sqlexec_eval.go` | **완료 (`[x]`)** | - |
| **P2** | **스토리지/인덱스** | **`DropIndex` 대소문자 불일치 시 `td.Indexes` 맵 미삭제로 인한 좀비 인덱스 잔존 결함 해결 (`BUG-DROP-INDEX-CASE-1`)** | `pkg/storage/memengine/ddl.go` | **완료 (`[x]`)** | 정규화 인덱스 명칭 기반 MapDeleteFold 삭제로 좀비 인덱스 잔존 방지 |
| **P1** | **실행/집계** | **Volcano `AggregateIterator`에서 동일 그룹 내 Decimal-정수 혼합 시 정수 합계 전량 유실 결함 해결 (`BUG-AGG-MIXED-NUM-DEC-1`)** | `pkg/execution/aggregate.go` | **완료 (`[x]`)** | - |
| **P1** | **프로시저/파서** | **단순 CASE 문(`CASE @val WHEN 1 THEN ...`) 미지원으로 인한 구문 파싱 및 컴파일 실패 결함 해결 (`BUG-PROC-SIMPLE-CASE-1`)** | `pkg/proc/parser_expr.go`, `pkg/proc/ast.go`, `pkg/proc/compiler.go` | **완료 (`[x]`)** | CaseExpr.Operand AST 필드 및 VDBE OpEq 피연산자-조건값 비교 분기 바이트코드 방출/평가 |
| **P1** | **프로시저/평가** | **`evalRowBinaryOp` 불일치 비교(`<>`, `!=`) 시 NULL 3가 논리 위반으로 인한 NULL 행 무단 통과 결함 해결 (`BUG-EVAL-NEQ-NULL-1`)** | `pkg/proc/eval.go` | **완료 (`[x]`)** | - |
| **P2** | **프로시저/날짜함수** | **`DATEADD`/`EOMONTH` 증분 파라미터 NULL 전달 시 0 대체로 날짜 미변조 반환 결함 해결 (`BUG-DATEADD-NULL-1`)** | `pkg/proc/builtins_date.go` | **완료 (`[x]`)** | - |
| **P1** | **프로시저/스토리지** | **VDBE `OpInsert` 상수 리터럴 기본값(DEFAULT) 누락 및 NULL 강제 삽입 결함 해결 (`BUG-VM-INSERT-DEFAULT-1`)** | `pkg/proc/runtime/vm_dispatcher.go` | **완료 (`[x]`)** | proc.ParseLiteralDefault 공통 추상화 연동으로 상수 리터럴/함수 기본값 100% 정상 주입 |
| **P1** | **프로시저/대소문자** | **VDBE DML 컬럼명 대소문자 불일치 시 `RecordToRow`/`updateLocked` 값 유실 및 NULL 삽입 결함 해결 (`BUG-VM-DML-COL-CASE-1`)** | `pkg/proc/runtime/vm_dispatcher.go`, `pkg/storage/tuple.go`, `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/무결성** | **VDBE 및 DML/DDL에서 PK 컬럼 미식별 시 임의 컬럼 무작위 순회(`Values[0]`, `range rec`) 행 파괴 결함 해결 (`BUG-DML-PK-RANDOM-GUESS-1`)** | `pkg/proc/runtime/vm.go`, `pkg/engine/sqlexec_dml.go`, `pkg/engine/sqlexec_ddl.go` | **완료 (`[x]`)** | - |
| **P1** | **와이어/보안** | **PGWire 및 TDS에서 슈퍼유저(`sa`, `postgres`, `admin`) 비밀번호 인증 완전 우회 백도어 결함 해결 (`BUG-AUTH-ADMIN-PW-BYPASS-1`)** | `pkg/wire/server.go`, `pkg/wire/tds/server.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/DML** | **코디네이터 `UPDATE` SET 절 쉼표 단순 분해로 인한 문자열 리터럴 및 함수 인자 쉼표 파괴 결함 해결 (`BUG-UPDATE-SET-COMMA-SPLIT-1`)** | `pkg/engine/sqlexec_dml.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/조건평가** | **코디네이터 `WHERE IN (...)` 쉼표 단순 분해로 인한 쉼표 포함 문자열 리터럴 절단 및 매칭 실패 결함 해결 (`BUG-EVAL-IN-COMMA-SPLIT-1`)** | `pkg/engine/sqlexec_eval.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/비교연산** | **`Value.Compare` 날짜/시간-문자열(`DateTime`-`VarChar`) 상호 변환 누락으로 인한 비교 역전 결함 해결 (`BUG-STORAGE-CMP-DATETIME-STR-1`)** | `pkg/storage/engine.go` | **완료 (`[x]`)** | - |
| **P2** | **스토리지/동등비교** | **`Value.Equal` 실수(Float) 6자리 고정 문자열 포맷팅으로 인한 오판 및 힙 할당 결함 해결 (`BUG-STORAGE-EQUAL-FLOAT-STR-1`)** | `pkg/storage/engine.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/MemEngine** | **트랜잭션 커밋 시 테이블 대소문자 불일치로 인한 사일런트 쓰기 증발 결함 해결 (`BUG-MEM-TX-TABLE-CASE-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | 변경 레코드 테이블명 정규화 및 Commit() MapGetFold 룩업 적용 |
| **P2** | **스토리지/인덱스** | **MemEngine `IndexScan` 인덱스명 대소문자 엄격 일치로 인한 조회 실패 결함 해결 (`BUG-MEM-TX-INDEX-CASE-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | IndexScan/IndexOnlyScan 대소문자 무관 매칭 보장 |
| **P1** | **엔진/MERGE** | **`MERGE` 문의 `WHEN NOT MATCHED THEN INSERT` IDENTITY 기본키 누락 및 실패 결함 해결 (`BUG-MERGE-IDENTITY-PK-1`)** | `pkg/engine/sqlexec_merge.go` | **완료 (`[x]`)** | MERGE INSERT 시 IDENTITY 자동 채번 및 세션/테이블 식별자 동기화 연동 |
| **P1** | **API/레코드생성** | **HTTP API 레코드 생성 시 `NOT NULL DEFAULT` 컬럼 오판 에러 및 기본값 누락 결함 해결 (`BUG-API-CREATE-DEFAULT-1`)** | `pkg/api/handlers_records.go` | **완료 (`[x]`)** | 생략된 필드 대상 스키마 기본값(리터럴 및 함수 기본값) 자동 주입 보강 |
| **P2** | **카탈로그/정규식** | **가상 카탈로그의 T-SQL 유니코드 리터럴(`N'...'`) 및 대괄호 식별자 정규식 미매칭 결함 해결 (`BUG-CATALOG-MSSQL-REGEX-UNICODE-1`)** | `pkg/catalog/virtual/mssql.go` | **완료 (`[x]`)** | N'...' 접두사 및 브래킷 식별자 매칭 지원 정규식 고도화 |
| **P1** | **와이어/COPY** | **`COPY FROM STDIN` 트랜잭션 세션 우회 및 독립 자동 커밋으로 인한 롤백 불가 결함 해결 (`BUG-WIRE-COPY-TX-BYPASS-1`)** | `pkg/wire/copy.go` | **완료 (`[x]`)** | cs.Tx 활성 트랜잭션 바인딩 및 트랜잭션 내 자동 커밋 차단으로 롤백/원자성 보장 |
| **P1** | **와이어/무결성** | **`COPY FROM STDIN` 파싱 에러 침묵 및 0값/현재시각(`time.Now()`) 강제 변조 결함 해결 (`BUG-WIRE-COPY-SILENT-CORRUPT-1`)** | `pkg/wire/copy.go` | **완료 (`[x]`)** | parseFieldVal 전 타입 엄격 검증 및 파싱 에러 시 즉시 롤백/에러 전파 |
| **P1** | **와이어/COPY** | **`COPY TO STDOUT` 텍스트 포맷 탭/줄바꿈(`\t`, `\n`) 이스케이프 누락으로 인한 데이터 왜곡 해결 (`BUG-WIRE-COPY-OUT-UNESCAPED-1`)** | `pkg/wire/copy.go` | **완료 (`[x]`)** | escapeCopyText / unescapeCopyText / splitCopyTextLine 텍스트 포맷 표준 이스케이프 완비 |
| **P1** | **실행/조인** | **`HashJoin`/`MergeJoin` 문자열 조인 키 대소문자 엄격 일치로 인한 조인 행 누락 결함 해결 (`BUG-EXEC-JOIN-STR-CASE-1`)** | `pkg/execution/join.go`, `pkg/execution/merge_join.go`, `pkg/execution/iterator.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/PIVOT** | **`PIVOT`의 `COUNT` 집계 시 매칭 행 없는 카테고리 0 대신 NULL 반환 결함 해결 (`BUG-PIVOT-COUNT-NULL-1`)** | `pkg/engine/sqlexec_pivot.go` | **완료 (`[x]`)** | COUNT 집계에 대해 빈 그룹 시 0을 정규 방출하도록 보강 |
| **P2** | **엔진/FOR_JSON** | **`FOR JSON PATH`에서 테이블 별칭(`u.col`)을 중첩 객체 경로로 오판하는 결함 해결 (`BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1`)** | `pkg/engine/sqlexec_pivot.go` | **완료 (`[x]`)** | 테이블 별칭과 컬럼 별칭 구분으로 순수 컬럼 프로젝션 시 불필요 래핑 제거 |
| **P1** | **엔진/DDL** | **테이블 레벨 `PRIMARY KEY (col1, col2)` 콤마 미분해로 인한 복합 PK 파괴 결함 해결 (`BUG-DDL-COMPOSITE-PK-PARSE-1`)** | `pkg/engine/sqlexec_ddl.go` | **완료 (`[x]`)** | - |
| **P1** | **보안/RBAC** | **RBAC `HasTablePermission`에서 사용자 소속 커스텀 역할(`u.Roles`) 권한 검사 누락 결함 해결 (`BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1`)** | `pkg/auth/rbac.go` | **완료 (`[x]`)** | 사용자 소속 커스텀 롤(`u.Roles`) 순회 및 DENY 우선순위 평가 연동 |
| **P1** | **플래너/조건분해** | **`splitAndConditions`의 이스케이프 따옴표(`''`) 및 대괄호 식별자(`[col AND val]`) AND 오절단 결함 해결 (`BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1`)** | `pkg/planner/decorrelate.go`, `pkg/planner/pushdown.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/스키플리스트** | **SkipList 유니크 인덱스 재인덱싱 시 `for _, item` 값 복사 순회로 커버링 컬럼 갱신 유실 결함 해결 (`BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1`)** | `pkg/storage/memengine/skiplist.go` | **완료 (`[x]`)** | 슬라이스 직접 인덱싱 대입(`curr.Items[i].IncludeValues`)으로 커버링 데이터 최신화 보장 |
| **P1** | **스토리지/동시성** | **`TableData.ensureShardsInit` 락 없는 맵 초기화로 인한 동시 읽기 트랜잭션 런타임 패닉 결함 해결 (`BUG-MEM-SHARD-INIT-RACE-1`)** | `pkg/storage/memengine/table_data.go` | **완료 (`[x]`)** | sync.Once 기반 멱등적 1회 초기화로 동시 읽기 트랜잭션 맵 경쟁 원천 방지 |
| **P2** | **함수/날짜시간** | **`TODATETIMEOFFSET`의 정수 분(Minute) 단위 오프셋을 시간(Hour)으로 60배 왜곡 처리하는 결함 해결 (`BUG-DATE-TODATETIMEOFFSET-INT-MINS-1`)** | `pkg/proc/builtins_date.go` | **완료 (`[x]`)** | - |
| **P2** | **카탈로그/메타데이터** | **가상 카탈로그의 복합 외래키 쉼표 병합(`"col1, col2"`) 및 CASCADE 액션 누락 결함 해결 (`BUG-CATALOG-FK-MULTI-COL-ACTIONS-1`)** | `pkg/catalog/virtual/mssql.go` | **완료 (`[x]`)** | 복합 FK 컬럼 쌍별 개별 행 방출 및 실측 OnUpdate/OnDelete 액션 바인딩 |
| **P1** | **VM/DML** | **VDBE `OpUpdate`/`OpDelete` 단일 PK 행 미존재 시 `ErrKeyNotFound` 치명적 프로시저 중단 결함 해결 (`BUG-VM-DML-PK-NOT-FOUND-CRASH-1`)** | `pkg/proc/runtime/vm_dispatcher.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/프로시저** | **`execProcedureCall`에서 활성 트랜잭션(`cs.Tx`) 무시 및 독립 세션 자동 커밋으로 인한 원자성 파괴 해결 (`BUG-EXEC-PROC-TX-BYPASS-1`)** | `pkg/engine/sqlexec_procs.go` | **완료 (`[x]`)** | cs.Tx 활성 트랜잭션 바인딩 및 ExecuteInTxWithSessionContext 연동으로 원자적 롤백/일관성 보장 |
| **P1** | **와이어/TDS** | **TDS RPC 사전 컴파일 플랜 실행 시 독립 스토리지 세션 강제 생성으로 인한 트랜잭션 우회 해결 (`BUG-TDS-RPC-PREP-TX-BYPASS-1`)** | `pkg/wire/tds/server.go` | **완료 (`[x]`)** | cs.Tx 활성 트랜잭션 감지 시 ExecuteInTxWithSessionContext 연동으로 트랜잭션 격리 보장 |
| **P2** | **와이어/TDS** | **TDS `handleRPCExecuteSQL`에서 파라미터명 미인식으로 인한 `@stmt`/`@params` 도치 결함 해결 (`BUG-TDS-RPC-PARAM-NAME-ORDER-1`)** | `pkg/wire/tds/server.go` | **완료 (`[x]`)** | 2단계 파라미터명(대소문자 무관 `@stmt`, `@params`) 사전 추출 및 순서 도치 완전 방어 |
| **P1** | **파서/문법** | **Pratt 파서 `parseCase`에서 Simple CASE (`CASE expr WHEN ...`) 피연산자 누락 및 문법 거부 해결 (`BUG-PARSER-SIMPLE-CASE-OMISSION-1`)** | `pkg/proc/parser_expr.go`, `pkg/proc/ast.go` | **완료 (`[x]`)** | Pratt 파서 parseCase 피연산자 파싱 및 CaseExpr.Operand AST 필드/평가기 연동 완비 |
| **P2** | **엔진/정렬** | **PIVOT 및 UNPIVOT `ORDER BY` 절 쉼표 분해 왜곡으로 인한 정렬 키 파괴 및 방향 무력화 해결 (`BUG-PIVOT-ORDER-BY-SPLIT-1`)** | `pkg/engine/sqlexec_pivot.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/DML** | **코디네이터 `UPDATE` 산술식 평가 시 0으로 나누기 및 컬럼 간 연산 시 문자열 폴백 데이터 변조 해결 (`BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1`)** | `pkg/engine/sqlexec_eval.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/WAL** | **WAL 충돌 복구 시 `OpCreateTable` 인라인 인덱스 초기화 누락으로 인한 인덱스 갱신 중단 해결 (`BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1`)** | `pkg/storage/memengine/persistence.go` | **완료 (`[x]`)** | op.Schema.Indexes 순회 초기화로 WAL 복구 후 세컨더리 인덱스 정상 갱신 보장 |
| **P1** | **실행/윈도우** | **윈도우 함수 `ORDER BY` 시 `RANGE` 피어 구간 미확장으로 인한 동순위 행 누적 집계 왜곡 해결 (`BUG-WINDOW-RANGE-PEER-EVAL-1`)** | `pkg/execution/window.go`, `pkg/planner/planner.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/OCC** | **단일 트랜잭션 내 다중 유니크 인덱스 충돌 행 커밋 시 에러 침묵 및 중복 데이터 영구 삽입 해결 (`BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | tx.changes 내부 상호 유니크 충돌 사전 검증 및 커밋 단계 에러 즉시 전파 |
| **P1** | **스토리지/OCC** | **MemEngine `updateLocked`에서 유니크 인덱스 제약 검증 완전 누락 결함 해결 (`BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1`)** | `pkg/storage/memengine/tx.go` | **완료 (`[x]`)** | updateLocked 내 대상 유니크 인덱스 및 로컬 변경본 대상 충돌 사전 검증 |
| **P1** | **API/직렬화** | **HTTP REST API 레코드 조회 시 DATETIME/DATE/TIME 컬럼 빈 문자열(`""`) 반환 결함 해결 (`BUG-API-JSON-DATETIME-EMPTY-STR-1`)** | `pkg/api/handlers_records.go`, `pkg/storage/engine.go` | **완료 (`[x]`)** | - |
| **P2** | **API/정밀도** | **HTTP REST API 레코드 직렬화 시 DECIMAL 컬럼 float64 변환 부동소수점 오차 결함 해결 (`BUG-API-JSON-DECIMAL-PRECISION-LOSS-1`)** | `pkg/api/handlers_records.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/파생테이블** | **0건 반환 파생테이블에서 임의의 `Columns[0]`을 PK로 승격하여 후속 쿼리 실패 초래하는 결함 해결 (`BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1`)** | `pkg/engine/sqlexec_derived.go` | **완료 (`[x]`)** | - |
| **P1** | **백업/PITR** | **Point-in-Time Recovery 시 DDL 실행 실패 에러 침묵(`_ =`)으로 인한 복구 실패 은폐 해결 (`BUG-PITR-DDL-SILENT-SWALLOW-1`)** | `pkg/backup/pitr.go` | **완료 (`[x]`)** | DDL 실행 실패 시 _= 침묵 제거 및 명시적 에러 반환/롤백 연동 |
| **P1** | **스토리지/식별자** | **스토리지 엔진 테이블명 대소문자 미구분 맵 조회 실패 및 중복 테이블 생성 결함 해결 (`BUG-STORAGE-TABLE-CASE-SENSITIVITY-1`)** | `pkg/storage/memengine/ddl.go`, `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | CreateTable/GetTable/DropTable MapGetFold 기반 대소문자 무관 룩업 및 중복 방지 |
| **P1** | **실행/집계** | **`AggregateIterator` 정규식의 DISTINCT 비캡처 폐기로 인한 중복 행 무차별 집계 결함 해결 (`BUG-EXEC-AGG-DISTINCT-IGNORED-1`)** | `pkg/execution/aggregate.go` | **완료 (`[x]`)** | - |
| **P1** | **엔진/집계** | **코디네이터 집계 `MIN`/`MAX` 비숫자형(문자열/날짜/불리언) 파싱 실패 시 NULL 반환 결함 해결 (`BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1`)** | `pkg/engine/sqlexec_select.go` | **완료 (`[x]`)** | - |
| **P1** | **와이어/PGWire** | **PGWire 사전 컴파일 바이트코드 실행 시 신규 독립 세션 생성으로 인한 트랜잭션 우회 해결 (`BUG-PGWIRE-PREP-EXEC-TX-BYPASS-1`)** | `pkg/wire/server.go` | **완료 (`[x]`)** | sessID 바인딩 및 코디네이터 세션 트랜잭션 문맥 공유로 트랜잭션 격리 보장 |
| **P1** | **와이어/PGWire** | **PGWire 파라미터 치환 루프 재귀 치환 왜곡 및 숫자형 문자열 따옴표 탈락 결함 해결 (`BUG-WIRE-PG-PARAM-SUBSTITUTION-INJECTION-1`)** | `pkg/wire/server.go` | **완료 (`[x]`)** | pgParamRe 일괄 치환 및 formatParamString 타입별 안전 이스케이프 완비 |
| **P1** | **카탈로그/영속화** | **서버 재기동 시 인메모리 프로시저/함수/뷰/트리거 메타데이터 영구 유실 결함 해결 (`BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`)** | `pkg/catalog/catalog.go`, `pkg/engine/sqlexec_ddl.go` | **완료 (`[x]`)** | catalog_objects.json 원자적 저장 및 재기동 시 프로시저/함수/뷰/트리거 자동 복원 |
| **P1** | **스토리지/WiredTiger** | **WiredTiger 스토리지 엔진 보조 유니크 인덱스 제약 검증 누락 및 순차 스캔 결함 해결 (`BUG-WT-UNIQUE-INDEX-UNENFORCED-1`)** | `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | - |
| **P1** | **실행/조인** | **복합 조인 조건 '=' 분해 파괴로 인한 빈 문자열 해시 매칭 및 무제한 카테시안 곱 결함 해결 (`BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1`)** | `pkg/execution/builder.go` | **완료 (`[x]`)** | - |
| **P1** | **스토리지/임시테이블** | **세션 임시테이블 스냅샷/WAL 영속화 및 재기동 후 영구 고아 객체 누수 결함 해결 (`BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1`)** | `pkg/storage/memengine/persistence.go`, `pkg/storage/wiredtiger/wt_engine.go` | **완료 (`[x]`)** | `storage.IsTemporaryTable` 도입으로 스냅샷 저장 및 WAL 기록/재생 시 임시테이블(`__temp_*`, `__tv_*`) 영속화 원천 차단 |
| **P1** | **엔진/MERGE** | **MERGE 구문 내 `WHEN NOT MATCHED` 신규 삽입 행이 `NOT MATCHED BY SOURCE`에 의해 즉각 자가 삭제되는 결함 해결 (`BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`)** | `pkg/engine/sqlexec_merge.go` | **완료 (`[x]`)** | NOT MATCHED BY SOURCE 적용 범위를 MERGE 시작 전 기존 행으로 한정하여 신규 삽입 행 자가 삭제 방지 |
| **P1** | **함수/문자열** | **`CHARINDEX` 및 `STRPOS`의 NULL 인자 수신 시 0 반환 삼치논리 파괴 결함 해결 (`BUG-STR-CHARINDEX-NULL-ZERO-1`)** | `pkg/proc/builtins_string.go` | **완료 (`[x]`)** | - |
| **P1** | **플래너/CBO** | **CBO 선택도 추정 시 컬럼명 대소문자 불일치로 인한 통계 탐색 실패 및 0.33 추락 결함 해결 (`BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1`)** | `pkg/planner/stats.go` | **완료 (`[x]`)** | - |
| **P1** | **플래너/비상관화** | **OR 조건 분기 내 서브쿼리 불법 SEMI 조인 변환 및 `removeClause`의 식별자 절단 결함 해결 (`BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1`)** | `pkg/planner/decorrelate.go` | **완료 (`[x]`)** | - |
| **P1** | **백업/복원** | **백업 아카이브 생성 및 복원 시 뷰(View) 메타데이터 전무 누락으로 인한 영구 유실 결함 해결 (`BUG-BACKUP-RESTORE-VIEW-OMISSION-1`)** | `pkg/backup/backup.go`, `pkg/backup/restore.go` | **완료 (`[x]`)** | 백업/복원 시 catalog/views.json 직렬화 및 ViewsCount/ViewsRestored 추적 |

---

## 4. 연계 문서 및 진실 공급원 (SSOT)

- **활성 엔지니어링 백로그**: [`TODO.ko.md`](TODO.ko.md)
- **코드 리뷰 및 결함 감사 트래커**: [`CODE_REVIEW.ko.md`](CODE_REVIEW.ko.md)
- **아키텍처 추상화 및 리팩토링 명세**: [`REFACTORING.ko.md`](REFACTORING.ko.md)
- **전체 식별 코드 분류 체계**: [`notes/TAXONOMY.ko.md`](notes/TAXONOMY.ko.md)
- **중장기 거시 릴리즈 로드맵**: [`ROADMAP.md`](ROADMAP.md)

