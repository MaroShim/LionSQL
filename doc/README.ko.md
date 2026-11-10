# LionSQL 공식 기술 문서 디렉토리 (`doc/`)

> - **최초 작성일**: 2026-10-24
> - **최종 갱신일**: 2026-11-04
> [English Version](README.md)

LionSQL은 단일 정적 바이너리로 패키징된 고동시성 임베디드 OLTP 데이터베이스 엔진이자 모던 Backend-as-a-Service (BaaS) 플랫폼입니다.  
본 디렉토리는 LionSQL의 **공식 사용자 매뉴얼, 기술 레퍼런스, 시스템 아키텍처 스펙 및 운영 가이드**를 체계적으로 제공합니다.

---

## 1. 핵심 종합 매뉴얼 (Core Manuals - 필독)

LionSQL의 기본 사용법, 전체 SQL/T-SQL 사양, 엔진 아키텍처 및 공인 성능을 망라하는 최상위 기준 문서입니다.

* **[`USER_MANUAL.ko.md`](USER_MANUAL.ko.md)** ([English](USER_MANUAL.md))
  * 설치, 기동, 클라이언트 도구 연동, 데이터베이스 개발 및 운영 유지보수 전 과정을 안내하는 공식 사용자 매뉴얼.
* **[`REFERENCE_MANUAL.ko.md`](REFERENCE_MANUAL.ko.md)** ([English](REFERENCE_MANUAL.md))
  * CLI 플래그, 설정 파일, SQL 문법 및 구문, 데이터 타입, 내장 함수, 가상 카탈로그 뷰, REST API 및 와이어 프로토콜 기술 사양을 총망라하는 공식 레퍼런스 매뉴얼.
* **[`FAQ.ko.md`](FAQ.ko.md)** ([English](FAQ.md))
  * 아키텍처 불변식, 동시성/내구성, 프로토콜 호환성 및 극한의 성능 최적화를 망라하는 LionSQL 핵심 엔지니어링 FAQ 15선.
* **[`ARCHITECTURE.md`](ARCHITECTURE.md)** (English)
  * LionSQL 전 계층 엔드투엔드 아키텍처 청사진 (듀얼 스토리지 백엔드, 2-Tier CBO/VDBE 쿼리 엔진, 고루틴 커넥션 풀러).
* **[`BENCHMARK.md`](BENCHMARK.md)** (English)
  * TPC-C (최대 118만 tpmC), 은행 송금 SP (21.8만 TPS), SQLLogicTest 107개 전수 검증, WiredTiger vs OCC 비교 등 공식 실측 벤치마크.

---

## 2. 개발자 및 연동 가이드 (Developer & Tooling Guides)

클라이언트 도구, 쿼리 개발, SQL 편의 문법 및 검증 스위트 활용 가이드입니다.

* **[`CLIENT_TOOL_INTEGRATION.md`](CLIENT_TOOL_INTEGRATION.md)** (English)
  * SSMS, DBeaver, psql, Beekeeper Studio, Grafana 등 주요 클라이언트 GUI 및 CLI 도구 연동 가이드.
* **[`SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md`](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md)** ([English](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md))
  * LionSQL 다중 방언 SQL 호환성 및 개발자 편의 문법(MySQL 호환 백틱, `DESC`, `SHOW TABLES`, 고ROI 빌트인 함수 등).
* **[`SQLLOGICTEST_GUIDE.ko.md`](SQLLOGICTEST_GUIDE.ko.md)** ([English](SQLLOGICTEST_GUIDE.md))
  * SQLite 공식 SQLLogicTest (55만 쿼리) 및 Babelfish T-SQL 프로시저 스위트 실행·검증 가이드.
* **[`COMPLEX_QUERY_BENCHMARK_GUIDE.ko.md`](COMPLEX_QUERY_BENCHMARK_GUIDE.ko.md)** ([English](COMPLEX_QUERY_BENCHMARK_GUIDE.md))
  * SSB (Star Schema Benchmark) 및 Mini TPC-H 복합 쿼리 실측 및 튜닝 가이드.
