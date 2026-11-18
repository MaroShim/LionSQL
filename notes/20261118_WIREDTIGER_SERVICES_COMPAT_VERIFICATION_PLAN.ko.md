# 8~9대 엔터프라이즈 서비스 WiredTiger (WT) 초기화 실전 호환성 전수 검증 플랜

## 1. 개요 및 목표
- **목적**: 기존에 In-Memory OCC (`compact`) 엔진 위주로 검증되었던 8~9개 외부 엔터프라이즈 서비스(Apache Answer, Wiki.js, GoCD, Casdoor, Vaultwarden, Umami, Metabase, Vikunja, Woodpecker, Grafana, Gitea)를 **WiredTiger (WT) B+Tree 엔진 기반으로 완전히 새로 초기화(Fresh Init)하여 처음부터 끝까지 전수 실행 및 검증**.
- **배경**:
  - 최근 WiredTiger 엔진에 Go 레벨 `activeWriters` (행 잠금 추적 테이블)가 도입되고 VDBE의 에러 마스킹 버그가 수정됨.
  - 다양한 실전 프레임워크(Go XORM, Node Knex/Objection, Java Hibernate/Liquibase/MyBatis, Rust Diesel, TS Prisma, Clojure JDBC)의 복합 쿼리와 트랜잭션이 순수 WiredTiger 스토리지 위에서 한 치의 오차 없이 완벽하게 구동되는지 전 계층 검증.

## 2. 검증 대상 9개 서비스 목록
1. **Apache Answer**: Go + XORM Q&A 플랫폼 (35개 테이블, 복합 배치 INSERT, IDENTITY 자동 발급, 트랜잭션 격리)
2. **GoCD**: Java 17 + Spring + Liquibase + Hibernate (107개 Changeset, `_BUILDS`/`_STAGES` 뷰, `string_agg` + `<> ALL`)
3. **Wiki.js**: Node.js + Knex.js + Objection.js (42개 테이블, `knex_migrations_lock`, 합성 PK `_row_id` 은닉)
4. **Casdoor**: Go + XORM 인증 플랫폼 (복합 기본키 `(owner, name)`, `VARBINARY` 주소 데이터)
5. **Vaultwarden**: Rust + Diesel ORM 비밀번호 관리자 (`ciphers`, `devices`, `users`)
6. **Umami**: TypeScript + Prisma ORM 웹 분석기 (`session`, `website_event`, 복합 타임스탬프 집계)
7. **Metabase**: Clojure + JDBC 비즈니스 인텔리전스 (가상 카탈로그 뷰 인트로스펙션, 집계 쿼리)
8. **Grafana & Gitea**: Grafana 대시보드 및 Gitea Git 레포지토리 관리 워크플로우
9. **Woodpecker & Vikunja**: 경량 CI/CD 파이프라인 및 할 일 관리 시스템

## 3. 구현 및 검증 세부 단계
1. **WiredTiger 전용 Wire 프로토콜 통합 테스트 구축 (`pkg/wire/service_compat_wt_wire_test.go`)**:
   - `//go:build cgo && wiredtiger`
   - 각 테스트별로 `t.TempDir()` 기반의 완전히 독립된 WiredTiger 데이터 디렉토리 생성 및 오픈.
   - PGWire (`:5433`) 및 TDS (`:1433`) 리스너를 통해 9개 서비스의 실제 클라이언트 워크플로우 전수 검증.
2. **WiredTiger 전용 Engine/Coordinator 통합 테스트 구축 (`pkg/engine/services_wt_compat_test.go`)**:
   - WiredTiger를 백엔드로 하여 각 서비스의 Knex, Liquibase, XORM, Diesel, Prisma 쿼리 시퀀스를 직접 실행하여 검증.
3. **파이프라인 검증 및 결과 보고**:
   - `go test -v -tags wiredtiger -race` 로 전수 PASS 확인.
   - 0 Data Races 및 100% Hermetic Parity 달성 확인.
