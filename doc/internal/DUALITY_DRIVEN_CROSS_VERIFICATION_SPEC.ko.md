# Duality-Driven Cross-Verification Methodology
## 이중성 기반 상호 교차 검증 방법론: 4계층 아키텍처적 이중성의 자가 정답지화 명세서 (`ARCH-DUALITY-CROSS-VERIFICATION-1`)

> **부제 (Subtitle)**:  
> *4-Layer Architectural Duality as an Autonomous Ground Truth*  
> *(스스로 정답지가 되는 4계층 아키텍처적 이중성과 차분 검증 엔진)*

---

## 1. 개요 및 설계 철학 (Executive Summary & Philosophy)

### 1.1 배경 및 문제의식 (The Oracle Problem in RDBMS)
데이터베이스 엔진 개발에서 가장 어렵고 비용이 많이 드는 문제는 **"소프트웨어 검증의 오라클 문제(Test Oracle Problem)"**입니다.
- 임의의 SQL 쿼리, 저장 프로시저, 동시성 트랜잭션 시퀀스를 실행했을 때, 반환된 결과셋(튜플, 컬럼 타입, 정렬 순서, NULL 처리, 격리 수준)이 **참(True Ground Truth)**인지 판정하려면 반드시 신뢰할 수 있는 외부 기준(Oracle)이 필요합니다.
- 통상적인 RDBMS 엔지니어링에서는 외부 상용 DB(MS SQL Server, PostgreSQL)를 Docker 컨테이너로 띄워 결과를 맞비교하지만, 이는 다음과 같은 3대 한계를 가집니다:
  1. **네트워크 및 환경 노이즈**: 연결 지연, 인증 실패, 컨테이너 기동 지연으로 인한 테스트 플레이크(Flake).
  2. **환경 비결정론(Non-Determinism)**: 외부 DB의 내부 타이머나 OS 스케줄링 차이로 인한 재현 불가능성.
  3. **폐쇄적 내부 검증 불가**: 외부 DB는 와이어 프로토콜 너머의 결과만 볼 수 있을 뿐, 엔진 내부(VDBE 레지스터 상태, 튜플 코덱 바이트, B-Tree 슬롯 구조)의 정확성을 실시간 검증할 수 없음.

### 1.2 아키텍처적 이중성(Architectural Duality)의 발견
LionSQL의 코드베이스를 조감하면, **최상위 통신 계층부터 최하위 스토리지 디스크 계층까지 모든 핵심 레이어가 완벽하게 독립적인 2중 구현체(Dual Implementations)**로 설계되어 있습니다:
- 와이어 프로토콜: **TDS** vs **PGWire**
- SQL 파서: **Native Pratt Parser** vs **ANTLR4 Full-Grammar**
- 쿼리 실행 엔진: **VDBE Bytecode VM** vs **Volcano CBO Engine**
- 스토리지 엔진: **In-Memory OCC** vs **WiredTiger MVCC B-Tree**

본 명세서는 이 4계층 2중화 구조를 단순한 기능 나열에 두지 않고, **"서로 다른 철학으로 구현된 두 트랙이 실시간으로 서로를 비춰보며 1비트의 시맨틱 오차도 용납하지 않는 자가 치유형 오라클(Self-Checking Living Oracle)"**로 전환하는 공학적 방법론을 확립합니다.

---

## 2. 4계층 이중성 토폴로지 (4-Layer Dual-Track Topology)

LionSQL의 Duality는 시스템의 수직적 파이프라인 전체를 관통합니다:

