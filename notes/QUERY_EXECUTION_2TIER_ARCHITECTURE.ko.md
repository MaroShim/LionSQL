# 아키텍처 결정 레코드 (ADR): 쿼리 실행 엔진 2-Tier 단일화 및 레거시 폴백 제거 (KISS-5 / REV-ENG-008)

- **상태**: 승인 및 적용 완료 (`Accepted & Implemented`)
- **결정일자**: 2026-10-24
- **도메인**: `pkg/engine`, `pkg/execution`, `pkg/proc/runtime`
- **관련 트래커**: `REV-ENG-008` (CODE_REVIEW.ko.md), `KISS-5` (TODO.ko.md)

---

## 1. 배경 및 문제 제기 (Context & Problem Statement)

LionSQL은 기능 확장과 성능 최적화 과정에서 세 가지 서로 다른 `SELECT` 쿼리 실행 경로를 축적해왔습니다:

1. **1세대 원시 실행기 (`sqlexec_select.go:1475~`)**:
   - VDBE 및 Volcano CBO가 없던 프로젝트 극초기에 작성된 거대 정규식(`reSelectStmt`) 기반 테이블 풀스캔, 수동 map 할당, 인메모리 슬라이스 정렬 루프 (약 800라인).
2. **2세대 Volcano CBO 관계대수 엔진 (`pkg/execution`)**:
   - PostgreSQL/MySQL 스타일의 표준 화산형(Volcano) 반복자 트리 엔진. 다중 테이블 JOIN (Hash/Merge/NL), CBO 힌트, 서브쿼리 최적화 지원.
3. **3세대 VDBE 가상 머신 엔진 (`pkg/proc/runtime`)**:
   - SQLite 스타일의 고성능 레지스터 기반 바이트코드 VM. 단일 테이블, 스칼라 함수, 인덱스 룩업을 서브마이크로초(0.5~2µs)로 실행.

이 3가지 경로가 `sqlexec_select.go` 내에서 복잡하게 중첩되면서 다음과 같은 심각한 아키텍처 부채가 발생했습니다:
- **코드 중복 및 비대화**: 약 800라인에 달하는 1세대 원시 코드가 '최후의 폴백'으로 남아 유지보수 부담 가중.
- **에러 은폐 (Silent Error Swallowing - 골든 룰 #7 위반)**: Volcano나 VDBE에서 문법/스키마 오류가 발생했을 때 에러를 즉시 반환하지 않고 구형 정규식으로 흘려보내 잘못된 결과를 내거나 원인 불명의 오류를 유발.
- **실행 의미론 불일치**: 동일한 쿼리가 어떤 경로로 떨어지느냐에 따라 정렬, 집계, 타입 변환 결과가 미묘하게 달라질 위험.

---

## 2. 검토된 대안들 (Considered Alternatives)

### 대안 A: 1개로 완전 단일화 (모든 쿼리를 Volcano CBO로 일원화)
- **장점**: 실행 경로가 단 1개로 완벽히 일원화되어 코드 중복이 0이 됨.
- **탈락 사유**: 단순 쿼리(`SELECT name FROM users WHERE id = 1`)조차 반복자 트리 노드를 매번 힙에 할당하여 레이턴시가 5~10배 악화(0.5µs ➔ 15~50µs)되고 OLTP 벤치마크(TPC-C 16,000+ TPS)가 심각하게 저하됨.

### 대안 B: 1개로 완전 단일화 (모든 쿼리를 VDBE VM으로 일원화)
- **장점**: SQLite/DuckDB 스타일의 단일 바이트코드 엔진으로 초고속 실행 단일화.
- **탈락 사유**: VDBE 컴파일러 내에 비용 기반 조인 트리 최적화(CBO), Hash Join/Merge Join 바이트코드를 전부 재구현해야 하므로 공수가 수 개월로 폭증하고 회귀 위험이 큼.

### 대안 C: 2-Tier 아키텍처 확립 (VDBE Fast-Path + Volcano CBO) 및 1세대 레거시 제거 (채택)
- **장점**:
  1. **성능 무손실**: 단순 단일 테이블/스칼라/인덱스 쿼리는 VDBE 바이트코드 VM(0.5µs)으로 초고속 처리.
  2. **복합 쿼리 최적화**: 다중 JOIN, 서브쿼리, CBO 힌트는 Volcano CBO 반복자 엔진이 표준 관계대수로 처리.
  3. **클린 아키텍처 & Deletion-First**: 버그의 온상이자 800라인에 달했던 1세대 원시 정규식 스캔 코드를 전면 삭제 (골든 룰 #15).
  4. **에러 가시성 확보 (골든 룰 #7)**: 정규 엔진에서 실패 시 엉뚱한 정규식으로 흘려보내지 않고 즉시 명확한 SQL 오류를 반환.

---

## 3. 아키텍처 결정 사항 (Decision & Invariants)

1. **2-Tier 실행 구조 확정**:
   - **Tier 1 (Fast-Path)**: `execVDBESelect` (VDBE 가상머신, 0.5~2µs)
   - **Tier 2 (Complex-Path)**: `execVolcanoSelect` (Volcano CBO 엔진, 15~50µs)
2. **1세대 레거시 스캔 코드 영구 제거**:
   - `sqlexec_select.go` 내의 `reSelectStmt` 정규식 및 수동 `tx.Scan` 루프 800여 라인을 영구 삭제하고 묘비명 주석을 남김.
3. **에러 전파 불변 규칙**:
   - 플래너 또는 Volcano 엔진에서 에러 발생 시, 침묵하지 않고 즉시 호출자에게 오류를 반환한다.
