# LionSQL Umami 웹 분석 플랫폼 연동 검증 보고서 (INTEG-UMAMI-1)

> - **최종 갱신일**: 2026-11-01
> - **최초 작성일**: 2026-11-01
> - **상태**: 완료 (Completed)
> - **대상 애플리케이션**: Umami v2 (공식 Docker 이미지 `ghcr.io/umami-software/umami:postgresql-latest`, Next.js 16.4.2 기반)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`, SSL 비활성화)
> - **스토리지 엔진**: WiredTiger MVCC 디스크 B+Tree 엔진

---

## 1. 개요 및 목적

Umami는 개인정보를 보호하는 오픈소스 웹 분석 플랫폼으로, Node.js Next.js 프레임워크와 Prisma ORM을 기반으로 동작합니다.
본 검증의 목적은 LionSQL의 PostgreSQL 와이어 프로토콜(PGWire) 및 쿼리 실행 엔진이 Umami의 모든 데이터베이스 요구사항(총 26개의 Prisma 마이그레이션 DDL/DML, 파라미터화된 쿼리, 집계 및 서브쿼리 등)을 Umami 원본 소스코드 수정 없이(Zero Code Change) 100% 충족함을 실증하는 것입니다.

---

## 2. 연동 검증 결과 요약

| 검증 항목 | 대상 및 상세 | 결과 |
| :--- | :--- | :---: |
| **Prisma 스키마 마이그레이션** | Umami 초기화 26개 마이그레이션 (01 ~ 26) 순차 적용 | **성공 (100% 적용)** |
| **Prisma 연결 헬스체크** | `SELECT EXISTS(...), version(), current_setting(...)` 쿼리 해석 | **성공 (200 OK)** |
| **관리자 인증 및 로그인** | `POST /api/auth/login` (admin / umami) JWT 토큰 발급 | **성공 (200 OK)** |
| **웹사이트 등록 (DML)** | `POST /api/websites` (`OUTPUT INSERTED.*` / `RETURNING`) | **성공 (200 OK)** |
| **웹사이트 목록 조회** | `GET /api/websites` (서브쿼리 및 `COUNT(*) AS "_count$_all"`) | **성공 (200 OK)** |
| **웹 트래킹 이벤트 수집** | `POST /api/send` (페이지뷰/세션/방문자 메트릭 인제스트) | **성공 (200 OK)** |
| **웹사이트 통계 분석 쿼리** | `GET /api/websites/{id}/stats` (HAVING, CASE, GROUP BY 1, 2) | **성공 (200 OK)** |
| **대시보드 UI 서빙** | `GET /login` 및 대시보드 웹 UI 렌더링 | **성공 (HTTP 200)** |

---

## 3. 핵심 호환성 이슈 및 해결 내역

### 3.1. Prisma 연결 헬스체크 단독 버전 쿼리 단락 평가
- **현상**: Prisma `schema-engine` 연결 시 `strings.Contains(upper, "VERSION()")` 단순 탐지에 의해 단독 버전 질의로 오인되어 단일 컬럼만 반환하고 스택 오버플로가 발생함.
- **해결**: `isStandaloneVersionQuery`를 통해 단독 버전 질의와 복합 표현식을 엄격히 구분하고, `sqlexec_select.go`에서 `::integer` 타입 캐스팅 및 `EXISTS(SELECT 1 ...)` 평가를 완비함.

### 3.2. ANY($1) 배열 파라미터 타입 추론 및 디코딩
- **현상**: Prisma가 카탈로그 조회 시 `namespace.nspname = ANY ( $1 )` 쿼리를 실행할 때 OID가 0으로 반환되거나 `oid` 타입 불일치 에러 발생.
- **해결**: `inferParamOID`를 통해 `= ANY($1)` 패턴을 `_text` (OID 1009)로 정확히 추론하고, PGWire `decodeBinaryArray` 핸들러를 구현하여 바이너리 텍스트 배열을 완벽히 디코딩함.

### 3.3. CTE 결합 DML 및 USING 절 확장
- **현상**: 마이그레이션 `23_update_session_data`의 `WITH ... DELETE FROM ... USING ...` 구문에서 DML 파서 문법 에러 발생.
- **해결**: `planner.ParseCTEs`를 외부 노출하고, `parseDeleteBody`에 `USING ...` 절을 수용하여 복합 CTE 삭제 쿼리를 안정적으로 처리함.

### 3.4. 파생 테이블 집계 시 특수문자 별칭 및 OID 추론 개선
- **현상**: `GET /api/websites` 호출 시 `SELECT COUNT(*) AS "_count$_all" FROM (SELECT ...)` 쿼리에서 집계 이터레이터의 정규식이 `_count$_all`의 `$` 및 큰따옴표를 인식하지 못하여 `COUNT(*)`가 리터럴로 폴스루되고 문자열 `*`가 반환되어 Prisma에서 `Expected an integer in column '_count$_all', got string: *` 오류 발생.
- **해결**:
  1. `pkg/execution/aggregate.go`의 `parseFields`에서 `sqlutil.SplitAsAlias`를 통해 별칭을 사전 분리하도록 개선하여 특수문자(`$`, `_`, 따옴표)가 포함된 별칭의 집계 함수(`COUNT(*)`, `SUM(...)` 등)를 정확히 인식하도록 수정.
  2. `pkg/wire/server.go`의 `resolveColumnOIDs`에서 `cleanCol`에 `COUNT`가 포함된 경우 OID 20(`int8`)을 우선 할당하도록 보강.

---

## 4. 검증 결론

LionSQL은 Umami v2의 공식 Docker 컨테이너와 완벽하게 연동되며, 복잡한 Prisma 마이그레이션부터 실시간 분석 이벤트 수집 및 집계 쿼리까지 무결성 있게 처리함을 확인하였습니다.
이를 통해 LionSQL의 PGWire 호환성 및 다계층 쿼리 엔진의 안정성이 한 단계 더 향상되었습니다.