```
===================================================================================================================
 계층 (Layer)               트랙 A (Track A: Alpha)             트랙 B (Track B: Beta)              쌍대성 보증 대상 (Invariant)
===================================================================================================================
 Layer 1: Wire Protocol      Microsoft TDS 7.x/8.0 (:1433)       PostgreSQL Wire v3.0 (:5433)        CanonicalResultSet 동등성
                             [PacketSQLBatch vs PacketRPC]       [Simple Query 'Q' vs Extended PBE]  (프로토콜 계층 데이터 유실 박멸)
-------------------------------------------------------------------------------------------------------------------
 Layer 2: SQL Parser         Tier 1: Native Pratt Lexer/Parser   Tier 2: ANTLR4 Official Grammar     AST 정규화 트리 일치
                             (Sub-microsecond Zero-Alloc)        (Official T-SQL Formal Grammar)     (문법 파싱 오차/세미콜론 누락 박멸)
-------------------------------------------------------------------------------------------------------------------
 Layer 3: Execution Engine   VDBE Bytecode Virtual Machine       Volcano CBO Query Planner           튜플 집합 및 3VL 시맨틱 일치
                             (SQLite-style Register Bytecode)    (Iterator-based Operator Pipeline)  (최적화 경로별 계산 오차 박멸)
-------------------------------------------------------------------------------------------------------------------
 Layer 4: Storage Engine     In-Memory OCC Storage Engine        WiredTiger MVCC B+Tree Engine       물리 레코드/인덱스 체크섬 일치
                             (Version Counter + Memory Flat WAL) (Hazard Pointer Lock-Free B-Tree)   (엔진별 사일런트 데이터 손상 박멸)
===================================================================================================================
```

---

## 3. 계층별 이중성 교차 검증 방법론 (Duality Verification Pillars)

### 3.1 Layer 1: Wire Protocol Cross-Symmetry Verification (와이어 프로토콜 이중성)
와이어 프로토콜의 이중성은 **포트 간 이중성(Inter-Protocol)**과 **포트 내 호출 경로 간 이중성(Intra-Protocol)**이라는 2차원 4-분면 매트릭스로 검증됩니다.

#### [검증 불변식 (Wire Symmetry Invariant)]
동일한 쿼리 또는 프로시저 $Q$에 대해, 클라이언트 드라이버가 사용하는 경로와 무관하게 수신되는 결과셋의 정규화 표현 $\mathcal{C}(Q)$는 완전히 동일해야 합니다:
$$\mathcal{C}(Q) = \mathcal{C}(\text{TDS}_{\text{Batch}}) \equiv \mathcal{C}(\text{TDS}_{\text{RPC}}) \equiv \mathcal{C}(\text{PGWire}_{\text{Simple}}) \equiv \mathcal{C}(\text{PGWire}_{\text{Extended}})$$

* **정규화 결과셋 구조 (`CanonicalResultSet`)**:
  ```go
  type CanonicalResultSet struct {
      Columns []string    // 대소문자 무시 컬럼명
      Rows    [][]string  // 표준 문자열/NULL 정규화 레코드
  }
  ```
* **적용 사례 (`test/spec_defects/gate_protocol_symmetry_test.go`)**:
  - 단일 결과셋 및 매개변수 타입 바인딩 (`INT`, `VARCHAR`, `NULL`).
  - 다중 결과셋(`rows.NextResultSet()`)의 순차 스트리밍 완결성.
  - 제로-파라미터 프로시저 및 후행 세미콜론 정규화.
  - 클라이언트 트랜잭션 내 프로시저 DML의 `COMMIT` 및 `ROLLBACK` 원자성.

---

### 3.2 Layer 2: Dual-Parser Differential Equivalence (파서 이중성)
LionSQL은 1마이크로초 이내에 완료되는 자체 고속 재귀하향 Pratt 파서(Tier 1)와 공식 ANTLR4 풀-문법 파서(Tier 2)를 가집니다.

#### [검증 불변식 (Parser AST Invariant)]
$$\text{AST}_{\text{Normalized}}(\text{Pratt}(Q)) \equiv \text{AST}_{\text{Normalized}}(\text{ANTLR4}(Q))$$
* **차분 파싱 엔진 (Differential Parser Harness)**:
  - 테스트 및 디버그 모드(`LIONSQL_PARSER_ORACLE=1`)에서 유입되는 모든 쿼리를 두 파서에 동시 주입.
  - Pratt 파서가 정상 파싱한 AST의 Statement 종류, 대상 테이블, 조건절 트리, 바인딩 파라미터가 ANTLR4의 생성 트리와 정확히 일치하는지 비교.
  - Pratt 파서가 처리하지 못하는 복잡한 힌트나 특수 구문은 조용히 잘못된 트리를 만들지 않고 즉시 Fail-Closed되어 ANTLR4로 폴백되는지 검증.

