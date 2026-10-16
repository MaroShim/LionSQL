# LionSQL Engineering TODO & Backlog

이 문서는 LionSQL의 단기 엔지니어링 우선순위 과제, 도메인별 기능 백로그, 최근 완료된 마일스톤 이력을 관리하는 공식 엔지니어링 백로그입니다. 중장기 아키텍처 로드맵은 [ROADMAP.md](ROADMAP.md)를 참조하세요.

---

## 식별 코드 체계 범례 (Taxonomy Legend)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 부여 규칙 및 범위 | 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 (P1: 즉시 착수/완료, P2: 차기 착수, P3: 생태계 검증) |
| **`P-1` ~ `P-10`** | **엔진 성능 (Performance)** | `P-1` ~ `P-10` | 스토리지, 디코딩, 와이어, 캐시, 정규식, JSON, 병렬 쿼리 전반의 10대 핵심 성능 극대화 트랙 |
| **`R-1` ~ `R-5`** | **코드 리팩토링 (Refactoring)** | `R-1` ~ `R-5` | 거대 파일 모듈 분해, 단일 책임 원칙(SRP), DRY 중복 해소 및 가상 뷰 레지스트리화 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (A: 통계 수집, B: 조인 알고리즘, C: 선택도, D: 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |

> [!NOTE]
> 전체 식별 코드 일람 및 상세 명세는 [notes/TAXONOMY.md](notes/TAXONOMY.md)를 참조하세요.

---

## 1. 차기 착수 우선순위 과제 (Next High Priorities)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 기대 효과 및 구현 요약 |
| :---: | :---: | :--- | :--- | :--- |
| **P1** | **성능/DML (`P-13`)** | **DML `storage.Record`(Map) 제거 ➔ `storage.Row`(Slice) 직결 (Zero-Alloc DML)** | `pkg/proc/runtime`, `pkg/engine`, `pkg/storage` | VDBE 및 DML에서 중간 맵 생성과 해시 룩업을 제거하고 `Row` 슬라이스 직결로 DML 처리량 20~30% 향상 |
| **P3** | **생태계/드라이버** | **ODBC / JDBC 엔터프라이즈 드라이버 적합성 인증 확대** | `pkg/wire` | 엔터프라이즈 BI 도구 및 레거시 드라이버 연결 안정화 |

### P1/P2 최근 완료 과제: 고가용성, 성능 & 리팩토링
- [x] **TDS 단일 프레임 버퍼 풀링 & 네트워크 시스템 콜 50% 삭감 (`pkg/wire/tds`)**
  - **배경**: 기존 `WritePacket`이 8바이트 헤더와 페이로드를 별도 호출(`w.Write(hdr)` 후 `w.Write(payload)`)하여 패킷당 2회의 OS 시스템 콜(`write/sendto`) 및 불필요한 유저-커널 컨텍스트 스위칭 오버헤드가 발생함.
  - **구현 내용**:
    - `pkg/wire/tds/packet.go`: `sync.Pool` 기반 4,096바이트 고정 프레임 풀(`tdsFramePool`) 구축.
    - 8바이트 헤더 직렬화와 청크 페이로드를 단일 연속 프레임(`frame[:packetLen]`)에 인메모리 패킹하여 패킷당 단 1회의 `w.Write()`로 원자적 전송.
    - 30여 개 호출부 수정 없이 시스템 콜 횟수 50% 즉시 삭감 및 힙 할당 0건(Zero-Allocation) 달성.
    - `pkg/wire/tds/server_test.go`: `TestTDS_WritePacket_SingleSyscallPerPacket` 단위 검증 및 전체 레이스 프리 테스트 100% PASS 완료.
- [x] **Volcano 벡터화 배치 청크(`Chunk`) 풀링 (`P-12`) (`pkg/execution`)**
  - **배경**: 1,024건 단위 튜플 배치 스트리밍(`Chunk`) 간 연산자(`Filter`, `Projection`, `Sort`, `HashJoin`, `Aggregate`) 경계 전송 및 버퍼링 시 `make([]Tuple, 0, 1024)` 슬라이스가 반복 동적 생성되어 GC에 버려지는 오버헤드 제거.
  - **구현 내용**:
    - `pkg/execution/chunk.go`: `sync.Pool` 기반 `chunkPool` (`GetChunk`, `PutChunk`, `AcquireChunk`, `ReleaseChunk`) 구축.
    - 튜플 슬라이스 반환 시 내부 참조(`Tuple.Values`, `Lazy`, `Index`)를 `Tuple{}`로 초기화하여 메모리 누수 방지 및 4배 초과 비정상 확장 청크 비반환 상한 가드(Rule 23).
    - `aggregate.go`, `join.go`, `sort.go`, `sqlexec_select.go` 전반에 청크 대여 및 `defer ReleaseChunk` 반환 적용.
    - **실측 검증**: TPC-C VDBE 및 대규모 튜플 스트리밍 시 슬라이스 할당 제거로 WiredTiger **16,814.17 TPS** (457,021 tpmC, +13.2% 향상) 및 인메모리 **20,087.70 TPS** 달성 기여.
- [x] **In-Memory OCC 트랜잭션 객체 & 워크스페이스 맵 풀링 (`P-11`) (`pkg/storage/memengine`)**
  - **배경**: `sess.BeginTx()` 호출 시마다 `Transaction` 구조체와 6개의 Go 맵(`tableCache`, `baseVers`, `local`, `deleted`, `changes`, `savepoints`)을 힙에 새로 할당(`make(map...)`)하고 커밋/롤백 시 GC에 버리는 오버헤드(초당 16,000~20,000 TPS 환경에서 초당 10만+ 맵 객체 생성) 제거.
  - **구현 내용**:
    - `pkg/storage/memengine/tx.go`: `sync.Pool` 기반 `txPool` 구축.
    - 트랜잭션 종료(`Commit()` 또는 `Rollback()`) 시 `defer tx.releaseToPoolLocked()`를 통해 Go 1.21+ 내장 `clear(m)` 함수로 맵의 내부 해시 버킷 용량을 유지한 채 $O(1)$ 초기화 후 풀에 반환.
    - 대량 변경 트랜잭션(1,024건 초과)의 경우 거대 버킷 배열 영구 점유 방지를 위해 적응형 재할당 가드 적용(Rule 23).
    - 트랜잭션 시작 시 힙 메모리 할당 0건(Zero-Allocation Transaction) 달성.
    - **실측 검증**: TPC-C VDBE 벤치마크(Scale 10, 20 Workers)에서 **20,087.70 TPS** (541,708 tpmC), 직렬화 어보트율 **0.10%**로 20,000 TPS 선 공식 돌파 검증 완료.
- [x] **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 경량 키 스캐너 (`P-7`) (`pkg/storage`, `pkg/engine`)**
  - **배경**: `ExtractJSON`, `ExtractJSONText`, `JSONValueByPath` 호출 시 매번 Go 표준 라이브러리의 `json.Unmarshal`을 호출하여 `map[string]any`를 힙에 생성하는 오버헤드 및 단일 튜플 내 다중 JSON 필드 참조 시 중복 파싱 제거.
  - **구현 내용**:
    - `pkg/storage/json.go`: 최상위 키 추출 전용 제로-할당 경량 스트리밍 바이트 스캐너(`ScanJSONTopLevelKey`) 구현. 단일 키 추출(`doc->>'city'`, `JSON_VALUE(doc, '$.city')`) 시 전체 트리 언마샬링 없이 서브마이크로초 즉시 반환.
    - 용량 1,024건 상한 FIFO 퇴출 기반 스레드 안전 DOM 캐시(`jsonDOMCache`, `globalDOMCache`) 구축: 한 행에서 여러 복합 필드를 추출할 때 1회만 언마샬링하여 DOM 재사용.
    - `ExtractJSON`, `ExtractJSONText`, `JSONValueByPath`, `JSONQueryByPath`, `navigatePath` 전반에 제로-할당 스캐너 및 캐시 결합 완료.
- [x] **프로덕션 스토리지 파일시스템 암호화 표준 가이드 수립 & TDE 보류 (`doc/STORAGE_ENCRYPTION_GUIDE.md`, `doc/SECURITY.md`)**
  - **배경**: 인-커널 소프트웨어 레벨 TDE(Transparent Data Encryption)는 클라우드 블록 스토리지나 엔터프라이즈 파일시스템과 중복 암호화되어 심각한 NVMe I/O 처리량 저하 및 CPU 암호화 부하를 유발함.
  - **결정 및 구현**:
    - **TDE 보류 및 플랫폼 프리미티브 위임 (Golden Rule 14, 15)**: 데이터베이스 프로세스 내부 소프트웨어 암호화 대신 OS 커널(`dm-crypt`/LUKS2 with AES-NI) 및 클라우드 KMS 블록 볼륨 암호화(AWS EBS, GCP CMEK, Azure Managed Disk)를 공식 표준으로 채택.
    - **프로덕션 암호화 가이드 수립 (`doc/STORAGE_ENCRYPTION_GUIDE.md`)**:
      - Linux 온프레미스/베어메탈 LUKS2 파티셔닝, ext4/XFS 포맷, NVMe 작업 큐 바이패스 성능 튜닝(`--perf-no_read_workqueue --perf-no_write_workqueue`).
      - AWS EBS / GCP PD / Azure Managed Disk 암호화 StorageClass 매니페스트.
      - 컨테이너/쿠버네티스 읽기 전용 루트 및 암호화 마운트 바운더리 격리 가이드.
      - 백업 아카이브 OpenSSL AES-256-GCM 오프사이트 전송 암호화 절차.
      - PCI-DSS v4.0, HIPAA, SOC 2, GDPR 규제 준수 매핑 완비.
- [x] **가상 카탈로그 멀티테이블 조인 인터셉터 격리 리팩토링 (`pkg/catalog/virtual`, `pkg/engine`)**
  - **배경**: `pkg/engine/sqlexec_gui_interop.go`와 `sqlexec_mssql_sys.go`에 분산되어 있던 DBeaver, DbGate, Beekeeper Studio 등 GUI 도구용 메타데이터 크로스 조인 쿼리 가로채기(PostgreSQL `pg_class JOIN pg_namespace`, `pg_attribute JOIN pg_class`, `pg_constraint`, `pg_proc`, MSSQL `sys.columns JOIN sys.types`, `sys.indexes`, `sys.partitions`) 레이어를 독립 패키지인 `pkg/catalog/virtual`로 분리하여 엔진과의 결합도를 낮추고 유지보수성을 극대화.
  - **구현 내용**:
    - **독립 패키지 신설 (`pkg/catalog/virtual`)**:
      - `source.go`: 메타데이터 원천 인터페이스 `Source` (`ListTables`, `GetTable`, `ListProcedures`, `ListFunctions`, `ListViews`, `GetView`, `GetSortedTables`, `GetSortedViews`, `GetSortedProcedures`) 및 `Result` 구조체 정의.
      - `result.go`: 가상 카탈로그 행 필터링, 정렬, 페이징, 프로젝션 평가기 `FormatResult` 및 정규식 최적화.
      - `pg.go`: PostgreSQL 메타데이터 가상 조인 핸들러 (`execClassSelect`, `execColumnMetadataSelect`, `execPrimaryKeySelect`, `execIndexSelect`, `execForeignKeySelect`, `execTriggerSelect`, `execProcSelect`, `execBeekeeperTableSelect`, `execPgShdescriptionSelect`, `execPgConversionSelect`).
      - `mssql.go`: SQL Server / TDS 메타데이터 가상 조인 핸들러 (`execBeekeeperMSSQLColumnSelect`, `execDbGateMSSQLColumnSelect`, `execDbGateMSSQLPrimaryKeySelect`, `execDbGateMSSQLIndexSelect`, `execDbGateMSSQLIndexColumnSelect`, `execDbGateMSSQLForeignKeySelect`, `execDbGateMSSQLRowCountSelect`, `execDbGateMSSQLColumnCommentSelect`, `execDbGateMSSQLParamSelect`, `execDbGateMSSQLTriggerSelect`, `execDbGateMSSQLObjectSelect`, `execDbGateMSSQLTableSelect`, `execDbGateMSSQLViewSelect`, `execDbGateMSSQLProgrammablesSelect`, `execDbGateMSSQLLoadSqlCodeSelect`, `execDBeaverMSSQLAllObjectsSelect`).
      - `interceptor.go`: `IsVirtualCatalogJoinQuery(sql string) bool` 판별기 및 `Execute(src Source, sql string) (*Result, error)` 중앙 라우팅 디스패처.
      - `interceptor_test.go`: 가상 카탈로그 단위 테스트 완비.
    - **엔진 연동 및 코드 다이어트 (`pkg/engine`)**:
      - `sqlexec_virtual.go`: `*Coordinator`가 `virtual.Source` 및 `GetTableRowCount(string) int64`를 충족하도록 구현하고 `execVirtualCatalogJoinSelect`로 단일화.
      - `sqlexec.go`, `sqlexec_select.go`: `virtual.IsVirtualCatalogJoinQuery` 및 `s.execVirtualCatalogJoinSelect` 호출로 전환.
      - 비대했던 `sqlexec_gui_interop.go`와 `sqlexec_mssql_sys.go` 중복 코드 1,300여 라인 완전 제거.
      - 레거시 HeidiSQL 명칭을 표준적이고 중립적인 `VirtualCatalog`로 통일.
    - 전체 패키지 빌드 및 race-free 테스트 100% PASS 검증 완료.
- [x] **하드코딩 Go 시딩 제거 및 5개 테이블 기반 독립 데모 스크립트(`scripts/demo_seed.sql`) & `--seed=false` 기본값 전환 (`cmd/server`, `scripts/`)**
  - **배경**: 서버 바이너리에 Go 구조체로 하드코딩되어 있던 샘플 데이터 시딩 로직(`seedSampleAccounts`, `seedSampleProcedure`)을 제거하고, 실무 및 교육용으로 널리 쓰이는 표준 5개 테이블 세트와 샘플 Stored Procedure를 포함한 독립 SQL 스크립트로 분리하여 재사용성과 유지보수성을 극대화.
  - **구현 내용**:
    - **5개 표준 데모 테이블 완비 (`scripts/demo_seed.sql`)**: `customers`(고객 인적사항), `accounts`(고객 계좌 및 잔액), `products`(판매 상품 카탈로그), `sales`(주문 마스터), `sales_details`(주문 상세) 및 샘플 데이터 레코드(각 5~10건) 등록.
    - **샘플 Stored Procedure 2종 완비**: 금융 계좌 이체 프로시저(`TransferFunds`) 및 전자상거래 주문 처리/재고 차감 프로시저(`ProcessOrder`).
    - **`--seed=false` 기본값 전환**: 운영 환경 안정성을 위해 자동 시딩 기본값을 `false`로 전환(`pkg/config/config.go`, `cmd/server/main.go`).
    - **단일 원천 스크립트 분리 (`scripts/demo_seed.sql`)**: 불필요한 바이너리 임베딩 중복 및 패키지 레이어 없이 `scripts/demo_seed.sql` 단일 원천 파일 유지 및 `--seed` 지정 시 파일 직접 로딩(KISS & DRY).
    - **SQL 스크립트 분할기 및 프로시저 파서 개선**: 주석 무시(`StripLeadingComments`), `BEGIN TRANSACTION` 구문의 블록 깊이 오인식 방지, `DECIMAL(15, 2)` 등 다중 인자 타입 한정자 파싱 지원, `EXEC` 명명된 파라미터(`@Name = Val`) 및 위치 파라미터 동시 지원.
- [x] **PGWire, TDS, HTTP 전반의 통합 관리자 인증 및 AuthProvider SPI 추상화 (`pkg/auth`, `pkg/wire`, `pkg/wire/tds`)**
  - **배경**: HTTP(`admin@sqlion.local`), PostgreSQL(`postgres`), SQL Server(`sa`) 간 관리자 비밀번호가 분산되지 않고 단일 원천(SSOT)으로 동기화 관리되도록 개선하고, 향후 LDAP 연동을 위한 인터페이스 추상화 완비.
  - **구현 내용**:
    - `AuthService.defaultAdminEmail` 단일 원천 관리자 해시 보관 및 `AuthenticateLogin`에서 내장 관리자(`sa`, `postgres`, `admin`)의 단일 비밀번호 자동 검증.
    - `ALTER LOGIN sa WITH PASSWORD = '...'` 실행 시 HTTP 슈퍼유저 비밀번호도 실시간 동기화.
    - `pkg/wire` 및 `pkg/wire/tds`: `--auth=false` 시 신뢰(Trust) 무인증 접속 허용, `--auth=true` 시 단일 관리자 비밀번호 엄격 검증 및 실패 시 표준 에러(TDS Error 18456, PGWire 28P01) 반환.
    - `AuthProvider` SPI 인터페이스(`pkg/auth/provider.go`) 신설 및 `RegisterProvider` 외부 공급자 체인 조회 구조 완비.
    - 단위 및 동시성 경쟁 테스트 100% PASS 검증 완료.
- [x] **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 리팩토링 (`P-6`) (`pkg/engine`)**
  - **배경**: `sqlexec.go`, `sqlexec_dml.go`, `sqlexec_pivot.go`, `sqlexec_apply.go`, `sqlexec_derived.go`, `sqlexec_eval.go`, `sqlexec_explain.go`, `sqlexec_mssql_sys.go`, `sqlexec_gui_interop.go`, `sqlexec_string_split.go`, `sqlexec_procs.go` 등 핫패스 쿼리 라우팅 및 행 루프 내부에 분산되어 있던 40여 개의 동적 `regexp.MustCompile` 호출로 인한 매 쿼리당 DFA 재컴파일 지연(5~15µs) 및 힙 할당 병목 해소.
  - **구현 내용**:
    - **패키지 레벨 싱글톤 승격**: 정적 정규식 패턴 전체를 패키지 로드 시 1회 컴파일되는 `var reXxx = regexp.MustCompile(...)` 싱글톤으로 승격.
    - **동적 파라미터 정규식 캐싱 (`getCompiledRegexp`)**: `CROSS/OUTER APPLY`, 뷰 치환, CTE 재작성, 프로시저 파라미터 치환 등 동적 식별자가 결합되는 정규식에 대해 `sync.Map` 기반 스레드 안전 정규식 캐시(`rePatternCache`)를 적용하여 0 allocation 재사용 보장.
    - **행 루프/평가 핫패스 컴파일 제거**: `evalSimpleRowWhere`, `evaluateSingleConditionMap`, `evalUpdateExpr` 등 매 레코드마다 호출되던 정규식 동적 생성을 100% 제거.
- [x] **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`P-8`) (`pkg/storage/wiredtiger`)**
  - **배경**: `tx.Get`, `tx.Scan`, `tx.IndexScan`, `Insert`, `Update`, `Delete` 호출 시마다 CGo를 통한 `go_wt_open_cursor` 및 `cur->close` 반복 호출 오버헤드, 매 쿼리당 C `malloc`/`free`(`C.CString`), 그리고 단일 포인트 룩업당 6회의 CGo 경계 왕복 병목 해소.
  - **구현 내용**:
    - **세션 레벨 커서 풀링(`WTSession`)**: 테이블 URI(`table:name`)별로 유휴 커서 슬라이스(`cursorPool map[string][]*C.WT_CURSOR`, 최대 16개 한도)를 보관하여 `acquireCursor`/`releaseCursor`를 통해 닫지 않고 `C.go_wt_cursor_reset`으로 초기화 후 즉각 재사용.
    - **단일 CGo 왕복 통합 헬퍼(`go_wt_cursor_get`, `go_wt_cursor_insert_raw`, `go_wt_cursor_update_raw`, `go_wt_cursor_remove_key`)**: C preamble 내에서 `set_key`, `search`/`operation`, `get_val_raw`를 단 1회의 CGo 호출로 통합 실행하여 CGo 호출 횟수를 1/3~1/6로 감축.
    - **제로-할당 C 문자열 변환(`fastCString`)**: 256바이트 스택 버퍼(`[256]byte`) 기반 널 종료자 주입을 통해 일반 PK 조회 시 C `malloc`/`free` 호출을 100% 제거.
    - **성능 검증**: 단건 PK 룩업(`tx.Get`) 처리량 894ns/op (초당 1,130,000+ OPS), 100건 스캔 및 튜플 디코딩 27µs/op (초당 36,000+ Table Scans), 멀티스레드 동시성 및 중첩 스캔 무결점 검증(0 Data Race).
