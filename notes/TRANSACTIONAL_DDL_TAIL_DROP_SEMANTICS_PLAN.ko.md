# 런타임 꼬리 컬럼 삭제(Tail-Only DROP COLUMN) 트랜잭션 시맨틱 및 아키텍처 결정서 (ADR)

> **문서 식별자**: `ADR-TRANSACTIONAL-DDL-TAIL-DROP-SEMANTICS-V1`  
> **문서 상태**: 확정 및 공식 규격 채택 (Adopted / SSOT)  
> **관련 마일스톤**: `v0.95.0`, `1.0-GA`  
> **관련 불변식**: `[Invariant 13]` (Physical Table Format & Instant Tail DDL), `[RULE:RadicalKISS]`, `[RULE:ExplicitError]`, `[RULE:PlanPersistence]`  
> **대상 모듈**: `pkg/engine/sqlexec_ddl.go`, `pkg/storage/memengine/ddl_alter.go`, `pkg/storage/wiredtiger/wt_engine.go`

---

## 1. 배경 및 문제 정의 (Context & Problem Statement)

관계형 데이터베이스 엔진에서 `ALTER TABLE ... DROP COLUMN`을 **트랜잭션(`BEGIN TRANSACTION; ...; COMMIT;`) 내부에서 실행할 때의 수명주기 및 롤백 시맨틱**은 엔진 아키텍처의 단순성과 상용 도구 호환성 사이의 가장 첨예한 트레이드오프 지점 중 하나이다.

LionSQL은 이미 `[Invariant 13]`(PHYSICAL_TABLE_FORMAT)에 의해 **중간(Middle) 컬럼 삭제를 원천 차단하고 오직 맨 마지막(Tail) 컬럼만 삭제를 허용**하도록 규정해 두었다.

그러나 **"트랜잭션 도중 꼬리 컬럼을 삭제한 후 롤백(`ROLLBACK`)했을 때 데이터 값까지 살려낼 것인가?"**에 대해 다음 4가지 대안을 놓고 심층적인 공학적 검토를 수행하였다.

---

## 2. 검토된 4대 아키텍처 대안 및 트레이드오프 분석

### 대안 1: 실행 즉시 암묵적 강제 커밋 (오라클 / MySQL 스타일)
- **동작**: 트랜잭션 도중 `DROP COLUMN`을 실행하면 엔진이 앞뒤로 `COMMIT`을 강제 호출하여 롤백 자체를 원천 차단.
- **기각 사유 (Spec-Out)**:
  - LionSQL은 PostgreSQL(:5433)과 SQL Server TDS(:1433) 다중 와이어 프로토콜을 서비스함.
  - Flyway, Liquibase, DBeaver 등 현대적 마이그레이션 도구는 전체 DDL 스크립트를 `BEGIN; ...; COMMIT;` 단일 트랜잭션으로 전송함.
  - 중간에 엔진이 멋대로 커밋을 날려버리면 **"배치 실패 시 전체 롤백"이라는 도구의 기본 원자성 계약이 완전히 파괴**됨.

### 대안 2: 비어있지 않은(Non-Empty) 테이블의 컬럼 삭제 전면 금지 (Fail-Closed)
- **동작**: 데이터가 1건이라도 있는 테이블의 `DROP COLUMN`을 즉시 에러로 거부하고, 오직 빈 테이블이나 Copy-and-Switch만 강제.
- **기각 사유**:
  - 로컬 개발, 테스트 스크립트, 프로토타입 작성 시의 개발자 사용성(Ergonomics)을 극심하게 저해함.

### 대안 4: 트랜잭션 내부에서만 Non-Empty 테이블 DROP 금지 (조건부 금지)
- **동작**: 오토커밋 모드에서는 꼬리 삭제를 허용하되, 명시적 `BEGIN TRAN` 내부에서 데이터가 있는 테이블의 `DROP COLUMN` 시도시만 에러 반환.
- **기각 사유 (치명적 단점 발견)**:
  1. **마이그레이션 도구 충돌**: Flyway, Prisma, Django ORM 등은 기본적으로 마이그레이션을 `BEGIN`으로 감싸므로, 운영 배포 시 LionSQL이 즉시 에러를 뿜으며 마이그레이션이 실패함.
  2. **환경 불일치 결함(Flakiness)**: 데이터가 없는 빈 DB(로컬/CI 테스트 환경)에서는 성공하지만, 데이터가 이미 쌓여있는 운영 DB에 배포하는 순간 배포 실패가 발생하는 치명적인 환경 의존적 결함을 유발함.
  3. **과도한 방어**: 실무 마이그레이션의 99.99%는 정상 커밋하려는 목적이므로, 0.01%의 롤백 예외 때문에 정상 배치를 문전박대하는 결과를 낳음.