---

### 3.3 Layer 3: Dual-Engine Volcano vs VDBE Parity (실행 엔진 이중성)
LionSQL은 SQLite VDBE 계통의 레지스터 바이트코드 가상머신과 Go 기반 화산 모델(Volcano Iterator) CBO 실행기를 모두 탑재하고 있습니다.

#### [검증 불변식 (Execution Semantics Invariant)]
$$\text{TupleStream}(\text{VDBE}(Q)) \equiv \text{TupleStream}(\text{Volcano}(Q))$$
* **시맨틱 동등성 검증 영역**:
  - `NULL` 정렬 순서 (`ORDER BY col ASC/DESC` 시 NULL의 위치 보장).
  - 3치 논리(Three-Valued Logic: `TRUE`, `FALSE`, `UNKNOWN`) 조건 평가.
  - 정수 오버플로우 방어 및 부동소수점 소수점 절삭 정밀도.
  - 묵시적 데이터 타입 변환(Coercion) 규칙 일치.

---

### 3.4 Layer 4: Storage Engine Twin-Run Verification (스토리지 엔진 이중성)
LionSQL은 메모리 특화 `In-Memory OCC (FlatRecord + WAL)`와 디스크 영속성 특화 `WiredTiger MVCC B-Tree`를 가집니다.

#### [검증 불변식 (Storage ACID & Index Invariant)]
동일한 트랜잭션 시퀀스 $\mathcal{T} = [t_1, t_2, \dots, t_n]$를 두 스토리지에 트윈 실행했을 때:
$$\text{StateCheckSum}(\text{MemEngine}(\mathcal{T})) \equiv \text{StateCheckSum}(\text{WiredTiger}(\mathcal{T}))$$
* **검증 매트릭스**:
  - 동시성 충돌(`ErrWriteConflict` vs `WT_ROLLBACK`) 발생 지점의 격리 동등성.
  - 복합 보조 인덱스(`IndexScanDesc`) 역방향 B-Tree 스캔 정렬 순서 일치.
  - DDL `ADD COLUMN` 후 이전 레코드 디코딩 시 기본값(`DEFAULT`) 패딩 일치.
  - 트랜잭션 `Rollback()` 후 스토리지 내 변경 사항 완전 소멸(Zero Residue) 검증.

---

### 3.5 SLT(SQLLogicTest) 6대 Duality 확장 규격 (Duality-Driven SQLLogicTest Architecture)
SQLLogicTest(SLT)는 수만 개의 복잡한 SQL 쿼리와 결과셋이 검증된 정답 코퍼스(Corpus)를 보유하고 있습니다. 본 절에서는 `pkg/slt.Executor` 인터페이스를 **Duality 교차 검증 러너**로 확장하여, 수만 개의 실전 쿼리를 통해 엔진 전 계층의 2중화를 전수 검증하는 6대 실행 시나리오를 명세합니다.

```
                                    [ SLT 코퍼스 쿼리 Q ]
                                              │
                    ┌─────────────────────────┼─────────────────────────┐
                    ▼                         ▼                         ▼
         [ 시나리오 A: Dual-Wire ]  [ 시나리오 B: Dual-Storage ] [ 시나리오 C: Dual-Parser ]
           TDS (:1433) vs PGWire      MemEngine vs WiredTiger      Pratt vs ANTLR4
                    │                         │                         │
                    └─────────────────────────┼─────────────────────────┘
                                              ▼
                             assertCanonicalSymmetry(A, B)
                           (수만 개 쿼리 전수 1:1 실시간 대조)
```