* **[`WEB_STUDIO_GUIDE.ko.md`](WEB_STUDIO_GUIDE.ko.md)** ([English](WEB_STUDIO_GUIDE.md))
  * 내장 Web Studio (`TCP :8090 /_/`) 대시보드 및 엑셀 스타일 인라인 레코드 편집 가이드.
* **[`WEB_APP_HOSTING_GUIDE.ko.md`](WEB_APP_HOSTING_GUIDE.ko.md)** ([English](WEB_APP_HOSTING_GUIDE.md))
  * 단일 정적 바이너리로 DB와 SPA 웹 앱(단일 HTML/Flat 폴더)을 동시 호스팅하는 자체 웹서버 및 Same-Origin 활용 가이드.
* **실전 오픈소스 애플리케이션 연동 가이드 (`guide/`)**:
  * 10대 프로덕션 오픈소스 앱(Answer, Vaultwarden, Metabase, Umami, Woodpecker, Vikunja, Gitea, Grafana, Casdoor, IOTA ERP) 연동 가이드는 **[`guide/README.ko.md`](../guide/README.ko.md)**를 참조하십시오.

---

## 3. 운영, 보안 및 서브시스템 (Operations, Security & Subsystems)

고가용성, 보안, 스토리지 암호화, API 및 백엔드 서비스 운영 지침입니다.

