# LionSQL 자주 묻는 질문 (FAQ) (`doc/FAQ.ko.md`)

> **문서 버전**: v0.91.0 기준  
> **영문 버전**: [`doc/FAQ.md`](FAQ.md)  
> **핵심 설계 및 아키텍처 문서**: [`notes/ENGINEERING_CONTEXT.ko.md`](../notes/ENGINEERING_CONTEXT.ko.md), [`doc/ARCHITECTURE.md`](ARCHITECTURE.md)

LionSQL의 설계 철학, 내부 아키텍처, 성능 최적화 및 프로토콜 호환성에 대해 자주 묻는 16가지 질문과 답변입니다.

---

## I. 엔진 정체성 및 실행 아키텍처 (Engine & Runtime Architecture)

### Q1. LionSQL은 한마디로 어떤 데이터베이스인가요?
> **A.** Go로 구현된 **단일 독립 정적 바이너리(Single Static Binary)** 형태의 초고동시성 OLTP 데이터베이스 엔진입니다. 외장 커넥션 풀러(PgBouncer), 캐시(Redis), 웹 프록시(Nginx) 같은 사이드카 데몬을 완전히 배제하고, 커넥션 풀러·스토리지·와이어 리스너·Web Studio 및 내장 웹 호스팅(`--app`)을 단 하나의 실행 파일(`lionsql`)로 완결했습니다.

### Q2. 저장 프로시저(Stored Procedure)를 어떻게 Go 엔진에서 서브 마이크로초(µs)로 실행하나요?
> **A.** SQLite 스타일의 **VDBE(Virtual Database Engine) 바이트코드 VM**을 사용합니다. SQL/T-SQL 구문을 파싱한 뒤 일련의 저수준 바이트코드 명령어로 컴파일하고, `sync.Pool` 기반 가상 레지스터 재활용과 배열 슬롯 직접 인덱싱(0.2ns)을 통해 AST 순회 오버헤드와 힙 메모리 할당을 0으로 쥐어짜 냈습니다.

### Q3. 왜 2-Tier 파서(Native Pratt + ANTLR4) 구조를 사용하나요?
> **A.** 95% 이상의 고빈도 DDL/DML 및 트랜잭션 문장은 0-할당 초고속 자체 **Pratt 파서(Tier 1)**로 즉시 파싱하여 서브 마이크로초 지연 시간을 달성합니다. 반면, 복잡한 힌트나 `WITH RECOMPILE` 같은 Microsoft 전용 복합 구문은 공식 T-SQL 문법기인 **ANTLR4 파서(Tier 2)**로 자동 폴백(Fallback)시킵니다. 이를 통해 **극강의 속도와 100% 문법 호환성**을 단일 엔진에서 완벽히 양립시켰습니다.

### Q4. 쿼리 실행기도 2-Tier(VDBE vs Volcano CBO)로 나뉜 이유는 무엇인가요?
> **A.** "모든 쿼리를 하나의 모델로 풀겠다"는 비현실적인 욕심을 버렸기 때문입니다. 단순 OLTP 트랜잭션과 포인트 읽기/쓰기는 바이트코드 VM(Tier 1)을 통해 직결 실행하여 나노~마이크로초 레이턴시를 보장하고, 다중 테이블 조인·CTE·윈도우 함수 등 관계대수적 최적화가 필수적인 복잡 분석 쿼리는 비용 기반 최적화(CBO)를 거쳐 Volcano 파이프라인(Tier 2)으로 라우팅합니다.

### Q5. In-Memory OCC와 WiredTiger 듀얼 엔진은 어떤 차이가 있나요?
> **A.** 
> - **In-Memory OCC**: 완전한 메모리 상주 엔진으로, 단조 버전 카운터를 이용한 낙관적 동시성 제어(OCC)와 백그라운드 WAL/AOF 디스크 복구 기능을 제공합니다 (순수 초고속 트랜잭션 워크로드).
> - **WiredTiger**: MongoDB의 코어 엔진으로 검증된 Lock-Free B+Tree 및 MVCC(Hazard Pointer 보호) 기반 디스크 스토리지 엔진으로, 대규모 영구 데이터셋과 정교한 페이지 캐시 관리를 제공합니다.