#### 시나리오 A: 와이어 프로토콜 대칭 SLT (`DualWireExecutor` / `-duality=wire`, `SLT-DUALITY-WIRE-1`)
* **설계 원리**:
  - SLT 러너가 내부 메모리 함수(`coord.ExecuteSessionSQL`)를 호출하는 대신, 로컬에 기동된 **TDS 서버(:1433, `go-mssqldb`)와 PGWire 서버(:5433, `pgx`) 양쪽 커넥션으로 동일 쿼리를 동시 발행**.
  - 수신된 두 프로토콜의 결과셋(컬럼명, 데이터 행, NULL 표현, 에러 코드)을 1:1로 맞비교.
* **적출 대상 결함**:
  - 수천 개의 복잡한 `CASE WHEN`, `JOIN`, 서브쿼리, 윈도우 함수가 TDS TDS 패킷 직렬화와 PGWire DataRow 스트림에서 단 1건의 포맷팅 오차나 절삭 없이 클라이언트에 도달하는지 전수 검증.
  - 다중 문장 세미콜론 일괄 전송 시 PGWire Extended Query 포털 누락 및 TDS DONEINPROC 토큰 누락 영구 박멸.

#### 시나리오 B: 스토리지 엔진 대칭 SLT (`DualStorageExecutor` / `-duality=storage`, `SLT-DUALITY-STORAGE-1`)
* **설계 원리**:
  - 동일한 SLT 스크립트를 `In-Memory OCC` 기반 인스턴스와 `WiredTiger B+Tree` 기반 인스턴스 양쪽에 트윈(Twin)으로 주입.
  - 모든 `statement ok` 및 `query` 레코드 실행 후, 두 엔진의 반환값 및 최종 테이블/인덱스 체크섬을 실시간 대조.
* **적출 대상 결함**:
  - 수천 번의 DDL/DML 수행 후 두 엔진의 레코드와 복합 보조 인덱스 정렬 순서(`IndexScanDesc`)가 100% 일치하는지 비교.
  - OCC 메모리 코덱의 비트맵 계산 오프셋 오류 및 WiredTiger CGo 바인딩의 미세 결함을 런타임에 즉시 탐지.

#### 시나리오 C: 2-Tier 파서 대칭 SLT (`DualParserExecutor` / `-duality=parser`, `SLT-DUALITY-PARSER-1`)
* **설계 원리**:
  - SLT에 기록된 수만 개의 쿼리를 파싱할 때, Pratt 파서와 ANTLR4 파서 양쪽에 모두 컴파일을 수행.
  - 두 파서가 생성한 AST 트리의 핵심 시맨틱 노드(구문 종류, 대상 테이블, 연산자 우선순위)를 비교.
* **적출 대상 결함**:
  - Pratt 파서가 공식 문법과 다르게 잘못 연산자 우선순위를 매기거나 조용히 조건을 누락하는 사일런트 파싱 결함(Silent Parsing Defect)을 100% 감지.

#### 시나리오 D: T-SQL 저장 프로시저 30개 핵심 스위트 와이어 대칭 SLT (`SLT-DUALITY-TSQL-WIRE-1`)
* **설계 원리**:
  - `test/slt_proc/01` ~ `30`에 이르는 30개 공식 T-SQL 프로시저 스위트 전체를 **TDS 네트워크 와이어(`:1433`)**를 통해 드라이버로 원격 호출한 결과와, **In-Process Coordinator** 내부 직접 실행 결과를 1:1 교차 대조.
* **적출 대상 결함**:
  - 프로시저 실행 도중 다중 결과셋 반환(`Multi-ResultSet`), 출력 매개변수(`OUTPUT`), 반환 코드(`RETURN @val`), 에러 핸들링(`TRY...CATCH`)의 TDS 토큰 스트리밍 직렬화 정합성 전수 검증.

#### 시나리오 E: 프로토콜 내부 듀얼 경로 대칭 SLT (`SLT-DUALITY-DUALPATH-1`)
* **설계 원리**:
  - TDS 내에서 동일 쿼리를 **SQL Batch 패킷**으로 전송한 결과와 **RPC `sp_executesql`**로 전송한 결과 대조.
  - PGWire 내에서 동일 쿼리를 **Simple Query ('Q')**로 전송한 결과와 **Extended Query (Parse-Bind-Execute 'PBE')**로 전송한 결과를 1:1 대조.