- [x] **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 로직 일원화 (`R-3`) (`pkg/planner`, `pkg/execution`, `pkg/engine`)**
  - **배경**: `sqlexec_eval.go`의 단일 테이블 고속 검색과 `execution/builder.go`의 Volcano 플랜 빌더 간에 인덱스 범위 검색 경계(`lowVal`, `highVal`) 추출 및 조건식 파싱 로직이 중복 존재(DRY 위반)하고, Volcano 인덱스 스캔에서 범위 조건(`BETWEEN`, `>=`, `<=`) 처리가 누락되어 있었음.
  - **구현 내용**:
    - `pkg/planner/bounds.go` 공통 모듈 신설:
      - `ParseWhereConditions`: 등호(`=`), `BETWEEN ... AND ...`, 부등호(`>=`, `>`, `<=`, `<`) 및 문자열/괄호 중첩을 고려한 $O(N)$ 단일 패스 파서 및 정규식 패키지 변수 사전 컴파일 적용.
      - `ParseIndexCond`: 단일/복합 인덱스 조건식 및 반정형 JSON 기능 표현식(`doc->>'key' = 'val'`) 바운드 추출 일원화.
      - `ParseValueToType`: `TypeInt`, `TypeFloat`, `TypeDecimal`, `TypeBool`, `TypeDateTime`, `TypeDate`, `TypeTime`, `TypeUUID`, `TypeBinary` 전 데이터 타입 파싱 통합.
      - `BuildCompositePrefixKey`: 복합 인덱스 `\x00` 구분자 접두사 키 생성 엔진 공통화.
      - `FindIndexAndBounds`: 복합 인덱스, 단일 컬럼 인덱스(등치/범위), 함수적 표현식 인덱스 매칭 일원화.
    - `pkg/execution/builder.go`: 중복 함수(`parseIndexCond`, `parseValueByType`, 미사용 `regexp`/`proc` 임포트) 완전 제거 및 `planner.ParseIndexCond` 호출로 단일화 (Volcano 엔진의 범위 인덱스 스캔 완벽 지원).
    - `pkg/engine/sqlexec_eval.go`: 중복 구현된 260줄 이상의 바운드 파싱 로직을 제거하고 `planner.Bounds` 위임으로 슬림화.
    - 단위 테스트 `pkg/planner/bounds_test.go` 완비 및 전체 25개 패키지 race-free 테스트 100% PASS 검증 완료.
- [x] **Pratt 파서 및 내장 함수 도메인별 관심사 분리 (`R-2`) (`pkg/proc`)**
  - **배경**: `pkg/proc/parser.go`(3,241라인)와 `pkg/proc/builtins.go`(2,880라인)가 단일 파일에 비대하게 집중되어 신규 문법/함수 추가 시 인지 부하 및 머지 충돌 위험 증가.
  - **구현 내용**:
    - **파서 모듈화 (3,241줄 ➔ 5개 도메인 파일 분해)**:
      - `parser.go` (280줄, 90%+ 슬림화): 코어 구조체, NewParser, 렉서 탐색 및 공통 어휘 헬퍼.
      - `parser_ddl.go` (599줄): 프로시저, 함수, 트리거, 테이블, 인덱스 생성/삭제 DDL.
      - `parser_dml.go` (1,141줄): `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH`(CTE), `EXPLAIN`.
      - `parser_stmt.go` (656줄): `IF/WHILE/TRY...CATCH/BEGIN...END`, 트랜잭션, 커서, `EXEC`.
      - `parser_expr.go` (615줄): Pratt 우선순위 식 파싱, 함수 호출, CASE WHEN, JSON 표현식 분석.
    - **내장 함수 모듈화 (2,880줄 ➔ 6개 도메인 파일 분해)**:
      - `builtins.go` (153줄, 94%+ 슬림화): 스칼라 함수 SPI 등록 및 도메인 디스패처, 공통 변환 헬퍼.
      - `builtins_date.go` (765줄): 날짜/시간 함수 및 파싱/버킷 헬퍼.
      - `builtins_string.go` (885줄): 문자열 조작, PATINDEX, SOUNDEX, 포맷팅 헬퍼.
      - `builtins_math.go` (352줄): 수학, 삼각, 비트 연산 함수.
      - `builtins_json.go` (179줄): JSON 내장 함수 및 경로 탐색/수정 헬퍼.
      - `builtins_sys.go` (697줄): 시스템, 메타데이터, UUIDv7, 에러, 조건 논리(ISNULL/COALESCE), 암호화 함수.
    - 전체 25개 패키지 race-free 테스트 100% PASS 검증 완료.
- [x] **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화 (`R-1`) (`pkg/engine`)**
  - **배경**: 단일 파일(3,283라인)에 PostgreSQL `pg_catalog`, ANSI `information_schema`, MSSQL `sys.tables`/DMV, DBeaver/Beekeeper/DbGate/HeidiSQL 도구별 쿼리 인터셉터가 혼재되어 거대한 switch 분기문과 높은 유지보수 비용을 초래함.
  - **구현 내용**:
    - 도메인별 4개 모듈 분리 완료: `sqlexec_pgcatalog.go`(634줄), `sqlexec_info_schema.go`(420줄), `sqlexec_mssql_sys.go`(554줄), `sqlexec_gui_interop.go`(477줄).
    - `VirtualCatalogView` 함수 시그니처 및 `registerCatalogView` 레지스트리 패턴 도입: 거대한 switch 분기를 15줄 수준의 $O(1)$ 맵 디스패치(`execPgCatalogSelect`) 및 $O(1)$ 테이블 식별(`isPgCatalogTable`)로 슬림화.
    - 전체 25개 패키지 레이스 감지 테스트 100% PASS 검증 완료.