* **운영 및 모니터링**:
  * **[`OPERATIONS.md`](OPERATIONS.md)**: 무중단 프로덕션 운영, 시스템 튜닝, 백업 및 복구 파이프라인.
  * **[`HIGH_AVAILABILITY.md`](HIGH_AVAILABILITY.md)**: 1-Writer Multi-Reader WAL 실시간 복제 및 페일오버.
  * **[`METRICS.md`](METRICS.md)**: Prometheus (`/metrics`) 엔드포인트 및 내부 성능 계측 지표.
  * **[`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md)** ([English](TRANSACTION_PERFORMANCE_GUIDE.md)): 고동시성 트랜잭션 성능 최적화 가이드.
  * **[`RELEASE_AUTOMATION_GUIDE.ko.md`](RELEASE_AUTOMATION_GUIDE.ko.md)** ([English](RELEASE_AUTOMATION_GUIDE.md)): 3단계 검증 파이프라인 및 원클릭 릴리즈 자동화 가이드.
* **보안 및 규제 준수**:
  * **[`SECURITY.md`](SECURITY.md)**: SQL RBAC Default Deny, TLS 동적 인증서, 권한 격리 모델.
  * **[`STORAGE_ENCRYPTION_GUIDE.md`](STORAGE_ENCRYPTION_GUIDE.md)**: WiredTiger 저장 데이터 암호화 (AES-256-CBC TDE).
  * **[`ENTERPRISE_AUTHENTICATION_SPI.md`](ENTERPRISE_AUTHENTICATION_SPI.md)**: 엔터프라이즈 인증 SPI 명세.
* **BaaS 및 임베디드 웹 서브시스템**:
  * **[`REST_API_REFERENCE.md`](REST_API_REFERENCE.md)** / **[`REST_API_RECORDS_GUIDE.md`](REST_API_RECORDS_GUIDE.md)**: HTTP REST JSON BaaS API 명세.
  * **[`WEBHOOKS_AND_RLS.md`](WEBHOOKS_AND_RLS.md)**: 실시간 웹훅 및 행 수준 보안(RLS).
  * **[`CRON_AND_MAILER.md`](CRON_AND_MAILER.md)**: 내장 크론 스케줄러 및 SMTP 메일러.
  * **[`CDC_DUCKDB_INTEGRATION.md`](CDC_DUCKDB_INTEGRATION.md)**: 실시간 CDC 이벤트 스트리밍.
  * **[`TRANSACTIONAL_OUTBOX_GUIDE.md`](TRANSACTIONAL_OUTBOX_GUIDE.md)**: 네이티브 아웃박스 브로커 릴레이 (Kafka, RabbitMQ, Webhook).
* **세부 기능 및 스토리지 가이드**:
  * **[`RANGE_PARTITIONING.md`](RANGE_PARTITIONING.md)**: 시계열 자동 롤링 레인지 파티셔닝.
  * **[`JSON_INDEXING_GUIDE.md`](JSON_INDEXING_GUIDE.md)**: 반정형 JSON B-Tree 표현식 인덱싱.
  * **[`SCHEMA_MIGRATION_GUIDE.md`](SCHEMA_MIGRATION_GUIDE.md)** / **[`DATA_IMPORT_AND_MIGRATION.md`](DATA_IMPORT_AND_MIGRATION.md)**: 스키마 마이그레이션.
  * **[`DATA_DICTIONARY.md`](DATA_DICTIONARY.md)**: 시스템 카탈로그 및 가상 뷰 명세.

---

## 4. 코어 엔진 내부 명세 (`doc/internal/`)

LionSQL의 심층 하부 구현, 바이트코드 가상 머신(VDBE), 제로 할당 메모리 아키텍처 및 쿼리 플래너의 기술적 세부 명세입니다.

* **[`internal/README.ko.md`](internal/README.ko.md)** ([English](internal/README.md))
  * **코어 엔진 내부 명세 허브**: 하부 컴포넌트별 7대 핵심 기술 스펙 총괄 색인.
* **스토리지 및 트랜잭션 내부 구조**:
  * **[`internal/STORAGE.md`](internal/STORAGE.md)**: In-Memory OCC vs WiredTiger B+Tree 스토리지 엔진 인터페이스 및 물리 레이아웃.
  * **[`internal/RECORD_BINARY_FORMAT.md`](internal/RECORD_BINARY_FORMAT.md)**: 콤팩트 바이너리 튜플 직렬화 및 고밀도 ZigZag Varint 인코딩 명세.
  * **[`internal/TRANSACTION_CONCURRENCY.md`](internal/TRANSACTION_CONCURRENCY.md)**: 비차단 스냅샷 읽기, 워크스페이스 격리 및 MVCC/OCC 동시성 모델.
* **실행 엔진 및 최적화**:
  * **[`internal/ZERO_ALLOCATION_ARCHITECTURE.md`](internal/ZERO_ALLOCATION_ARCHITECTURE.md)**: 20,000+ ~ 218,000+ TPS를 견인하는 제로 할당 메모리 및 버퍼 풀링 아키텍처.
  * **[`internal/VDBE.md`](internal/VDBE.md)**: SQLite 스타일 T-SQL 바이트코드 VM 명령어 세트(ISA) 및 레지스터 풀링 명세.
  * **[`internal/CBO_VOLCANO.md`](internal/CBO_VOLCANO.md)**: Volcano 반복자 파이프라인 및 64-버킷 균등 깊이 히스토그램 기반 비용 최적화기.
* **네트워킹 및 와이어 프로토콜**:
  * **[`internal/WIRE_PROTOCOLS.md`](internal/WIRE_PROTOCOLS.md)**: PostgreSQL v3 및 TDS 7.x/8.0 바이너리 프레이밍 및 고루틴 커넥션 풀러.

---

## 5. 엔지니어링 히스토리 및 아키텍처 기록 (ADR & Knowledge Base)

* **[`ADR.md`](ADR.md)**: 초기 아키텍처 설계 결정 기록 (ADR-001 ~ ADR-022).
* **[`FOUNDATIONAL_DESIGN_DECISIONS.md`](FOUNDATIONAL_DESIGN_DECISIONS.md)** / **[`WHY_TSQL_GO_WIREDTIGER.ko.md`](WHY_TSQL_GO_WIREDTIGER.ko.md)**: 핵심 기술 스택 채택 배경 및 설계 철학.
* **개발자 이면 맥락 및 아키텍처 결정 기록**:
  * 심층 포스트모텀, 기각된 대안(Negative Knowledge), 릴리스 노트 및 후보 과제(Candidates)은 **[`notes/README.ko.md`](../notes/README.ko.md)**를 참조하십시오.