* **적출 대상 결함**:
  - 단일 프로토콜 내에서 클라이언트 드라이버나 ORM(GORM, Hibernate, Dapper, EF Core)이 선택하는 전송 모드에 따라 결과셋이나 타입 캐스팅이 달라지는 불일치 결함 박멸.

#### 시나리오 F: Cold Compilation vs Hot PlanCache 대칭 SLT (`SLT-DUALITY-PLANCACHE-1`)
* **설계 원리**:
  - 1회차 실행(Cold: 신규 파싱 및 쿼리 플랜 신규 컴파일) 결과셋과, 플랜 캐시에 적재된 후 2회차 이후 실행(Hot: Plan Cache Hit 재사용) 결과셋을 대조.
* **적출 대상 결함**:
  - 쿼리 실행기가 캐시된 실행 계획(Plan AST / Bytecode Program)의 내부 상태(레지스터, 필터 조건자, 커서 컨텍스트)를 실행 도중 오염(Mutate)시켜 후속 실행에서 잘못된 결과를 반환하는 캐시 상태 오염 결함 원천 차단.

#### 올-인-원 통합 모드 (`-duality=all`)
* CLI 및 테스트 플래그 `go test ./test/slt -duality=all`을 통해 A부터 F까지 전 시나리오를 유기적으로 결합하여, **파서 ➔ 플랜 캐시 ➔ 실행 엔진 ➔ 스토리지 ➔ 와이어 프로토콜에 이르는 4계층 전수 교차 검증**을 자동 완주.

---

## 4. 자가 치유형 오라클의 공학적 비교 우위

| 비교 항목 | 전통적 외부 DB 오라클 방식 | LionSQL Duality 교차 검증 방식 |
| :--- | :--- | :--- |
| **외부 의존성** | Docker, 외부 네트워크, MS SQL Server/PG 설치 필수 | **완전 자립(Self-Contained) 단일 정적 바이너리** |
| **테스트 실행 속도** | 외부 프로세스 호출 및 TCP 지연 (초 단위) | **인-메모리 직접 연결 및 고속 루프백 (밀리초 단위)** |
| **테스트 결정론** | OS 타이밍 및 외부 DB 버전에 따른 플레이크 발생 | **100% Hermetic 결정론적 재현 가능 (`[RULE:DeterministicTesting]`)** |
| **검증 커버리지** | 와이어 최종 결과만 관측 가능 (Black-box) | **와이어/파서/VM/스토리지 전 계층 관측 (White-box)** |
| **결함 탐지 감도** | 대규모 크래시 위주 감지 | **1비트의 타입 OID/정렬 오차까지 즉시 감지** |

---

## 5. 결론 및 향후 로드맵 (Roadmap to 1.0-GA)

본 **이중성 기반 상호 교차 검증 방법론(Duality-Driven Cross-Verification Methodology)**은 LionSQL 1.0 프로덕션 릴리스의 핵심 품질 보증 축으로 영구 유지됩니다.

1. **Gate 1 (배포 차단 게이트 완성)**:
   - `test/spec_defects/gate_protocol_symmetry_test.go`를 `scripts/release.sh` 파이프라인의 핵심 검증 단계로 영구 편입하여, 와이어 대칭성이 깨지면 릴리스 빌드 원천 차단 (`GATE-PROTOCOL-SYMMETRY-1`, 완료).