---

## II. 동시성 및 ACID 내구성 (Concurrency & ACID Durability)

### Q6. Reads never block writes, writes never block reads의 원리는 무엇인가요?
> **A.** 전통적인 관계형 DB의 2PL(2단계 락) 공유 읽기 락(S-Lock)을 완전히 배제했습니다. 읽기 트랜잭션은 커밋된 스냅샷 버전을 non-blocking으로 즉시 읽고, 쓰기 트랜잭션은 프라이빗 작업 공간(Workspace)에 변경 사항을 버퍼링한 뒤 커밋 시점에만 충돌을 검증(OCC)하거나 MVCC 체인에 반영하므로 읽기와 쓰기가 서로를 블로킹하지 않습니다.

### Q7. 전원이 갑자기 꺼지거나 `kill -9`로 죽어도 데이터 손실이 없음을 어떻게 증명했나요?
> **A.** 자체 구현한 블랙박스 오라클 하네스(`cmd/crashtest`)를 통해 검증했습니다. 커밋 직후 무작위 `SIGKILL`, 복구 도중 재사살(`DOUBLE_KILL`), 로그 꼬리 절단(`TORN_WAL_TAIL`)을 포함한 **100라운드 연속 돌발 사살 환경에서 34만 건의 트랜잭션 전수 무손실 복구 및 잔액 불변식(`SUM(balance)=C`) 100% 무결함**을 실측으로 입증했습니다.

### Q8. 다중 연결 간 데드락(Deadlock)은 어떻게 방지하나요?
> **A.** 교차 잠금 대기 루프를 원천 차단했습니다. In-Memory OCC는 경합 발생 시 블로킹 대기 대신 즉각 Fast-Fail(`ErrWriteConflict`)을 반환하거나 짧은 마이크로 백오프 후 투명 재시도하며, 엔진 전역에서 카노니컬 락 계층 구조(`[RULE:LockHierarchy]`)를 엄격히 준수하여 상호 잠금 대기를 원천 방지합니다.

---

## III. 프로토콜 및 생태계 호환성 (Protocols & Ecosystem)

### Q9. 하나의 DB에 psql과 SSMS로 동시에 접속할 수 있나요?
> **A.** 네. LionSQL은 TCP `:5433`에서 PostgreSQL v3(PGWire) 프로토콜을, TCP `:1433`에서 Microsoft SQL Server(TDS 7.x/8.0) 프로토콜을 동시에 대등한 일급 시민(First-Class Citizen)으로 리스닝합니다. 같은 테이블 데이터를 `psql`로 조회하면서 `sqlcmd`나 SSMS, DBeaver로 동시에 수정할 수 있습니다.

### Q10. `USE <dbname>` 명령어를 지원하나요? (단일 카탈로그 격리 정책)
> **A.** LionSQL은 노이즈 네이버 메모리 누수를 원천 차단하기 위해 프로세스당 1개의 격리된 데이터베이스 카탈로그를 고수합니다. 하지만 SSMS, DBeaver, 각종 ORM 마이그레이션 스크립트와의 100% 호환성을 위해 `USE master`, `USE [SalesDB]` 등의 구문을 **가상 무동작(Tolerant Virtual No-Op)**으로 정상 수용하며 세션 환경 변수를 동기화합니다.

### Q11. 기존 오픈소스 애플리케이션들을 실제로 연결해 쓸 수 있나요?
> **A.** 네. 이미 **Gitea, Grafana, Metabase, Vaultwarden, Casdoor, Umami, Apache Answer, Woodpecker CI, Vikunja, IOTA-SDK ERP** 등 10대 주요 실전 서비스의 마이그레이션과 실운영 쿼리가 순정 컨테이너 소스 수정 0줄로 100% 무오류 연동 및 검증되었습니다. 특히 **Grafana**는 LionSQL의 내장 Prometheus 메트릭스(`:8090/metrics`) 관측성 모니터링과 SQL 실시간 데이터 소스 양방향 모두에서 핵심 파트너로 완벽히 동작합니다.