- [x] **반정형 JSON 표현식 B-Tree 인덱스 & 내장함수 완비 (`ADR-022`) (`pkg/proc`, `pkg/storage`, `pkg/planner`, `pkg/execution`, `pkg/engine`)**
  - **배경**: PostgreSQL JSON 이항 연산자(`->`, `->>`) 및 T-SQL/ANSI JSON 표준 함수(`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`) 지원, 기능 표현식 인덱스(`((data->>'key'))`)를 통한 반정형 도큐먼트 쿼리의 초고속 B-Tree 인덱스 스캔 가속.
  - **구현 내용**:
    - JSON 내장 함수 및 Pratt 파서 연산자(`->`, `->>`) 완비.
    - 기능 표현식 인덱스 DDL(`CREATE [UNIQUE] INDEX ON tbl ((expr))`) 파싱 및 AST 정규화.
    - 스토리지 튜플 삽입/갱신/삭제 시 sub-microsecond JSON 키 추출(`storage.ExtractJSONIndexKey`), SkipList 및 WiredTiger B+Tree 실시간 동기화.
    - ANSI SQL UNIQUE 제약 조건 준수: NULL 다중 허용(`!key.IsNull`) 및 중복 비-NULL 키 삽입 시 원자적 롤백(`ErrUniqueViolation`).
    - CBO 옵티마이저 매칭(`pkg/planner`) 및 실행기 바인딩(`pkg/execution`): `WHERE data->>'city' = 'Seoul'` 또는 `JSON_VALUE(data, '$.city') = 'Seoul'` 평가 시 CBO가 `Index Scan` 자동 선택.
    - 1,000건 JSON 레코드 대상 SeqScan(2.17ms) 대비 IndexScan(334µs)으로 7배+ 성능 가속 검증 완료.
- [x] **T-SQL `STRING_SPLIT` 테이블 반환 함수 & 서브쿼리/CROSS APPLY 완비 (`pkg/engine`)**
  - **배경**: 모던 SQL Server 및 T-SQL 저장 프로시저의 필수 TVF인 `STRING_SPLIT(string, separator [, enable_ordinal])` 지원을 통해 `WHERE id IN (SELECT value FROM STRING_SPLIT(@ids, ','))` 및 동적 태그 분해 `CROSS APPLY STRING_SPLIT(col, ',')`의 100% 호환성 확보.
  - **구현 내용**:
    - `pkg/engine/sqlexec_string_split.go`: `STRING_SPLIT(string, separator [, enable_ordinal])` 파싱 및 레코드셋 생성기 구현. 단일 쿼리(`SELECT value, ordinal FROM STRING_SPLIT(...)`), 테이블 별칭(`AS s`), 컬럼 별칭, `WHERE` 필터링, `ORDER BY`, `DISTINCT`, 집계 `COUNT(*)`, 페이징(`LIMIT`/`OFFSET` 및 T-SQL `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY`) 완비.
    - 세션 변수 연동: `@tags`, `@ids` 등 세션 변수를 즉각 해결하여 프로시저 및 매개변수화 쿼리와 매끄럽게 결합.
    - 서브쿼리 해결 (`pkg/engine/sqlexec_explain.go`): `resolveSubqueriesWithSession`을 통해 `WHERE id IN (SELECT value FROM STRING_SPLIT(...))` 구문을 세션 컨텍스트 하에서 리터럴 리스트로 원자적 치환 후 초고속 엔진 경로 실행.
    - `CROSS APPLY` 및 `OUTER APPLY` 연동: 행별 컬럼 데이터를 매개변수화하여 다중 행 조인 스트림 자동 생성.
    - 단위 및 E2E 테스트 완비 (`pkg/engine/sqlexec_string_split_test.go`).
- [x] **Transactional Outbox 브로커 디스패처 (Kafka/RabbitMQ/Webhook) (`pkg/cdc`, `pkg/config`, `pkg/api`, `cmd/server`)**
  - **배경**: 분산 마이크로서비스 환경에서 듀얼 라이트(Dual-Write) 문제 해결 및 데이터베이스 커밋과 100% 원자적으로 연동되는 무손실 메시지 브로커 발행(At-Least-Once Delivery).
  - **구현 내용**:
    - `pkg/cdc/outbox.go`: 트랜잭션 커밋 CDC 스트림 구독 및 `OutboxMessage` 정규화 디스패처 구현. 일반 테이블 변경 CDC 스트리밍(`sqlion.<table_name>`)과 전용 아웃박스 테이블(`outbox` / `_outbox`) 스키마(`topic`, `payload`, `key`, `headers`) 자동 해석 지원.
    - 무손실 체크포인트 영속화: 원자적 스왑 파일(`outbox_checkpoint.json`, Golden Rules 1 & 5) 기반으로 마지막 처리 LSN(`last_ack_lsn`)을 기록하여 재기동 시 유실 없이 자동 재개.
    - 지수 백오프 재시도: 브로커 일시 장애 시 최대 재시도 횟수(`MaxRetries`) 및 점진적 백오프 루프 관통.
    - 플러그형 브로커 싱크(`pkg/cdc/outbox_sinks.go`): `MemorySink`(테스트/임베디드), `WebhookSink`(HTTP POST), `KafkaRESTSink`(Confluent/Strimzi/Upstash), `RabbitMQHTTPSink`(Management REST API).
    - 운영 관측성 및 CLI 연동: `GET /api/cdc/outbox/status` 실시간 모니터링 엔드포인트 완비 및 CLI 플래그(`-outbox`, `-outbox-table`, `-outbox-webhook`, `-outbox-kafka`, `-outbox-rabbitmq`), JSON 설정 바인딩.
    - 단위 및 E2E 테스트 완비 (`pkg/cdc/outbox_test.go`, `pkg/api/outbox_api_test.go`).
- [x] **PostgreSQL 서버 사이드 커서 및 Extended Query 포털 배치 스트리밍 (`pkg/engine`, `pkg/wire`)**
  - **배경**: 대규모 데이터셋 조회 시 클라이언트 메모리 고갈 방지 및 PostgreSQL 표준 SQL 커서(`DECLARE ... CURSOR`, `FETCH`, `MOVE`, `CLOSE`) 및 드라이버 포털 배치(`Execute` with `maxRows`, `PortalSuspended`) 완비.
  - **구현 내용**:
    - `pkg/engine/sqlexec_cursor.go`: `DECLARE <name> [SCROLL] [BINARY] CURSOR [{WITH|WITHOUT} HOLD] FOR <query>` 스냅샷 커서 등록기 구현.
    - 커서 제어 명령 구현: `FETCH` 및 `MOVE` 지원 (`NEXT`, `PRIOR`, `FIRST`, `LAST`, `ALL`, `FORWARD`, `BACKWARD`, `ABSOLUTE <n>`, `RELATIVE <n>`, 정수 카운트).
    - 커서 해제: 단일 커서 `CLOSE <name>` 및 전체 커서 일괄 정리 `CLOSE ALL`.
    - 트랜잭션 수명주기 불변성: 일반 커서는 `COMMIT` 시 파기, `WITH HOLD` 커서는 `COMMIT` 후에도 유지, `ROLLBACK` 시 모든 커서 즉각 파기 (PostgreSQL 공식 표준).
    - Extended Query 프로토콜 연동 (`pkg/wire/server.go`): `Execute ('E')` 메시지의 `maxRows`를 파싱하여 배치 단위(`maxRows`)로 데이터 튜플을 스트리밍하고 미완료 시 `MsgPortalSuspended ('s')` 송출, 완료 시 `MsgCommandComplete ('C')` 송출.
    - 단위 및 통합 테스트 완비: `pkg/engine/sqlexec_cursor_test.go` 및 `pkg/wire/server_test.go`.
- [x] **관용적 No-Op `USE <database>` 및 세션 가상 DB 컨텍스트 동기화 (`pkg/engine`, `pkg/wire/tds`, `pkg/proc/runtime`)**
  - **배경**: 인스턴스 단위 격리(Instance-Based Isolation) 아키텍처 불변성을 엄격히 유지하면서도, 레거시 SQL Server 덤프 스크립트, 마이그레이션 도구, DBeaver/SSMS GUI 클라이언트와의 100% 호환성 확보.
  - **구현 내용**:
    - `pkg/engine/sqlexec.go`: `USE <database>` (`USE master`, `USE [SalesDB]`, `USE "Analytics_2026"`) 정규식 인터셉트를 통한 무오류 즉시 수용 및 `Command: "USE"` 응답.
    - 세션 격리형 가상 DB 추적: `ClientSession.Database` 및 `ClientSession.Vars["current_database"]` / `["db_name"]` 동기화 (기본 세션 `sqlion`, TDS 세션 `master`).
    - 스칼라 함수 일원화: `DB_NAME()`, `CURRENT_DATABASE()`, `DB_ID()`, `ORIGINAL_DB_NAME()` 호출 시 세션 활성 데이터베이스를 완벽 동기화 반환 (테이블 미참조 스칼라 SELECT 및 VDBE `OpCallBuiltin` 바인딩).
    - TDS 와이어 프로토콜 연동: `pkg/wire/tds/server.go`에서 `USE` 명령 실행 시 공식 MS-TDS `TokenEnvChange (0xE3)` (`EnvChangeDatabase`) 토큰 스트림을 클라이언트로 즉시 송출.
    - 회귀 테스트 완비: `pkg/engine/coordinator_test.go` (`TestTolerantUseDatabase`), `pkg/wire/tds/server_test.go` (`TestTDS_TolerantUseDatabase`).
- [x] **Always On 스타일 자동 장애 조치(Auto-Failover) 및 리더 선출 (`pkg/replication`, `cmd/server`, `pkg/config`, `pkg/ui`)**
  - **배경**: 엔터프라이즈 99.999% 무중단 운영을 위한 Primary 장애 자동 감지, 최저 지연 LSN 레플리카 무손실 승격 및 스플릿 브레인 방지.
  - **구현 내용**:
    - `pkg/replication/failover.go`: 단조 증가 리더 텀(`term int64`) 및 Always On 최신 LSN 우선 규칙(`req.LastLSN >= localLSN`) 기반 리더 선출 코디네이터 구현.
    - 스플릿 브레인 방지(펜싱): 기존 Primary가 상위 텀 발표(`POST /api/replication/leader`)나 투표 요청 감지 시 즉시 읽기 전용 레플리카로 자동 강등(`role = RoleReplica`, 쓰기 트랜잭션 차단).
    - Primary 하트비트 타임아웃 자동 감지(`ReplicationClient.LastHeartbeatTime()`) 및 정족수(Quorum) 기반 백그라운드 선출 루프 완비.
    - CLI 플래그 및 JSON 설정 연동: `-auto-failover`, `-peers`, `-self-url`, `-heartbeat-timeout-ms`, `-election-timeout-ms`.
    - 임베디드 웹 스튜디오(`pkg/ui/dashboard.html`) 실시간 리더 텀 및 고가용성 상태 시각화 연동.
    - 단위 및 E2E 통합 테스트 완비 (`pkg/replication/failover_test.go`): LSN 우선권 검증, 스플릿 브레인 방지, 정족수 투표, Primary 강제 다운 시 자동 승격 및 신규 Primary 쓰기 검증.
- [x] **선언적 스키마 마이그레이션 CLI (`lionsql migrate`) 및 `_migrations` 이력 추적 (`cmd/lionsql`, `pkg/migrate`, `cmd/server`)**
  - **배경**: PocketBase 스타일의 개발자 생산성 극대화 및 독립형 데이터베이스 스키마 버전 관리 완비.
  - **구현 내용**:
    - `_migrations` 내부 테이블 자동 생성 및 마이그레이션 메타데이터(`version`, `name`, `applied_at`, `duration_ms`) 이력 추적.
    - `pkg/migrate` 엔진 신설: `Create`, `Load`, `Up`, `Down`, `Status` 라이프사이클 및 단조 증가 타임스탬프 충돌 방지 완비.
    - `cmd/lionsql` 통합 CLI 바이너리 구축 (`bin/lionsql`): `lionsql migrate new <name>`, `lionsql migrate up/down/status`, 임베디드 직접 쿼리 실행기 `lionsql sql "<query>"`.
    - `cmd/server/main.go` 양방향 패리티 완비 (`./bin/server migrate ...`).
    - `pkg/migrate/migrate_test.go` 및 `cmd/lionsql/main_test.go` 단위/통합 테스트 완비.