2. **Gate 2 (SLT 6대 Duality 러너 체계 구축)**:
   - `SLT-DUALITY-WIRE-1`: TDS vs PGWire 차분 SQLLogicTest 러너 구현 (Scenario A, 완료).
   - `SLT-DUALITY-STORAGE-1`: MemEngine vs WiredTiger 트윈 스토리지 SQLLogicTest 러너 구현 (Scenario B).
   - `SLT-DUALITY-PARSER-1`: Pratt vs ANTLR4 차분 파서 동등성 SQLLogicTest 러너 구현 (Scenario C).
   - `SLT-DUALITY-TSQL-WIRE-1`: T-SQL 저장 프로시저 30개 핵심 스위트 와이어 대칭 러너 구현 (Scenario D).
   - `SLT-DUALITY-DUALPATH-1`: 프로토콜 내부 듀얼 경로(TDS Batch/RPC, PGWire Simple/Extended) 대칭 러너 구현 (Scenario E).
   - `SLT-DUALITY-PLANCACHE-1`: Cold Compilation vs Hot PlanCache 대칭 러너 구현 (Scenario F).
3. **Gate 3 (소크 및 카오스 연계)**:
   - 스프린트 과제 `DURA-SOAK-1`(24h~72h 장기 소크 테스트)에 Layer 4 스토리지 트윈 검증기(MemEngine vs WiredTiger)를 결합하여 고동시성 메모리 누수 및 데이터 정합성 지속 감시.
4. **영구 불변식 등록**:
   - `GEMINI.md` 및 프로젝트 아키텍처 가이드라인에 **[RULE:DualitySymmetry]**를 명문화하여 향후 추가되는 모든 와이어 프로토콜 기능과 스토리지 기능이 대칭성 검증을 필수로 통과하도록 강제.

---

## 6. 실전 결함 적출 사례 연구 (Real-World Case Studies Uncovered by Duality SLT)

본 절에서는 `test/slt` 32개 핵심 스위트에 대해 **시나리오 A(Dual-Wire SLT: TDS `:1433` vs PGWire `:5433`)** 러너를 최초 가동했을 때, 기존 단일 와이어 테스트나 개별 단위 테스트에서는 발견되지 않고 은폐되어 있던 2건의 중대 결함이 실시간 교차 검증을 통해 어떻게 즉각 적출(Pinpointed)되었는지 그 실전 사례를 기록한다.

```
+---------------------------------------------------------------------------------------+
|                 Dual-Wire SQLLogicTest Runner (Scenario A Execution)                 |
|                                                                                       |
|   [Query] ---> Track 1: TDS (:1433)   ---> Output Rowset A                            |
|           ---> Track 2: PGWire (:5433) ---> Output Rowset B                           |
|                                                      |                                |
|                        [Duality Comparator] <--------+                                |
|                                 |                                                     |
|                   +-------------+-------------+                                       |
|                   |                           |                                       |
|         Rowset A == Rowset B        Rowset A != Rowset B                              |
|             [100% PASS]               [MISMATCH DETECTED]                             |
|                                       - Case 1: agg_stress.slt:68                     |
|                                       - Case 2: join_advanced.slt:48                  |
+---------------------------------------------------------------------------------------+
```

---

### 사례 1. 중첩 집계 함수 표현식의 점(`.`) 분할로 인한 식별자 절단 결함
* **발견 위치**: `pkg/wire/tds/token.go:CleanTDSColumnName` 및 `pkg/wire/param_rules.go` / `server.go`
* **적출 트리거**: `test/slt/core/agg_stress.slt:68`
* **실행 쿼리**:
  ```sql
  SELECT d.dept_name, COUNT(o.order_id), COALESCE(SUM(o.amount), 0)
  FROM slt_sales_dept d
  LEFT JOIN slt_sales_orders o ON d.dept_id = o.dept_id
  GROUP BY d.dept_name
  ORDER BY d.dept_name;
  ```

#### 1) Duality Observer 관측 불일치 (Discrepancy)
* **TDS Track (`:1433`)**: 컬럼 메타데이터를 `"amount), 0)"`로 절단하여 전송하고, 내부 행 맵에서도 절단된 키로 값을 찾아 `"1000"` 반환.
* **PGWire Track (`:5433`)**: 정상적인 전체 함수 표현식 `COALESCE(SUM(o.amount), 0)`을 컬럼명으로 유지.
* **러너 적출 메시지**:
  ```text
  --- FAIL: TestSQLLogicTest_Duality_DualWire/core/agg_stress.slt (0.01s)
      slt_duality_test.go:76: [core/agg_stress.slt:68] Duality Dual-Wire mismatch failure:
        [DUALITY-MISMATCH] TDS_1433 vs PGWire_5433:
          Divergence: Row 0 Col "amount), 0)" Value mismatch:
            TDS_1433:    "1000" (int64)
            PGWire_5433: "NULL" (<nil>)
  ```