---

## IV. 성능 및 엔지니어링 불변식 (Performance & Invariants)

### Q12. 단일 노드에서 어느 정도의 성능(TPS / tpmC)이 나오나요?
> **A.** 
> - **In-Memory OCC**: 은행 계좌 이체(TransferFunds SP) 기준 **218,000+ TPS**, TPC-C 기준 스토리지 커널 **1,066,000+ tpmC** (39,700+ TPS), VDBE 런타임 **759,000+ tpmC** (28,100+ TPS), 네트워크 유선(PGWire) **376,000+ tpmC** (13,900+ TPS).
> - **WiredTiger (디스크 B+Tree)**: 계좌 이체 기준 **141,000+ TPS**, TPC-C 기준 스토리지 커널 **1,184,000+ tpmC** (41,900+ TPS), VDBE 런타임 **863,000+ tpmC** (31,700+ TPS), 네트워크 유선(PGWire) **352,000+ tpmC** (13,000+ TPS).  
> 복잡하고 취약한 분산 네트워크 2PC 없이 단일 서버 하드웨어 자원을 극한으로 포화시킵니다.

### Q13. 왜 SQL 파싱 경로에서 정규 표현식(Regex)을 완전히 제거했나요?
> **A.** 정규 표현식(Chomsky Type 3 정규 언어)은 스택 메모리가 없어 괄호가 중첩된 SQL(Type 2 문맥 자유 언어)을 구조적으로 파싱할 수 없기 때문입니다. 정규식을 쓰면 따옴표 안의 리터럴이나 주석을 잘못 자르거나 조건을 누락하는 침묵형 데이터 왜곡이 발생하기 때문에, LionSQL v0.91.0은 전 프로덕션 쿼리 경로의 정규식을 100% 박멸하고 결정론적 상태 머신 토크나이저(`proc.Lexer`, `sqlutil`)로 전면 교체했습니다 (`Invariant 7`).

---

## V. 운영 및 제품 철학 (Operations & Philosophy)

### Q14. 분산 2PC(XA)나 멀티 마스터 샤딩을 지원하지 않는 이유는 무엇인가요?
> **A.** **Scale-Up First & 1-Writer Multi-Reader 철학** 때문입니다. 99%의 엔터프라이즈 환경에서 분산 2PC는 네트워크 지연과 분산 데드락이라는 엄청난 복잡성을 낳습니다. LionSQL은 단일 노드 수직 스케일업으로 초당 수십만 TPS를 처리하고, 복제는 비동기/반동기 WAL 스트리밍 기반 읽기 전용 레플리카로 확장하는 단순하고 견고한 길을 선택했습니다.

### Q15. 1.0 릴리즈를 앞둔 LionSQL의 궁극적인 지향점은 무엇인가요?
> **A.** **"가장 가볍지만 가장 타협 없는 OLTP 엔진"**입니다. 설치와 운영은 SQLite처럼 가볍고 단순하게, 엔터프라이즈 기능(T-SQL 저장 프로시저, ACID 내구성, 멀티 와이어)과 성능은 상용 데이터베이스를 압도하는 자립형 엔진을 완성하는 것이 LionSQL의 목표입니다.

### Q16. 왜 데이터베이스 프로세스 안에 정적 웹 서버와 JS SDK를 내장했나요?
> **A.** 전통적인 3-Tier(웹서버 ➔ API ➔ DB)의 복잡한 인프라 설정과 네트워크 지연, CORS 오버헤드를 원천 제거하기 위함입니다. 단일 포트(`:8090`)에서 웹 프론트엔드와 BaaS REST/SQL API를 함께 서비스하여 루프백 홉 없는 제로-레이턴시 통신을 제공하며, 커널 제로카피 서빙으로 DB 본체의 고동시성 OLTP 트랜잭션 성능에 전혀 영향을 주지 않습니다.