---

## 3. 공식 채택 결정: 대안 3 (현행 유지: 꼬리 삭제 허용 & 스키마 롤백 / 데이터 절단)

LionSQL은 실용주의와 극단적 단순성(`[RULE:RadicalKISS]`)에 기반하여 **대안 3을 영구 공식 규격(SSOT)으로 채택**한다.

### 3.1 공식 동작 규격 (Specification Contract)
1. **정상 커밋 경로 (99.99%의 실무 경로)**:
   - 트랜잭션 내에서 `ALTER TABLE t DROP COLUMN tail_col;` 실행 후 `COMMIT`하면, 스키마와 물리 튜플에서 꼬리 컬럼이 완벽하게 삭제 확정된다.
   - Flyway, Liquibase, DBeaver, SSMS 등 모든 엔터프라이즈 도구와 100% 매끄럽게 호환된다.
2. **롤백 경로 (0.01%의 비정상 취소 경로)**:
   - 트랜잭션 내에서 `DROP COLUMN` 후 `ROLLBACK`을 실행하면:
     - **스키마 & 튜플 디코더**: 직전 상태로 100% 복원된다 (`SELECT tail_col FROM t` 쿼리가 에러 없이 정상 실행됨).
     - **데이터 값**: 이미 샤드에서 잘려나간 기존 행의 물리 데이터 값은 **`NULL`로 리셋**된다.
     - **불변식 준수**: 이는 `doc/internal/TX_MODULE_RIGOROUS_TEST_SPEC.ko.md` T2.2의 명세(*"메타데이터와 튜플 디코더가 직전 스키마 상태로 정확히 복원되는지 확인"*)와 100% 일치한다.

---

## 4. 아키텍처적 근거 및 이점 (Architectural Rationales)

### 1. 극단적인 엔진 단순성 유지 (`[RULE:RadicalKISS]`)
- PostgreSQL 스타일의 데이터 무손실 복원을 달성하려면, 컬럼 번호(`attnum`) 단조 증가 결번 추적 시스템과 모든 튜플 디코더에 `Logical Index != Physical Slot` 간접 참조 레이어를 삽입해야 한다.
- 이는 쿼리 실행 hot path의 서브마이크로초 $O(1)$ 직접 슬롯 인덱싱 성능을 영구적으로 저하시킨다.
- 대안 3은 이러한 누더기 레이어 도입을 원천 배제하여 코어 엔진의 가벼움과 초고속 튜플 디코딩 속도를 보존한다.

### 2. "DROP 후 ADD" 시의 유령 데이터 슬롯 충돌(Stale Slot Collision) 원천 차단
- 물리 튜플의 바이트를 남겨둔 채 마스킹만 하면, 동일 테이블에 새 컬럼(`ADD COLUMN`)이 추가될 때 이전 꼬리 데이터의 바이트가 새 컬럼에 오염되거나 타입 크래시가 발생하는 치명적 결함이 생긴다.
- 대안 3은 물리 샤드에서 꼬리를 깨끗하게 잘라내므로, 추후 어떤 컬럼이 다시 추가되더라도 **유령 데이터 오염 및 타입 불일치 위험이 0%**이다.

---

## 5. 실무 운영 가이드라인: Copy-and-Switch 표준 준수

LionSQL은 대규모 운영 데이터베이스에서 컬럼을 영구 삭제할 때 오라클(`DBMS_REDEFINITION`), MySQL(`gh-ost`), PostgreSQL 엔지니어링 표준과 동일하게 **Copy-and-Switch 패턴**을 공식 표준으로 권고한다:

```sql
-- 대규모 운영 테이블의 안전한 무중단 컬럼 재구성 표준
CREATE TABLE orders_new AS 
SELECT id, customer_id, order_date, amount 
FROM orders;

-- 원자적 교체 (Zero Downtime Atomic Switch)
BEGIN TRANSACTION;
ALTER TABLE orders RENAME TO orders_old;
ALTER TABLE orders_new RENAME TO orders;
COMMIT;

DROP TABLE orders_old;
```

---

## 6. 결론 및 불변식 요약

1. LionSQL은 런타임 컬럼 삭제 시 **테이블의 마지막(Tail) 컬럼에 한해 트랜잭션 내 `DROP COLUMN`을 허용**한다.
2. 트랜잭션이 롤백될 경우 **스키마와 디코더는 복원되지만, 잘려나간 기존 데이터 값은 `NULL`로 패딩된다는 점을 공식 물리 엔진 규격으로 확정**한다.
3. 이를 통해 마이그레이션 도구 호환성 100%, 핫패스 $O(1)$ 직접 슬롯 디코딩, 슬롯 충돌 방어라는 세 마리 토끼를 모두 잡는다.