- [x] **[제거 / Spec-Out] 내장형 OAuth2 소셜 로그인 배제 (`pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)**
  - **배경 및 결정 사유**: DB 커널 내부에서 구글/깃허브 웹 리다이렉트 및 아웃바운드 인터넷 토큰 교환을 처리하는 것은 단일 책임 원칙 및 KISS(Rule 15)를 위반하고 보안 공격 표면을 확대함. 1.0 릴리즈 안정화를 위해 스펙 아웃(Spec-Out) 결정 (`GEMINI.md` 섹션 8.6, `notes/ENGINEERING_CONTEXT.md` 섹션 1.12).
  - **제거 내용**:
    - `pkg/api/oauth.go`, `pkg/api/handlers_oauth.go`, `pkg/api/oauth_test.go` 파일 완전 삭제.
    - `pkg/api/server.go`: OAuth 매니저, 라우트(`/api/auth/oauth2/*`, `/api/settings/oauth2`) 및 핸들러 제거.
    - `pkg/auth/auth.go`: `ProvisionOAuthUser`, `GetUserByOAuth` 및 슈퍼유저 OAuth 필드 제거.
    - `pkg/config/config.go`: `OAuth2Config` 및 `SQLION_OAUTH_*` 환경 변수 파싱 제거.
    - `pkg/ui/dashboard.html`: 웹 스튜디오 로그인 팝업 내 OAuth 버튼 및 메시지 리스너 제거.
    - 전체 25개 패키지 race-free 테스트 100% PASS 검증 완료.
- [x] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`C-7`) (`pkg/planner`)**
  - **배경**: 범주형 상태 컬럼이나 특정 핫스팟 값에 데이터가 집중되는 왜곡(Skew) 데이터 환경에서 균등 분포 가정(1/NDV)으로 인해 발생하는 선택도 최대 10~100배 오차 제거.
  - **구현 내용**:
    - `MCVItem` 모델 및 컬럼 통계 내 `MCVList` 추출 엔진 구현 (`pkg/planner/stats.go`).
    - `BuildColumnStats`에서 상위 최빈값(Top-20, 발생 빈도 >= 1% 및 평균 빈도 1.2배 초과) 자동 탐지 및 MCV 목록 분리.
    - 동치(`=`), 부등치(`!=`), `IN`, `BETWEEN`, 범위(`<`, `<=`, `>`, `>=`) 평가 시 MCV 일치 여부를 1차 검사하고, 비-MCV 롱테일에 대해서만 히스토그램 보간을 적용하는 이원화 모델 완비.
    - `cbo_stats.json` 영속화 및 역직렬화 무결성 보장.
    - 범주형/수치형 왜곡 데이터, 균등 데이터, 단일값 데이터 및 JSON 라운드트립 단위 테스트 완비 (`pkg/planner/stats_test.go`).
- [x] **다중 컬럼 확장 통계 및 함수적 종속성 모델링 (`C-5`) (`pkg/planner`, `pkg/catalog`, `pkg/engine`)**
  - **배경**: 속성 값 독립성(AVI: Attribute Value Independence) 가정으로 인해 상관관계가 높은 다중 컬럼(예: 국가-도시) 조건 평가 시 카디널리티가 최대 10~100배 과소평가(Underestimation)되는 왜곡 해소.
  - **구현 내용**:
    - `MultiColumnStats` 모델 및 다중 컬럼 결합 NDV(DistinctCount) 수집 엔진 구현 (`pkg/planner/stats.go`).
    - 단일 컬럼 NDV 대비 결합 NDV 비율을 기반으로 함수적 종속성 계수($dep \in [0.0, 1.0]$) 자동 산출 (`ComputeFunctionalDependency`).
    - `EstimateCompoundSelectivity` 및 `EstimateExprSelectivity`에서 상관관계 컬럼 감지 시 $dep \cdot \min(P_i) + (1-dep) \cdot \prod P_i$ 결합 선택도 산출 적용.
    - T-SQL DDL 문법 지원: `CREATE STATISTICS stat_name ON table_name (col1, col2, ...)` 및 `DROP STATISTICS` 파싱 및 카탈로그 영속화 연동.
    - `ANALYZE TABLE` 실행 시 복합 보조 인덱스 및 등록된 확장 통계에 대한 다중 컬럼 결합 통계 자동 수집 완비.
- [x] **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger) 조인 순서 최적화 (`B-4`) (`pkg/planner`)**
  - **배경**: 5개 이상 다중 테이블 조인 시 기존 $O(N!)$ 전수 순열 탐색으로 인한 지수적 계산 폭증 방지 및 서브밀리초 최적 조인 트리 탐색.
  - **구현 내용**:
    - 부분집합 비트마스크(`1 << n`) 기반 Selinger 동적 계획법 `optimizeJoinOrderDP` 및 $O(1)$ 플랫 슬라이스 메모이제이션 구현.
    - 조인 동등 조건 그래프 연결 검증을 통한 데카르트 곱(Cartesian Product) 우선 가지치기(Pruning) 및 비연결 쿼리용 2차 폴백 패스 완비.
    - DP 상태 공간 $\le 12$ 테이블 바운딩 가드(Golden Rules 21 & 23)로 복잡한 엔터프라이즈 ERP 다중 조인에서도 쿼리 플래닝 지연 서브밀리초 보장.
    - 6개 및 8개 테이블 체인 조인에 대한 서브밀리초 플래닝 단위 테스트 완비 (`pkg/planner/cbo_join_test.go`).
- [x] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**
  - **배경**: 쿼리 결과 스트리밍 시 행마다 `[]any` 및 `[][]byte` 슬라이스를 생성하는 힙 할당 오버헤드와 복사 제거.
  - **구현 내용**:
    - TDS 프로토콜 `WriteNBCRowDirect` 신설: `TokenNBCRow` (0xD2) 및 NULL 비트맵을 네트워크 직렬화 버퍼에 직접 기록.
    - `executeRPCAndRespond` 및 `executeQueryAndRespond`에서 행 반복문 외부 `scratchVals []any` 1회 할당 재사용.
    - PGWire 프로토콜 `writeExecutionResult` 및 `runAndWriteQueryWithFormats`에서 행 반복문 외부 `scratchRowBytes [][]byte` 1회 할당 재사용.
- [x] **T-SQL 핵심 시스템 내장 함수 및 IDENTITY 추적 완비 (`pkg/proc`, `pkg/engine`)**
  - **배경**: SQL Server 애플리케이션 및 ORM(Dapper, EF Core) 호환성을 위한 핵심 시스템 스칼라 함수 및 ID 생성 추적 기능 제공.
  - **구현 내용**:
    - `ISNUMERIC`, `CHARINDEX`, `PATINDEX`, `STUFF`, `IDENT_CURRENT`, `COLUMNPROPERTY`(IsIdentity, AllowsNull, Precision 등), `SCOPE_IDENTITY` 함수 구현.
    - `Coordinator` 테이블별 마지막 발번 ID 추적(`SetTableLastIdentity`/`GetTableLastIdentity`).
    - 세션 변수 치환 시 단일 `@` 사용자 변수가 시스템 `@@IDENTITY`와 충돌하는 토큰 결함 수정.
- [x] **TDS PLP (Partially Length-Prefixed) LOB 청크 스트리밍 (`VARCHAR(MAX)`, `VARBINARY(MAX)`) (`pkg/wire/tds`)**
  - **배경**: TDS 프로토콜에서 8,000 바이트를 초과하는 대용량 LOB 데이터 전송 시 단일 패킷 길이를 초과하는 페이로드 전송 표준 규격 준수.
  - **구현 내용**:
    - [MS-TDS] 2.2.5.2 규격 기반 PLP(Partially Length-Prefixed) 청크 인코딩 엔진 구현 (`writePLPValue`).
    - `ColMetaData`에서 비제한 LOB 타입(`VARCHAR(MAX)`, `NVARCHAR(MAX)`, `VARBINARY(MAX)`)에 대해 `0xFFFF` (65535) 길이 지시자 지정.
    - 8바이트 전체 길이 헤더, 최대 8,000바이트 단위 청크 분할 전송, 4바이트 제로 종료 청크(`0x00000000`) 스트리밍.
    - PLP 8바이트 `0xFFFFFFFFFFFFFFFF` NULL 표현식 완비.
    - Go 공식 `sqlserver` 드라이버(12,000바이트) 및 Python `pymssql` 드라이버(10,000바이트) E2E 검증 완료.
- [x] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - **배경**: Go 공식 드라이버(`go-mssqldb`, `lib/pq`, `pgx/v5`)에 이어 엔터프라이즈 멀티 언어 클라이언트 검증.
  - **구현 내용**:
    - `pkg/wire/driver_external_test.go`: Python `psycopg3` (PGWire :5433) 연결, DDL/DML, NULL 값 처리, 명시적 트랜잭션(COMMIT/ROLLBACK) E2E 검증.
    - `pkg/wire/tds/driver_external_test.go`: Python `pymssql` (TDS :1433 / FreeTDS dblib) 연결, DDL/DML, DATE/TIME/GUID 바이너리 파싱, 트랜잭션 원자성 E2E 검증.
    - FreeTDS/pymssql 클라이언트 호환성: 일반 DDL 실행 시 unsolicited INFO 토큰 예외 방지 가드(`server.go` PRINT 전용 제한) 적용.
    - `uv run` 기반 격리형 실행 환경 및 미설치 환경 자동 스킵(`exec.LookPath`) 연동으로 100% 무결점 밀폐형 테스트(Golden Rule 17) 달성.
- [x] **볼케이노 벡터화 청크 반복자 (Chunked Batch Next: 64~1024건) (`P-4`) (`pkg/execution`, `pkg/engine`)**
  - **배경**: 행 단위 `Next()` 가상 함수 호출 및 `ctx.Done()` 검사 오버헤드로 인한 CPU 캐시 미스 해소.
  - **구현 내용**:
    - `pkg/execution/chunk.go`: `Chunk` 구조체(1,024건 프리알로케이션), `ChunkedIterator` 인터페이스, `PullChunk` 자동 폴백 엔진 신설.
    - `SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`, `sqlexec_select` 전반에 배치 파이프라인 관통.
    - 기존 단건 `Next()` 호출에 대해 내부 청크 버퍼링을 통한 100% 투명한 하위 호환성 보장.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - **배경**: 풀 스캔 및 인덱스 스캔 시 불필요한 모든 컬럼의 Varint/문자열 디코딩 힙 할당을 방지하고, 메인 테이블 조회를 생략.
  - **구현 내용**:
    - `pkg/storage/tuple.go`: 제로 할당 `SkipTupleColumn`, $O(1)$ 오프셋 캐싱 `LazyTuple`, 선별 디코딩 `DecodeTupleSelected` 구현.
    - `CREATE INDEX ... INCLUDE(...)` 문법 파싱, 카탈로그 및 `SkipList` / `IndexData` 비키 페이로드 적재 연동.
    - CBO 플래너 `isCovering` 및 `NodeIndexOnlyScan` 연동으로 테이블 힙 조회 100% 생략.
- [x] **CBO 옵티마이저 힌트 오버라이드 제어 (`D-9`) (`pkg/planner`, `pkg/proc`)**
  - 테이블 힌트(`WITH (INDEX(...))`) 및 쿼리 힌트(`OPTION (HASH/MERGE/LOOP JOIN, FORCE ORDER)`) 파싱 및 CBO 강제 오버라이드.
- [x] **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`B-3`) (`pkg/execution`, `pkg/planner`)**
  - 정렬 입력에 대한 $O(M+N)$ 해시리스 스트리밍 반복자 `MergeJoinIterator` 신설 및 CBO 비용 경쟁 모델 연동.
- [x] **단순 PK 조회 고속 지문 해싱 & 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - FNV-1a 64비트 지문 해싱 기반 Fast-Path 템플릿 탐지, 정규 파서/CBO/VDBE 스택 우회 및 스토리지(`tx.Get`) 직결.
- [x] **TDS 프로토콜 고도화: NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - TDS 7.3+ 표준 `BuildNBCRow` 신설, NULL 비트맵 패킹 및 0바이트 전송을 통한 네트워크 오버헤드 30%+ 절감.
- [x] **SQL Server 2008 / TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET 및 UNIQUEIDENTIFIER 바이너리 직렬화 (`pkg/wire/tds`)**
  - `DATE`(0x28, 0001-01-01 기준 일수 3바이트 LE), `TIME`(0x29, Scale 0..7 100ns 틱 3~5바이트), `DATETIME2`(0x2A, Scale 0..7 틱 + 그레고리력 3바이트), `DATETIMEOFFSET`(0x2B, UTC DATETIME2 + 2바이트 분 오프셋), `UNIQUEIDENTIFIER`(0x24, Windows Mixed-Endian 바이트 스왑) 전 항목 완비.
  - 결과셋 컬럼 타입 자동 추론(`inferColumnInfos`) 고도화 및 공식 `github.com/microsoft/go-mssqldb`(`mssql.UniqueIdentifier`, `time.Time`) 통합 검증 완료.

---

## 2. 도메인별 대기 백로그 (Domain-Specific Backlogs)

### 2.1. CBO & 쿼리 최적화 (CBO & Query Engine)

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료 (`[x]`)** | 컬럼별 정렬 및 히스토그램 수집을 `sync.WaitGroup` 병렬화 완료 |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 완료 |
| **C-6** | **OR 조건 및 불리언 트리 선택도 계산** | **완료 (`[x]`)** | AST 재귀 평가, 배타합/독립합집합, NOT, IN, BETWEEN 정밀 산출 완료 |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **완료 (`[x]`)** | T-SQL 조인 힌트(`HASH/LOOP/MERGE`) 및 인덱스 힌트(`WITH (INDEX(...))`) 강제 반영 |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자** | **완료 (`[x]`)** | 정렬된 입력에 대해 $O(M+N)$ 해시리스 스트리밍 조인(`MergeJoinIterator`) |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP)** | **완료 (`[x]`)** | 5개 이상 테이블 조인 시 Left-Deep DP 부분집합 비트마스크 동적 탐색 완료 |
| **C-5** | **다중 컬럼 확장 통계 (Extended Stats)** | **완료 (`[x]`)** | 컬럼 상관관계로 인한 카디널리티 과소평가 방지 및 결합 선택도 산출 완료 |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **완료 (`[x]`)** | 왜곡 데이터 최빈값 분리 보관 및 비-MCV 히스토그램 이원화 완비 |
| **D-8** | **백그라운드 자동 통계 갱신 데몬** | **완료 (`[x]`)** | DML 변동량(Dirty Tuples) 기반 자동 `ANALYZE` 트리거 |

- [x] **동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`B-4`) (`pkg/planner`)**
  - 4개 이하의 체인 조인 전수 순열 탐색을 넘어, $N > 4$ 테이블 조인에 대한 부분집합 비트마스크(`1 << n`) 기반 Selinger DP 및 연결성 기반 가지치기(Pruning) 완비.
- [x] **다중 컬럼 확장 통계 (Extended / Multi-Column Statistics) (`C-5`) (`pkg/planner`, `pkg/catalog`, `pkg/engine`)**
  - 컬럼 간 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 수집, 함수적 종속성 모델링($dep$), CREATE/DROP STATISTICS DDL 및 결합 선택도 산출 완비.
- [x] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`C-7`) (`pkg/planner`)**
  - 왜곡(Skew)이 심한 상위 N개 최빈값(Top-20)은 MCV 목록으로 보관하고 나머지 균등 분포는 히스토그램으로 이원화 처리하여 선택도 오차 최소화.
- [x] **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze Daemon) (`D-8`) (`pkg/engine`, `pkg/catalog`, `pkg/storage`)**
  - 테이블 DML 변경 비율(동적 임계치: `min_threshold + base_rows * scale_factor`)을 추적하여 백그라운드 비동기로 `coordinator.AnalyzeTable` 자동 트리거.
  - 트랜잭션 커밋 핫패스에 락 경합 없이 원자적 카운팅(`AddWALHook`) 및 디바운싱 완비.
- [x] **반정형 JSON 표현식 B-Tree 인덱스 및 내장 함수 완비 (`ADR-022`) (`pkg/proc`, `pkg/storage`, `pkg/planner`, `pkg/execution`, `pkg/engine`)**
  - [x] **[1단계] JSON 내장 함수 및 파서 연산자 구현 (완료) (`pkg/proc`, `pkg/engine`, `pkg/execution`)**:
    - PostgreSQL 이항 연산자 `->`(JSON 하위 객체/배열 반환) 및 `->>`(스칼라 텍스트 추출) Pratt 파서 등록.
    - T-SQL & ANSI 표준 내장 함수: `ISJSON(val)`, `JSON_VALUE(col, '$.path')`, `JSON_QUERY(col, '$.path')`, `JSON_MODIFY(col, '$.path', new_val)`.
    - RFC 6901 / 경량 JSONPath(키 접근 및 `$.key.nested[0]`) 고속 파서 및 프로젝션/WHERE 필터 평가 연동.
    - `Tuple.ToEvalMap()` 지연 튜플(LazyTuple) 역직렬화 누락 버그 해결 및 스칼라 문자열 리터럴(`isSingleQuotedLiteral`) 식별 정밀화.
    - In-Memory OCC 및 WiredTiger 양대 스토리지 백엔드 상에서 단일/체인 추출, 다중 테이블 조인 및 필터 100% 통합 검증 완료 (`pkg/engine/json_query_test.go`, `pkg/engine/json_wt_test.go`).
  - [x] **[2단계] 기능 표현식 인덱스 DDL 및 스토리지 키 추출 (완료) (`pkg/proc`, `pkg/storage`, `pkg/engine`)**:
    - `CREATE [UNIQUE] INDEX idx ON tbl ((data->>'key'))` 및 `CREATE INDEX idx ON tbl ((JSON_VALUE(data, '$.path')))` 이중 괄호 기능 인덱스 DDL 파싱 및 AST 정규화 (`ExtractJSONExprInfo`, `ParseCreateIndex`).
    - `storage.IndexSchema`에 `Expression`, `ExprColumn`, `ExprPath` 및 `IsExpression()`, `TableSchema.FindExpressionIndex` 완비.
    - 스토리지 튜플 삽입/갱신/삭제 시 `ExtractJSONIndexKey`를 통해 실시간 SkipList 및 WAL 동기화 (`extractIndexKeyFromRowWithSchema`).
    - ANSI SQL 표준 준수: UNIQUE 표현식 인덱스에서 NULL 다중 허용(`!key.IsNull`), 중복 비-NULL 키 삽입 시 즉각 `ErrUniqueViolation` 롤백.
    - In-Memory OCC 및 WiredTiger 양대 스토리지 백엔드 상에서 DDL 생성, 직접 `IndexScan` 범위 스캔, UPDATE/DELETE 실시간 동기화 100% 통합 검증 완료 (`TestJSON_ExpressionIndex`, `TestJSON_ExpressionIndex_WiredTiger`).
  - [x] **[3단계] CBO 표현식 인덱스 매칭 및 E2E/성능 벤치마크 (완료) (`pkg/planner`, `pkg/execution`, `pkg/engine`)**:
    - CBO 옵티마이저 매칭 (`pkg/planner/planner.go`): `WHERE data->>'city' = 'Seoul'` 또는 `WHERE JSON_VALUE(data, '$.city') = 'Seoul'` 평가 시 순차 스캔 대신 `Index Scan using <idx_name>` 자동 선택.
    - 실행기 바인딩 (`pkg/execution/builder.go`): `parseIndexCond`에서 기능 표현식 조건 분석 및 인덱스 스캔 바운드(`lowVal`, `highVal`) 자동 추출.
    - 단일 테이블 고속 경로 (`pkg/engine/sqlexec_eval.go`): `findIndexAndBounds`에서 표현식 인덱스 즉각 감지 및 복합 투영 식별 시 Volcano 위임.
    - WiredTiger 인덱스 스캔 필터링 커서 (`pkg/storage/wiredtiger/wt_engine.go`): `wtFilteredIndexCursor`로 JSON 및 컬럼 키 범위 정확 필터링 완비.
    - 성능 벤치마크 검증: 1,000건 JSON 레코드 기준 SeqScan(2.17ms) 대비 IndexScan(334µs)으로 **6.5배~7.3배 성능 가속 달성** (`TestJSON_ExpressionIndex_CBO_And_Explain`, `TestJSON_ExpressionIndex_Benchmark`).



### 2.2. 스토리지 & 엔진 성능 (Storage & Engine Performance)

| 단계 | 전략 분야 | 핵심 구현 과제 | 상태 | 예상 효과 및 ROI |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 & 무경합 Fast-Path** | **완료 (`[x]`)** | `FsyncAlways` 100배 지연 단축(0.038ms/op), TPC-C 2.8K TPS |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 & 커버링 인덱스** | **완료 (`[x]`)** | 스캔 속도 2배 향상, 불필요한 Varint 디코딩 힙 할당 제거 |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩 & I/O 버퍼 결합** | **완료 (`[x]`)** | 중간 `[]any` 레코드 박싱 제거, TCP syscall 횟수 대폭 감소 |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next)** | **완료 (`[x]`)** | 볼케이노 가상 디스패치 및 컨텍스트 취소 오버헤드 99% 삭감, 1024 튜플 배치 스트리밍 |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱 & 단순 PK 조회 바이패스** | **완료 (`[x]`)** | 단순 PK 조회 파서 바이패스, TPS 50%+ 향상, GC 부하 90% 경감 |
| **P-6** | **파서 & 엔진** | **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 리팩토링** | **완료 (`[x]`)** | 40여 개 정규식 싱글톤/캐시 승격으로 쿼리당 5~15µs 지연 및 힙 할당 100% 제거 |
| **P-7** | **JSON & 내장함수** | **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 키 스캐너** | **완료 (`[x]`)** | 다중 JSON 필드 추출 쿼리 CPU/GC 부하 50~70% 경감 |
| **P-8** | **스토리지/CGo** | **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`cur->reset`)** | **완료 (`[x]`)** | 세션별 커서 풀링 및 C 헬퍼 단일화로 894ns 룩업(1.13M+ OPS) 달성 |
| **P-9** | **옵티마이저/캐시** | **정형 SQL Parameterized 쿼리 실행 계획 캐시 (SQL Plan Cache)** | **보류 (`[-]`)** | SP/Prepared/PK 바이패스 캐시로 주요 OLTP 경로 기충족되어 보류 |
| **P-10** | **실행기/병렬화** | **Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 (`ParallelSeqScan`)** | **보류 (`[-]`)** | OLAP 분석 쿼리 워커 분할 (OLTP 동시성 영향 방지를 위해 1.0 보류) |
| **P-11** | **스토리지/풀링** | **In-Memory OCC 트랜잭션 객체 & 워크스페이스 맵 풀링** | **완료 (`[x]`)** | 트랜잭션 맵 6개 동적 할당 0건화, 초당 2만 TPS GC 부하 80%+ 경감 |
| **P-12** | **실행기/풀링** | **Volcano 벡터화 배치 청크(`Chunk`) 풀링** | **완료 (`[x]`)** | 1,024건 슬라이스 할당 제거 및 대용량 배치/P-10 병렬 스트리밍 GC 최소화 |
| **P-13** | **실행기/DML** | **DML Record(Map) 제거 ➔ Row(Slice) 직결 (Zero-Alloc DML)** | **우선 착수 (`[ ]`)** | 중간 맵 할당 및 해시 룩업 완전 제거, DML 처리량 20~30% 향상 |

- [ ] **DML `storage.Record`(Map) 제거 ➔ `storage.Row`(Slice) 직결 파이프라인 (`P-13`) (`pkg/proc/runtime`, `pkg/engine`, `pkg/storage`)**
  - **배경**: VDBE `OpInsert`/`OpUpdate` 및 DML 실행기(`sqlexec_dml.go`)에서 스토리지 엔진(`tx.Insert`)으로 데이터를 넘길 때 매번 `make(map[string]Value)` 맵을 힙에 생성하고, 스토리지 엔진은 이를 다시 컬럼별로 순회하며 해시 룩업하여 슬라이스로 변환하는 중복 오버헤드 제거.
  - **구현 내용**:
    - `pkg/proc/runtime/vm_dispatcher.go`: `OpInsert` 및 `OpUpdate`에서 중간 `storage.Record` 맵 생성을 제거하고, 스키마 컬럼 슬롯 오프셋에 직접 값을 채워 `storage.Row` 슬라이스를 구성.
    - `pkg/engine/sqlexec_dml.go`: DML 파싱/실행 시 컬럼 인덱스 매핑을 통해 직접 `storage.Row`를 생성하고 기구축된 `tx.InsertRow(table, row)` / `tx.UpdateRow(table, pk, row)`로 직결.
    - DML/Prepared Statement 처리 시 맵 할당 0건(Zero-Allocation) 달성 및 TPC-C/DML 처리량 20~30% 향상.

- [x] **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 및 캐싱 리팩토링 (`P-6`) (`pkg/engine`)**
  - **배경**: `sqlexec.go`, `sqlexec_dml.go`, `sqlexec_pivot.go`, `sqlexec_apply.go`, `sqlexec_derived.go`, `sqlexec_eval.go`, `sqlexec_explain.go`, `sqlexec_mssql_sys.go`, `sqlexec_gui_interop.go`, `sqlexec_string_split.go`, `sqlexec_procs.go`의 핫패스 함수 및 행 루프 내부에 분산되어 있던 40여 개의 동적 `regexp.MustCompile` 호출로 인한 매 쿼리당 NFA/DFA 재컴파일 및 힙 할당 오버헤드(5~15µs) 제거.
  - **구현 내용**:
    - **패키지 레벨 싱글톤 승격**: 정적 정규식 패턴 전체를 패키지 로드 시 1회 컴파일되는 `var reXxx = regexp.MustCompile(...)` 싱글톤으로 승격.
    - **동적 파라미터 정규식 캐싱 (`getCompiledRegexp`)**: `CROSS/OUTER APPLY`, 뷰 치환, CTE 재작성, 프로시저 파라미터 치환, 세션 테이블 변수 치환 등 동적 식별자가 결합되는 정규식에 대해 `sync.Map` 기반 스레드 안전 정규식 캐시(`rePatternCache`)를 적용하여 0 allocation 재사용 보장.
    - **행 루프/평가 핫패스 컴파일 제거**: `evalSimpleRowWhere`, `evaluateSingleConditionMap`, `evalUpdateExpr` 등 매 레코드마다 호출되던 정규식 동적 생성을 100% 제거하여 쿼리당 디스패치 지연 서브마이크로초화 달성.
- [x] **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 경량 키 스캐너 (`P-7`) (`pkg/storage`, `pkg/engine`)**
  - **배경**: `ExtractJSON`, `ExtractJSONText`, `JSONValueByPath` 호출 시 매번 Go 표준 라이브러리의 `json.Unmarshal`을 호출하여 `map[string]any`를 힙에 생성하는 오버헤드 및 단일 튜플 내 다중 JSON 필드 참조 시 중복 파싱 제거.
  - **구현 내용**:
    - 동일 튜플 평가 컨텍스트 내 파싱된 JSON DOM 캐싱: 한 행에서 `doc->>'name'`, `doc->>'city'` 등 여러 필드를 추출할 때 1회만 언마샬링.
    - 최상위 키 추출 전용 제로-할당 경량 스트리밍 바이트 스캐너(`ScanJSONTopLevelKey`): 전체 트리 구축 없이 바이트 오프셋 슬라이싱으로 즉시 추출.
    - JSON 대량 스캔/필터 쿼리 시 GC 부하 70% 경감.
- [-] **정형 SQL Parameterized 쿼리 실행 계획 캐시 (`P-9`) (`pkg/engine`, `pkg/planner`) - [보류/Postponed]**
  - **보류 사유**: 고빈도 OLTP 워크로드는 이미 SP VDBE 바이트코드 캐시(`runtime.PlanCache`), SP 내부 물리 플랜 바인딩(`RelQueryData.CachedPlan`), Prepared Statement 캐시, 단순 PK 바이패스(`pkBypassCache`)에 의해 파싱/플래닝이 100% 생략되고 있음. Ad-hoc 임의 SQL 템플릿화는 리터럴 충돌 위험 대비 실익이 낮아 보류.
- [-] **Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 (`P-10`) (`pkg/execution`) - [보류/Postponed]**
  - **보류 사유**: 고동시성 OLTP 엔진 환경에서 단일 분석 쿼리의 멀티코어 점유는 다른 핵심 트랜잭션의 CPU 기아(Starvation) 및 레이턴시 급증을 유발함. 워커 동기화 및 채널 제어 복잡성 회피를 위해 1.0 프로덕션에서는 보류하고 향후 OLAP 분석 단계(v1.2+)로 이관.
- [x] **In-Memory OCC 트랜잭션 객체 & 워크스페이스 맵 풀링 (`P-11`) (`pkg/storage/memengine`)**
  - **배경**: `sess.BeginTx()` 호출 시마다 `Transaction` 구조체와 6개의 Go 맵(`tableCache`, `baseVers`, `local`, `deleted`, `changes`, `savepoints`)을 힙에 새로 할당(`make(map...)`)하고 커밋/롤백 시 GC에 버리는 오버헤드(초당 16,000~20,000 TPS 환경에서 초당 10만+ 맵 객체 생성) 제거.
  - **구현 내용**:
    - `sync.Pool` 기반 `txPool` 도입.
    - 트랜잭션 종료(`Commit()` 또는 `Rollback()`) 시 Go 1.21+ 내장 `clear(m)` 함수를 통해 맵의 내부 해시 버킷 용량을 유지한 채 $O(1)$ 초기화 후 풀에 반환.
    - 트랜잭션 시작 시 힙 메모리 할당 0건(Zero-Allocation Transaction) 달성 및 p99 지연 시간 안정화.
- [x] **Volcano 벡터화 배치 청크(`Chunk`) 풀링 (`P-12`) (`pkg/execution`)**
  - **배경**: 1,024건 단위 튜플 배치 스트리밍(`Chunk`) 간 연산자(`Filter`, `Projection`, `Sort`, `HashJoin`) 경계 전송 및 버퍼링 시 `make([]Tuple, 0, 1024)` 슬라이스가 반복 동적 생성되어 GC에 버려지는 현상 방지 및 향후 P-10 병렬 실행기 파이프라인 연계.
  - **구현 내용**:
    - `sync.Pool` 기반 `chunkPool` (`GetChunk`, `PutChunk`) 구축.
    - `Chunk.Reset()`을 통해 1,024개 튜플 용량 슬라이스를 보존한 상태로 재대여.
    - 10만~1000만 건 대규모 테이블 스캔, 정렬, 해시 조인 시 슬라이스 메모리 할당 및 복사 오버헤드 제거.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 투영 푸시다운 (`pkg/storage`, `pkg/execution`)**
  - 풀 스캔 시 WHERE 절 조건 컬럼만 선별 디코딩하고, 조건을 통과한 행에 한해서만 나머지 투영 컬럼을 언패킹하여 CPU 및 힙 할당 70% 삭감.
- [x] **커버링 인덱스 비키 컬럼 문법 지원: `CREATE INDEX ... INCLUDE (col...)` (`pkg/storage`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - B+Tree/SkipList 리프 노드에 비키 컬럼을 페이로드로 적재하여 메인 테이블 힙 조회를 100% 생략하는 Index-Only Scan 극대화.
- [x] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**
  - TDS 프로토콜 `WriteNBCRowDirect` 신설: `TokenNBCRow` (0xD2) 및 NULL 비트맵을 네트워크 직렬화 버퍼에 직접 기록하여 중간 `[]any` 레코드 박싱 및 버퍼 복사 100% 제거.
  - TDS 및 PGWire 프로토콜 결과 스트리밍 핫패스에서 행 반복문 외부 `scratchVals` / `scratchRowBytes` 1회 할당 재사용.
- [x] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator: 64~1024건) (`pkg/execution`, `pkg/engine`)**
  - 행 단위 `Next()` 가상 함수 호출 및 `ctx.Done()` 검사 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍(`ChunkedIterator`, `NextChunk`).
  - `SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`, `sqlexec_select` 전반에 배치 파이프라인 관통 및 기존 단건 `Next()` 투명 버퍼링 100% 하위 호환성 보장.
- [x] **세션/쿼리 전용 아레나 메모리 할당기 (Arena Allocator) (`pkg/execution`, `pkg/storage`)**
  - 해시 조인, 소트 버퍼, 스캔, 어그리게이션에 대해 쿼리 수명주기에 묶인 아레나 할당기를 적용하여 Go GC 압박 원천 제거 (`GetArena` / `PutArena`, `AllocAnySlice`, `AllocValues`, `AllocString`, `AllocBytes`).
  - 8바이트 정렬 슬랩 풀링(`sync.Pool`)과 단일 포인터 리셋($O(1)$)으로 슬랩 재활용 및 제로 GC 실행 경로 구현.

### 2.3. 와이어 프로토콜 & 생태계 (Wire Protocols & Ecosystem)
- [x] **T-SQL 특수 타입 직렬화 (TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET, UNIQUEIDENTIFIER) (`pkg/wire/tds`)**
  - `UNIQUEIDENTIFIER` (UUID 16바이트 Windows GUID 바이트 스왑), `DATE` (`0x28`, 3바이트 LE), `TIME` (`0x29`, 100ns 틱 3~5바이트), `DATETIME2` (`0x2A`, 틱 + 3바이트 날짜), `DATETIMEOFFSET` (`0x2B`, UTC DATETIME2 + 2바이트 분 오프셋) TDS 바이너리 인코딩 완비.
  - 결과셋 컬럼 타입 자동 추론(`inferColumnInfos`) 고도화 및 `go-mssqldb` 공식 드라이버 통합 검증 완료.
- [x] **PLP LOB 청크 스트리밍 (`pkg/wire/tds`)**
  - [MS-TDS] 2.2.5.2 Partially Length-Prefixed (PLP) 스트리밍 규격 구현 (`writePLPValue`), 8바이트 전체 길이 헤더, 최대 8,000바이트 단위 청크 분할 및 4바이트 제로 종료 청크(`0x00000000`) 전송, 8바이트 `0xFFFFFFFFFFFFFFFF` NULL 표현 완비.
- [x] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - Python `psycopg3` (PostgreSQL Wire Protocol :5433) 및 `pymssql` (TDS Protocol :1433) 종단간 통합 검증 완료.
  - `CREATE TABLE`, `INSERT`, `SELECT`, DATE/TIME/GUID 바이너리 파싱, 트랜잭션 `COMMIT`/`ROLLBACK` 원자성 및 NULL 값 처리 완비.
- [x] **SSMS (SQL Server Management Studio) Object Explorer 가상 카탈로그/DMV 뷰 (`pkg/engine`, `pkg/catalog`)**
  - SSMS 접속 및 탐색기 트리 조회에 필수적인 `sys.configurations`, `sys.dm_exec_sessions`, `sys.dm_exec_connections`, `sys.database_principals`, `sys.server_principals`, `sys.dm_os_sys_info`, `sys.endpoints`, `sys.database_files`, `sys.master_files`, `sys.all_objects`, `sys.all_columns`, `sys.all_views` 가상 뷰 완비.
- [x] **PostgreSQL 서버 사이드 커서 및 Extended Query 포털 배치 스트리밍 (`pkg/engine`, `pkg/wire`)**
  - 대규모 쿼리 결과 집합을 분할 수신하는 드라이버 호환성을 위한 `DECLARE ... CURSOR [WITH HOLD] FOR`, `FETCH`, `MOVE`, `CLOSE [ALL]` 및 트랜잭션 경계 수명주기 완비.
  - Extended Query 프로토콜 `Execute`의 `maxRows` 페이로드 분할 스트리밍 및 `MsgPortalSuspended ('s')` 토큰 송출 지원.
- [x] **임베디드 웹 스튜디오 HA 복제 모니터링 카드 (`pkg/ui`)**
  - 브라우저 웹 스튜디오(`:8090/_/`)에 고가용성(HA) 복제 상태(Primary vs Replica) 시각화 카드 추가.
  - 연결된 Replica 목록, LSN 랙(Lag) 지연 시간, 스트리밍 상태 표시 및 수동 승격(`Promote`) 버튼 제공.

### 2.4. 엔터프라이즈 보안 & 거버넌스 (Enterprise Security & Governance)
- [-] **[보류 / Postponed] 투명 데이터 암호화 (TDE: Transparent Data Encryption) (`pkg/storage`)**
  - OS/클라우드 블록 볼륨 암호화 표준 가이드([`doc/STORAGE_ENCRYPTION_GUIDE.md`](doc/STORAGE_ENCRYPTION_GUIDE.md)) 수립으로 대체 충족 및 프로세스 중복 암호화 부하 방지를 위해 보류 (2.7절 참조).
- [ ] **불변 엔터프라이즈 감사 로그 (Enterprise Immutable Audit Log) (`pkg/logger`, `pkg/api`)**
  - 로그인 성공/실패, 권한 변경(`GRANT/REVOKE`), DDL 스키마 변경, 관리자 데이터 조작 이벤트를 태깅하여 위변조가 불가능한 불변 감사 파일로 안전 기록.
- [-] **[보류 / Postponed] `INSTEAD OF` 트리거 런타임 인터셉터 (`pkg/proc/runtime`, `pkg/engine`)**
  - DML 핫패스 복잡도 증가 방지 및 현대적 ORM/SP 아키텍처 환경을 고려하여 1.0 프로덕션에서는 보류 (2.7절 참조).

### 2.5. 고가용성 & 분석 확장 (HA & Analytical Extensions)
- [x] **Always On 스타일 자동 장애 조치 및 리더 선출 (Automatic Failover & Leader Election) (`pkg/replication`)**
  - Primary 노드 다운 시 복제 랙(LSN Lag)이 가장 적은 최신 Replica 노드를 새로운 Primary로 자동 승격 (Leader Promotion).
  - 클라이언트 라우팅 갱신 및 Split-Brain 방지를 위한 리더 텀(Term) 검증.
- [x] **분리형 실시간 CDC (Change Data Capture) 스트리밍 프레임워크 (`pkg/cdc`, `pkg/api`, `cmd/server`)**
  - 트랜잭션 커밋과 100% 원자적으로 연동되는 Row-level 변경 델타(Inserted/Updated/Deleted) 이벤트 모델(`ChangeEvent`) 및 스트리머(`Streamer`) 구축.
  - 유계 링 버퍼(Bounded Ring Buffer, 10,000건) 기반 과거 LSN 재생, 비차단 팬아웃(Non-blocking Backpressure, Golden Rule 29), 테이블 필터링 지원.
  - `GET /api/cdc/stream` 실시간 NDJSON 및 SSE 스트리밍 HTTP 엔드포인트 완비.
  - 스토리지 엔진(`memengine`, `wiredtiger`) 커밋 훅 다중화(`AddWALHook`) 관통.
  - Python 표준 라이브러리 기반 DuckDB 마이크로 배치 수신 워커 레시피 작성 (`doc/CDC_DUCKDB_INTEGRATION.md`).
- [x] **트랜잭셔널 아웃박스 외부 브로커 디스패처 (Kafka, RabbitMQ, Webhook) (`pkg/cdc`, `pkg/config`, `pkg/api`, `cmd/server`)**
  - CDC 스트림을 외부 분산 메시지 브로커로 무손실 발행하는 Outbox 릴레이 워커, 지수 백오프 재시도 및 아웃박스 체크포인트 원자적 영속화 완비.
- [ ] **[보류 / Postponed] Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`)**
  - CGO/WiredTiger 의존성 없이 완전한 정적 바이너리(`CGO_ENABLED=0`, Golden Rule 36) 크로스 컴파일 배포를 위한 `storage.Engine` 플러그인 백엔드 (현재는 네이티브 WiredTiger CGO 및 인메모리 OCC 고성능 엔진에 집중하며 보류).
- [x] **[제거 / Spec-Out] 내장형 Google 및 GitHub OAuth2 소셜 로그인 & 자동 사용자 프로비저닝 (`pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)**
  - DB 커널 경량화, 아웃바운드 인터넷 의존성 제거, 보안 공격 표면 축소를 위해 커널 내장 OAuth2 소셜 로그인을 제거하고 전면 리버스 프록시 위임으로 표준화 (`GEMINI.md` 8.6절 Spec-Out).
- [x] **[제거 / Spec-Out] 내장형 S3 Blob 파일 스토리지 및 이미지 썸네일 리사이징 (`GEMINI.md` 8.7절)**
  - 대용량 바이너리 스트리밍 및 CPU 집약적 이미지 리사이징으로 인한 OLTP 엔진 성능 저하(GC 압력/CPU 기아)를 방지하고, S3/MinIO 및 엣지 CDN 위임 표준화를 위해 커널 내장 배제 (패턴 1 Pre-signed URL 직접 업로드 계획서: [`doc/S3_PRESIGNED_STORAGE_PLAN.md`](doc/S3_PRESIGNED_STORAGE_PLAN.md) 참조).
- [x] **선언적 스키마 마이그레이션 CLI (`lionsql migrate`) (`cmd/lionsql`, `pkg/migrate`, `cmd/server`)**
  - 타임스탬프 기반 마이그레이션 파일 페어(`YYYYMMDDHHMMSS_<name>.up.sql` / `.down.sql`) 생성 (`lionsql migrate new <name>`).
  - 데이터베이스 스키마 순차 배포 및 실행 지연 시간 추적 (`lionsql migrate up [-steps=N]`).
  - `.down.sql` 기반 역순 롤백 및 `_migrations` 이력 갱신 (`lionsql migrate down [-steps=N]`).
  - 배포 상태 정렬 표 형식 출력 (`lionsql migrate status`).
  - 임베디드 데이터베이스 직접 쿼리 실행기 (`lionsql sql "<query>"`).
  - `cmd/server` 및 `cmd/lionsql` 양방향 CLI 문법 패리티 완비.

### 2.6. 코드베이스 구조 개선 & 아키텍처 리팩토링 (Architecture & Refactoring)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 목표 및 리팩토링 상세 |
| :---: | :--- | :--- | :---: | :--- |
| **R-1** | **엔진/카탈로그** | **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화** | **완료 (`[x]`)** | 3.2K줄 `sqlexec_pgcatalog.go` 도메인 분해(PG/MSSQL/ANSI) 및 $O(1)$ 라우팅 |
| **R-2** | **파서/내장함수** | **Pratt 파서 및 내장 함수 도메인별 관심사 분리** | **완료 (`[x]`)** | `parser.go`(3.2K줄), `builtins.go`(2.8K줄) 도메인 모듈화 |
| **R-3** | **엔진/실행기** | **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 로직 일원화** | **완료 (`[x]`)** | `sqlexec_eval.go`와 `execution/builder.go` 간 중복 제거 (DRY) |
| **R-4** | **스토리지/OCC** | **In-Memory OCC 트랜잭션 라이프사이클 모듈 분해** | **Post-1.0 이관 (`[-]`)** | 1.0 안정성 우선, 동시성 검증 완료 상태 보존 (2.7절 참조) |
| **R-5** | **스토리지/CGo** | **WiredTiger CGo 바인딩 격리 및 트랜잭션/커서 모듈화** | **Post-1.0 이관 (`[-]`)** | 1.0 안정성 우선, CGo 빌드 복잡도 및 회귀 위험 방지 (2.7절 참조) |

- [x] **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화 (`R-1`) (`pkg/engine`)**
  - **배경**: 단일 파일(3,284라인)에 PostgreSQL `pg_catalog`, ANSI `information_schema`, MSSQL `sys.tables`/DMV, DBeaver/Beekeeper/DbGate/HeidiSQL 도구별 쿼리 인터셉터가 혼재되어 유지보수성과 가독성이 저하됨.
  - **구현 내용**:
    - **도메인별 물리적 파일 분리**:
      - `sqlexec_pgcatalog.go` (634라인으로 80% 이상 슬림화): 순수 PostgreSQL `pg_catalog.pg_*` 가상 테이블 및 전역 레지스트리 디스패처.
      - `sqlexec_mssql_sys.go` (554라인): SQL Server / TDS 전용 가상 카탈로그 및 DMV (`sys.tables`, `sys.columns`, `sys.dm_*`, `sys.database_files` 등) 및 데이터타입 포매터.
      - `sqlexec_info_schema.go` (420라인): ANSI 표준 정보 스키마 (`information_schema.tables`, `columns`, `table_constraints` 등) 및 스키마 카탈로그 매핑.
      - `sqlexec_gui_interop.go` (477라인): HeidiSQL, Beekeeper Studio, DbGate, DBeaver 멀티 테이블 크로스 조인 쿼리 인터셉터.
    - **가상 뷰 레지스트리 패턴 확립 (`VirtualCatalogView`)**:
      - `VirtualCatalogView` 함수 시그니처 및 동시성 안전 읽기 전용 `catalogViewRegistry map[string]VirtualCatalogView` 도입.
      - 기존 60줄의 `isPgCatalogTable` 분기문을 $O(1)$ 맵 조회로 교체.
      - 기존 1,500줄의 `execPgCatalogSelect` 거대 switch-case를 15줄의 $O(1)$ 레지스트리 디스패처로 전면 교체(개방-폐쇄 원칙 OCP 달성).
    - 전체 25개 패키지 race-free 테스트(100% PASS) 및 GUI 클라이언트 호환성 보장.
- [x] **Pratt 파서 및 내장 함수 도메인별 관심사 분리 (`R-2`) (`pkg/proc`)**
  - **배경**: `pkg/proc/parser.go`(3,241라인)와 `pkg/proc/builtins.go`(2,880라인)가 단일 파일에 비대하게 집중되어 신규 문법/함수 추가 시 인지 부하 및 머지 충돌 위험 증가.
  - **구현 내용**:
    - **파서 모듈화 (3,241줄 ➔ 5개 도메인 파일 분해)**:
      - `parser.go` (280줄, 90%+ 슬림화): 코어 구조체, NewParser, 렉서 탐색 및 공통 어휘 헬퍼.
      - `parser_ddl.go` (599줄): 프로시저, 함수, 트리거, 테이블, 인덱스 생성/삭제 DDL.
      - `parser_dml.go` (1,141줄): `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH`(CTE), `EXPLAIN`.
      - `parser_stmt.go` (656줄): `IF/WHILE/TRY...CATCH/BEGIN...END`, 트랜잭션, 커서, `EXEC`.
      - `parser_expr.go` (615줄): Pratt 우선순위 식 파싱, 함수 호출, CASE WHEN, JSON 표현식 분석.
    - **내장 함수 모듈화 (2,880줄 ➔ 6개 도메인 파일 분해)**:
      - `builtins.go` (153줄, 94%+ 슬림화): 스칼라 함수 SPI 등록 및 도메인 디스패처, 공통 변환 헬퍼.
      - `builtins_date.go` (765줄): 날짜/시간 함수 및 파싱/버킷 헬퍼.
      - `builtins_string.go` (885줄): 문자열 조작, PATINDEX, SOUNDEX, 포맷팅 헬퍼.
      - `builtins_math.go` (352줄): 수학, 삼각, 비트 연산 함수.
      - `builtins_json.go` (179줄): JSON 내장 함수 및 경로 탐색/수정 헬퍼.
      - `builtins_sys.go` (697줄): 시스템, 메타데이터, UUIDv7, 에러, 조건 논리(ISNULL/COALESCE), 암호화 함수.
    - 전체 25개 패키지 race-free 테스트 100% PASS 검증 완료.
- [x] **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 및 조건 평가 로직 일원화 (`R-3`) (`pkg/engine`, `pkg/execution`, `pkg/planner`)**
  - **배경**: `sqlexec_eval.go`의 단일 테이블 고속 검색과 `execution/builder.go`의 Volcano 플랜 빌더 간에 인덱스 범위 검색 경계(`lowVal`, `highVal`) 추출 및 조건식 파싱 로직이 중복 존재(DRY 위반)하고, Volcano 인덱스 스캔에서 범위 조건(`BETWEEN`, `>=`, `<=`) 처리가 누락되어 있었음.
  - **구현 내용**:
    - `pkg/planner/bounds.go` 공통 모듈 신설:
      - `ParseWhereConditions`: 등호(`=`), `BETWEEN ... AND ...`, 부등호(`>=`, `>`, `<=`, `<`) 및 문자열/괄호 중첩을 고려한 $O(N)$ 단일 패스 파서 및 정규식 패키지 변수 사전 컴파일 적용.
      - `ParseIndexCond`: 단일/복합 인덱스 조건식 및 반정형 JSON 기능 표현식(`doc->>'key' = 'val'`) 바운드 추출 일원화.
      - `ParseValueToType`: `TypeInt`, `TypeFloat`, `TypeDecimal`, `TypeBool`, `TypeDateTime`, `TypeDate`, `TypeTime`, `TypeUUID`, `TypeBinary` 전 데이터 타입 파싱 통합.
      - `BuildCompositePrefixKey`: 복합 인덱스 `\x00` 구분자 접두사 키 생성 엔진 공통화.
      - `FindIndexAndBounds`: 복합 인덱스, 단일 컬럼 인덱스(등치/범위), 함수적 표현식 인덱스 매칭 일원화.
    - `pkg/execution/builder.go`: 중복 함수(`parseIndexCond`, `parseValueByType`, 미사용 `regexp`/`proc` 임포트) 완전 제거 및 `planner.ParseIndexCond` 호출로 단일화 (Volcano 엔진의 범위 인덱스 스캔 완벽 지원).
    - `pkg/engine/sqlexec_eval.go`: 중복 구현된 260줄 이상의 바운드 파싱 로직을 제거하고 `planner.Bounds` 위임으로 슬림화.
    - 단위 테스트 `pkg/planner/bounds_test.go` 완비 및 전체 25개 패키지 race-free 테스트 100% PASS 검증 완료.

### 2.7. Post-1.0 유지보수 및 보류 백로그 (Post-1.0 Maintenance & Deferred Backlog)

1.0 릴리즈의 안정성 확보 및 YAGNI/KISS 원칙(Golden Rule 14, 15)에 따라, 런타임 성능이나 기능 결함이 아니며 회귀 위험이 있는 구조적 리팩토링 및 플랫폼 대체재가 충분한 과제들을 1.0 이후(v1.1+) 유지보수 백로그로 공식 이관·보류합니다.

- [-] **In-Memory OCC 스토리지 트랜잭션 라이프사이클 모듈 분해 (`R-4`) (`pkg/storage/memengine`) - [Post-1.0 이관]**
  - **이관 사유**: 현재 TPC-C 14,800+ TPS 및 25개 전 패키지 레이스 검사 100% PASS(0 Data Race)로 동시성 안정성이 완벽히 입증됨. 단순 파일 분리는 런타임 성능 이득이 없고 오히려 락/세이브포인트 회귀 위험을 유발하므로 1.0 안정화 단계에서는 현상 유지.
  - **향후 계획 (v1.1+)**: `tx_commit.go`, `tx_scan.go`, `tx_savepoint.go` 파일 분해.
- [-] **WiredTiger 스토리지 CGo 바인딩 격리 및 파일 분해 (`R-5`) (`pkg/storage/wiredtiger`) - [Post-1.0 이관]**
  - **이관 사유**: CGo C 헤더 바인딩, 커서 풀링, `-tags wiredtiger` 빌드 제약(`wt_stub.go`)이 정밀하게 페어링되어 안정 동작 중. 1.0 이전 파일 쪼개기는 빌드 태그 복잡성 및 CGo 메모리 관리 실수 위험 대비 실익이 없음.
  - **향후 계획 (v1.1+)**: `wt_cgo.go`, `wt_cursor.go`, `wt_tx.go` 분리.
- [-] **투명한 데이터 암호화 (TDE: In-Kernel AES-256-GCM) (`pkg/storage`) - [보류/Postponed]**
  - **보류 사유**: 데이터베이스 프로세스 내부 소프트웨어 TDE는 클라우드 볼륨(AWS EBS, GCP PD) 및 OS 블록 디바이스(LUKS2)와 중복 암호화되어 NVMe I/O 처리량을 최대 35% 저하시키고 프로세스 메모리 내 키 유출 위험을 유발함. 플랫폼 하드웨어 가속(AES-NI) 기반 스토리지 파일시스템 암호화 표준 가이드([`doc/STORAGE_ENCRYPTION_GUIDE.md`](doc/STORAGE_ENCRYPTION_GUIDE.md)) 수립으로 대체 충족.
- [-] **정형 SQL Parameterized 쿼리 실행 계획 캐시 (`P-9`) (`pkg/engine`, `pkg/planner`) - [보류/Postponed]**
  - **보류 사유**: 고빈도 OLTP 경로는 이미 SP VDBE 바이트코드 캐시(`runtime.PlanCache`), SP 내부 물리 플랜 바인딩(`RelQueryData.CachedPlan`), Prepared Statement 캐시, 단순 PK 스토리지 직결 바이패스(`pkBypassCache`)로 100% 최적화되어 파싱/플래닝이 생략됨. Ad-hoc 임의 SQL 템플릿화는 리터럴 충돌 위험 대비 실익이 낮음.
- [-] **Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`) - [보류/Postponed]**
  - **보류 사유**: CGO 의존성 없는 완전 정적 바이너리 배포를 위한 플러그인 백엔드 구상이었으나, 1.0 프로덕션에서는 검증된 WiredTiger B+Tree 엔진과 순수 Go 초고속 In-Memory OCC 엔진 2대 백엔드 체제에 집중(KISS & DRY).
- [-] **Wasm 런타임 샌드박스 UDF (wazero) (`pkg/extension`) - [보류/Postponed]**
  - **보류 사유**: 고속 Go 네이티브 함수 SPI(`pkg/catalog`) 및 VDBE 바이트코드 기반 T-SQL 저장 프로시저/함수가 이미 완비되어 사용자 정의 확장 요구를 100% 충족함. Wasm 샌드박스는 추가 런타임 의존성(`wazero`), 메모리 복사 마샬링 오버헤드, 샌드박스 리소스 제한 제어 복잡성을 수반하므로 1.0 프로덕션에서는 보류(KISS & YAGNI).
- [-] **LDAP / Active Directory 디렉토리 인증 공급자 (`pkg/auth`) - [보류/Postponed]**
  - **보류 사유**: `AuthProvider` SPI(`pkg/auth/provider.go`)가 완비되어 외부 공급자 플러그인 확장 구조는 갖추어져 있으나, 데이터베이스 커널 내부에서 직접 사내 LDAP/AD 서버로 아웃바운드 네트워크 요청을 발송하는 것은 타임아웃 지연, 연결 풀링 오버헤드, 및 보안 공격 표면을 증가시킴. 엔터프라이즈 SSO/ID 페더레이션은 경계 리버스 프록시(Keycloak, OAuth2-Proxy 등) 또는 표준 DB 인증(SCRAM-SHA-256, TDS NTLM)으로 처리하는 것이 권장되므로 1.0 프로덕션에서는 보류.
- [-] **Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 (`P-10`) (`pkg/execution`) - [보류/Postponed]**
  - **보류 사유**: 고동시성 OLTP 엔진 환경에서 단일 분석 쿼리의 멀티코어 점유는 다른 핵심 트랜잭션의 CPU 기아(Starvation) 및 레이턴시 급증을 유발함. 워커 동기화 및 채널 제어 복잡성 회피를 위해 1.0 프로덕션에서는 보류하고 향후 OLAP 분석 단계(v1.2+)로 이관.
- [-] **`INSTEAD OF` 트리거 런타임 인터셉터 (`pkg/proc/runtime`, `pkg/engine`) - [보류/Postponed]**
  - **보류 사유**: 복합 뷰에 대한 DML 가로채기는 `sqlexec_dml` 핫패스에 불필요한 분기 검사를 유발하고, 가상 `inserted`/`deleted` 테이블 바인딩 및 VDBE 프로시저 재귀 호출 등 복잡도를 증가시킴. 최신 애플리케이션 및 ORM(GORM, Prisma, Hibernate)은 뷰 대신 기본 테이블이나 Stored Procedure를 직접 호출하므로 실익이 낮아 1.0에서는 보류하고 향후 레거시 ERP 마이그레이션 고도화 단계(v1.1+)로 이관.

---

## 3. 최근 완료된 핵심 마일스톤 아카이브 (Recently Completed Milestones)

- [x] **[아키텍처] VDBE Instruction 구조체 16바이트 패킹 (`pkg/proc/bytecode.go`)**
  - `Op(1B) + Flags(1B) + Aux(2B) + A(4B) + B(4B) + C(4B) = Exactly 16 Bytes` 고정 크기 레코드 압축.
  - 64B L1 Cache Line당 정확히 4개 명령어 적재(`unsafe.Sizeof == 16`), VM 디스패치 레이턴시 단축.
- [x] **[아키텍처] SQL 실행 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)**
  - 13개 `sqlexec_*.go` 파일(14,000+ 라인)을 `pkg/engine`으로 완전 이관.
  - PGWire 및 TDS 리스너가 `pkg/api` 의존성 없이 `engine.SQLCoordinator` 단방향 직결 호출 확립.
- [x] **[스토리지] 스토리지 파이프라인 배치형 그룹 커밋 (Group Commit & Fsync 가속)**
  - `memengine` WAL 및 `wiredtiger` 양쪽 엔진 모두에 리더/팔로워 그룹 커밋 및 무경합 패스트패스 구현.
  - `FsyncAlways` 마이크로벤치마크 커밋 지연 100배 단축(0.038ms/op), TPC-C VDBE 14,800+ TPS (422,000+ tpmC) 달성.
- [x] **[SQL 문법] T-SQL & ANSI SQL 7대 문법 및 실행 파이프라인 전면 완비 (`S-1` ~ `S-7`)**
  - **S-1**: `OFFSET ... FETCH NEXT ... ROWS ONLY` 및 `TOP (n)` 정규 페이징 완비.
  - **S-2**: 복합 한정자(`[db].[schema].[tbl].[col]`) 및 인라인 별칭(`FROM accounts basetbl`) 파싱 정밀화.
  - **S-3**: FROM 절 파생 테이블(`FROM (SELECT ...) AS t`) 및 인라인 서브쿼리 AST 노드화, 격리 구체화(`__dt_*`).
  - **S-4**: `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT`, `::` 및 `COALESCE`/`ISNULL` 단락 평가 완비.
  - **S-5**: `CROSS APPLY`, `OUTER APPLY` 및 `FULL OUTER JOIN`, `RIGHT OUTER JOIN` 볼케이노 해시조인 완비.
  - **S-6**: 세션 제어문(`SET NOCOUNT ON`, `SET ANSI_NULLS ON` 등) 정식 AST 파싱 및 세션 플래그 바인딩.
  - **S-7**: `WHERE [NOT] EXISTS` 상관 서브쿼리 Semi/Anti-Join CBO 변환 및 비상관 조기 평가.
- [x] **[CBO] CBO 불리언 트리 재귀 평가 & OR 조건 정밀 선택도 산출 (`C-6`)**
  - `TableStats.EstimateExprSelectivity()` 신설, 동일 컬럼 배타 합($P(A)+P(B)$), 독립 합집합, NOT, IN, BETWEEN 정밀 산출 완비.
- [x] **[보안] 세분화된 SQL 객체 수준 RBAC (Role-Based Access Control) (`pkg/auth`, `pkg/catalog`, `pkg/api`)**
  - `CREATE LOGIN/USER`, `SETUSER`, `GRANT/REVOKE/DENY` (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `EXECUTE`, `CONTROL`, `REFERENCES`) 및 사전 카탈로그 인터셉터(`checkTableAccess`) 완비.
- [x] **[안정성] PlanCache Bounded LRU 캐시 및 메모리 상한 방어 (Golden Rule 29) (`pkg/proc/runtime`)**
  - 무제한 맵을 `container/list` 기반 상한 용량(`DefaultPlanCacheCapacity = 1024`) LRU 캐시로 전면 개편하여 OOM 원천 방지.
- [x] **[T-SQL 내장함수] T-SQL 공식 내장 스칼라 함수 전수 완비 (A~F 그룹)**
  - 에러(`ERROR_MESSAGE`), 문자열(`PATINDEX`, `STUFF`, `CONCAT_WS`), 수학/삼각(`LOG`, `EXP`, `RAND`), 메타데이터(`OBJECT_ID`, `DB_NAME`), 암호화(`HASHBYTES`), 날짜(`DATEFROMPARTS`) 등 완비.
- [x] **[드라이버] 공식 클라이언트 드라이버 E2E 통합 검증**
  - Go 공식 드라이버 `go-mssqldb` (TDS), `lib/pq` (PGWire), `pgx/v5` (Extended Query Pipelining) 100% PASS 검증 완료.
- [x] **[성능] 단순 PK 조회 고속 지문 해싱 & 스토리지 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - 쿼리 문자열 FNV-1a 64비트 지문 해싱 기반 Fast-Path 템플릿 탐지 및 바운디드 캐시(`PKLookupPlan`).
  - 정규 파서/CBO/VDBE 스택을 전면 우회하여 스토리지 엔진(`tx.Get`)으로 직결 반환, DDL 발생 시 자동 무효화(`InvalidatePKBypassCache`).
  - Prometheus 메트릭스 `sqlion_pk_bypass_hits_total` 연동 및 멀티 테이블 한정자/별칭 완벽 지원.
- [x] **[스토리지/디코딩] 바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 INCLUDE (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - 제로 할당 `SkipTupleColumn`, $O(1)$ 오프셋 캐싱 `LazyTuple`, 선별 디코딩 `DecodeTupleSelected` 구현.
  - `CREATE INDEX ... INCLUDE(...)` 문법 파싱, SkipList/IndexData 비키 페이로드 적재 및 CBO `isCovering` Index-Only Scan 극대화.
- [x] **[실행 엔진] 볼케이노 벡터화 청크 반복자 및 1,024건 배치 스트리밍 (`P-4`) (`pkg/execution`, `pkg/engine`)**
  - `ChunkedIterator` 및 `NextChunk` 인터페이스 신설, 1,024건 프리알로케이션 청크 기반 볼케이노 배치 파이프라인 관통.
  - SeqScan, IndexScan, Filter, Project, Limit, Aggregate, Sort, HashJoin, IndexNestedLoopJoin, sqlexec_select 전반 배치 가속 및 100% 하위 호환성 보장.
- [x] **[와이어 프로토콜] TDS NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - TDS 7.3+ 표준 `BuildNBCRow` 신설, NULL 비트맵 패킹 및 0바이트 전송으로 네트워크 대역폭 30%+ 절감.
- [x] **[CBO/조인] 소트-머지 조인 (Sort-Merge Join) 연산자 및 옵티마이저 힌트 (`B-3`, `D-9`) (`pkg/execution`, `pkg/planner`, `pkg/proc`)**
  - $O(M+N)$ 해시리스 스트리밍 반복자 `MergeJoinIterator` 신설 및 `OPTION (HASH/MERGE/LOOP JOIN)`, `WITH (INDEX(...))` 강제 오버라이드.
- [x] **[생태계/GUI] 3대 메이저 GUI 클라이언트(DBeaver, DbGate, Beekeeper Studio) 완벽 호환 (`pkg/engine`, `pkg/wire`, `pkg/wire/tds`)**
  - TDS 및 PGWire 양대 프로토콜 상에서 테이블 브라우징, 인덱스/외래키/프로시저 인트로스펙션, 스토리지 크기 산출 및 메타데이터 조회 전면 완비.
  - DBeaver SQL Server 무한 쿼리 루프(`totalSize`, `usedSize` 컬럼 및 단일 `object_id` 필터링) 해결 및 PostgreSQL 멀티 네임스페이스(`public`, `dbo`) `pg_class` 인트로스펙션 지원.
  - Beekeeper Studio 및 DbGate 테이블 한정 와일드카드 프로젝션(`alias.*`, `schema.table.*`)과 WiredTiger EOF 래핑 방지 래치 적용 완료.