#### 2) 근본 원인 분석 (Root Cause Analysis)
* TDS 프로토콜 핸들러의 컬럼명 정리 함수인 `CleanTDSColumnName` 및 PGWire 파라미터 규칙에서 문맥을 무시한 단순 점(`.`) 분할이 적용되어 있었음:
  ```go
  // [결함 코드] pkg/wire/tds/token.go
  func CleanTDSColumnName(name string) string {
      name = strings.TrimSpace(name)
      if name == "" {
          return name
      }
      name = strings.Trim(name, "[]\"'`")
      if dot := strings.LastIndex(name, "."); dot != -1 {
          name = strings.Trim(name[dot+1:], "[]\"'`") // [버그] 점(.) 뒤를 컬럼명으로 간주
      }
      return name
  }
  ```
* 단순 테이블 컬럼인 `o.amount`에서는 점(`.`)을 분할하여 `amount`를 취하는 것이 정상 작동함.
* 그러나 테이블 한정 컬럼이 `COALESCE(SUM(o.amount), 0)`와 같이 **중첩 함수 표현식 내부**에 포함되어 있을 때, `strings.LastIndex(name, ".")`를 수행하면서 점(`.`) 이후의 문자열인 `amount), 0)`가 컬럼 메타데이터 이름으로 처참하게 잘려나감.
* TDS 레이어는 전송 메타데이터와 행 조회 모두에서 이 절단 함수를 호출하여 우연히 일치하는 바람에 단독 테스트에서는 결함이 은폐되었으나, 전체 표현식을 보존하던 PGWire와의 교차 검증에서 **컬럼명 절단 및 셀 값 불일치**가 수면 위로 즉각 적출됨.
* **위반 불변식**: `[Invariant 12: Context-Blind strings.* SQL Parsing Prohibition & Mandatory sqlutil Primacy]`.

#### 3) 단일 테스트에서 은폐되었던 이유와 Duality의 해결
* 단일 프로토콜(TDS 전용) 테스트에서는 송신 측과 수신 측이 모두 동일하게 오염된 키(`amount), 0)`)를 공유하여 정상 통과된 것처럼 가장되었음.
* Duality 러너는 독립된 두 와이어 프로토콜(TDS와 PGWire)로 동일 쿼리를 동시 주입하고 결과셋 스키마와 셀 값을 1:1로 실시간 대조함으로써, 암묵적으로 은폐되어 있던 식별자 절단 결함을 완벽히 적출함.
* **조치 방안**: 식별자/표현식에 괄호(`(`)가 존재하는 경우 단순 식별자가 아닌 복합 함수/연산 표현식이므로 점(`.`) 분할을 안전하게 우회하도록 가드 추가.

---

### 사례 2. 셀프 조인 시 중복 프로젝션 컬럼명의 강제 리네이밍 결함
* **발견 위치**: `pkg/execution/project.go:addField`
* **적출 트리거**: `test/slt/core/join_advanced.slt:48`
* **실행 쿼리**:
  ```sql
  SELECT e.emp_id, e.emp_name, m.emp_name
  FROM slt_emp2 e
  JOIN slt_emp2 m ON e.mgr_id = m.emp_id;
  ```

#### 1) Duality Observer 관측 불일치 (Discrepancy)
* **TDS Track (`:1433`)**: 쿼리에 명시된 프로젝션 컬럼명(`emp_name`) 그대로 반환 -> 클라이언트 행 스캔 정상 완료 (`"Kim"` 반환).
* **PGWire Track (`:5433`)**: Volcano 실행 엔진이 두 번째 컬럼을 `m.emp_name`으로 강제 리네이밍 -> TDS와 컬럼명 불일치로 인해 `"NULL"` 반환.
* **러너 적출 메시지**:
  ```text
  --- FAIL: TestSQLLogicTest_Duality_DualWire/core/join_advanced.slt (0.01s)
      slt_duality_test.go:76: [core/join_advanced.slt:48] Duality Dual-Wire mismatch failure:
        [DUALITY-MISMATCH] TDS_1433 vs PGWire_5433:
          Divergence: Row 0 Col "emp_name#1" Value mismatch:
            TDS_1433:    "Kim" (string)
            PGWire_5433: "NULL" (<nil>)
  ```

#### 2) 근본 원인 분석 (Root Cause Analysis)
* Volcano 실행 엔진의 프로젝션 이터레이터(`pkg/execution/project.go`)는 결과 스키마에 동일한 컬럼명이 2개 이상 존재하면 이를 임의로 고유화(Uniquify)하려는 레거시 휴리스틱이 잔존해 있었음:
  ```go
  // [결함 코드] pkg/execution/project.go
  addField := func(expr, alias string) {
      schemaAlias := alias
      for _, existing := range p.schema {
          if strings.EqualFold(existing, schemaAlias) {
              cleanExpr := sqlutil.CleanIdentifier(expr)
              if cleanExpr != "" && !strings.EqualFold(cleanExpr, schemaAlias) {
                  schemaAlias = cleanExpr // [버그] m.emp_name으로 강제 리네이밍
              } else {
                  schemaAlias = fmt.Sprintf("%s_%d", alias, len(p.schema)+1)
              }
              break
          }
      }
      // ...
      p.schema = append(p.schema, schemaAlias)
  }
  ```
* 셀프 조인 쿼리 `SELECT e.emp_id, e.emp_name, m.emp_name`에서 세 번째 컬럼 `m.emp_name`은 원래 `emp_name`이라는 컬럼 레이블을 가져야 하지만, 이미 첫 번째 `emp_name`이 스키마에 등록되어 있다는 이유로 엔진이 임의로 `m.emp_name`으로 변조함.
* 그러나 **ANSI SQL 표준 및 PostgreSQL, Microsoft SQL Server 표준 규격**에서는 프로젝션 절에 중복된 컬럼명이 존재하는 것을 완전히 허용하며, 엔진이 임의로 결과셋 컬럼 레이블을 개작해서는 안 됨.
* 결과셋 메타데이터의 컬럼명이 TDS는 `emp_name`, PGWire(Volcano)는 `m.emp_name`으로 분기됨에 따라 와이어 프로토콜 대칭성이 파괴됨.

#### 3) 단일 테스트에서 은폐되었던 이유와 Duality의 해결
* 단일 프로토콜 환경에서는 엔진이 내부적으로 리네이밍을 해도 그 상태 그대로 클라이언트에 전달되어 얼핏 오류 없이 지나치는 경우가 많음.
* 하지만 TDS 드라이버와 PGWire 드라이버가 동일한 셀프 조인 쿼리를 각각 실행하고 그 결과셋 메타데이터와 셀 데이터를 교차 검증했을 때, 컬럼 명칭과 튜플 값의 위치 불일치가 극명하게 드러남.
* **조치 방안**: 과거 `map[string]any` 충돌 방지용으로 추가되었던 불필요한 스키마 강제 리네이밍 로직을 전면 제거하고, 쿼리가 명시한 프로젝션 컬럼명을 충실하게 보존하도록 수정 (`[RULE:RadicalKISS]`).

---

### 결론: 이중화 아키텍처가 증명한 결함 발견 가치
위 2건의 사례는 Duality-Driven 방법론이 단순한 "추가 테스트"를 넘어, **기존의 모든 정적 테스트와 단일 프로토콜 블랙박스 테스트를 뚫고 지나갔던 심층적인 프로토콜/엔진 레벨의 불일치를 자동으로 수면 위로 끌어올리는 강력한 돋보기**임을 입증한다.
LionSQL은 본 방법론을 통해 SQLite 이상의 자가 치유형 품질 검증 체계를 확립하였다.
