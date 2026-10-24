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

### 대안 B: 1개로 완전 단일화 (모든 쿼리를 VDBE VM으로 일원화, SQLite 스타일)
- **장점**: 모든 쿼리를 단일 VDBE 바이트코드 가상 머신에서 일관되게 실행.
- **영구 배제 사유 (Spec-Out)**:
  1. **"복잡한 쿼리는 빠르게 하는 것보다 옳게(Correctly & Safely) 하는 것이 최우선"**: 다중 조인, 윈도우 함수, 재귀 CTE, 복합 서브쿼리 등 분석형 관계대수는 서브마이크로초 최적화보다 **정확한 연산자 분리, 메모리 경계 제어, NULL 처리 정합성, 디버깅 용이성**이 핵심 가치임.
  2. **컴파일러 비대화(Bloat) 및 취약성**: 이를 전부 바이트코드 명령어로 번역하려면 수만~십만 라인의 거대 컴파일러 상태 머신(SQLite의 select.c/where.c 형태)이 필요하여 엔진이 극도로 무거워지고 버그 발생 표면이 폭증함.
  3. **실질적 성능 이득 부재**: 수십~수백만 건의 데이터를 조인/집계하는 복합 쿼리는 데이터 I/O와 해시 테이블 구축이 실행 시간의 99%를 차지하므로 바이트코드 VM으로의 전환이 가져다주는 실익이 없음.

### 대안 C: 2-Tier 아키텍처 확립 (VDBE Fast-Path + Volcano CBO) 및 1세대 레거시 제거 (영구 확정 채택)
- **선정 근거**: "적재적소(Right Tool for the Right Job)" 원칙에 따라 최적의 두 엔진을 결합:
  1. **단순 쿼리 (Tier 1: VDBE Fast-Path)**: 처리 시간 자체가 수 마이크로초 단위이므로, 플래닝 오버헤드를 0으로 줄이는 **속도(Speed & Zero-Alloc, <1µs)**를 극대화.
  2. **복잡 쿼리 (Tier 2: Volcano CBO)**: 연산자별(HashJoin, Window, Sort, Filter) 격리와 검증이 투명한 **정확성(Correctness, Modularity & Explainability)**을 극대화.
  3. **클린 아키텍처 & Deletion-First**: 버그의 온상이자 800라인에 달했던 1세대 원시 정규식 스캔 코드를 전면 삭제 (골든 룰 #15).
  4. **에러 가시성 확보 (골든 룰 #7)**: 정규 엔진에서 실패 시 엉뚱한 정규식으로 흘려보내지 않고 즉시 명확한 SQL 오류를 반환.

---

## 3. 아키텍처 결정 사항 (Decision & Invariants)

1. **2-Tier 실행 구조 확정**:
   - **Tier 1 (Fast-Path)**: `compileAndExecuteFastSelect` (VDBE 가상머신, 0.5~2µs)
   - **Tier 2 (General CBO Engine)**: `execVolcanoSelect` (Volcano CBO 엔진, 15~50µs)
2. **1세대 레거시 스캔 코드 영구 제거**:
   - `sqlexec_select.go` 내의 `reSelectStmt` 정규식 및 수동 `tx.Scan` 루프 800여 라인을 영구 삭제하고 묘비명 주석을 남김.
3. **에러 전파 불변 규칙**:
   - 플래너 또는 Volcano 엔진에서 에러 발생 시, 침묵하지 않고 즉시 호출자에게 오류를 반환한다.

---

## 4. 향후 재검토에 대한 불변 원칙 가이드 (Future Re-evaluation Guidelines)

후속 엔지니어링이나 차세대 메이저 버전 논의에서 "실행 엔진을 1개로 완전히 합치자"는 의견이 재부상하더라도 다음 원칙에 따라 본 2-Tier 설계를 영구 유지해야 합니다:

1. **대안 1(Volcano 100%)로의 후퇴 불가**:
   - 포인트 룩업(PK 조회)에 CBO 트리 생성 오버헤드를 강제하는 것은 LionSQL의 핵심 정체성인 서브마이크로초 고동시성 OLTP 성능을 희생시키는 퇴보이므로 절대 채택하지 않는다.
2. **대안 2(VDBE 100%)로의 비대화 불가**:
   - 복잡한 쿼리를 무리하게 바이트코드로 컴파일하려는 시도는 컴파일러를 비대화하고 시스템을 무겁게 만들 뿐이다. 복잡한 쿼리에 필요한 미덕은 '미세 지연시간 단축'이 아니라 **'정확한 실행과 디버깅 용이성'**이며, 이는 파이프라인 형태의 Volcano 이터레이터가 가장 잘 수행한다.
3. **결론**:
   - 2-Tier 아키텍처는 완전 통합으로 가기 위한 과도기적 타협안이 아니라, **"속도(VDBE)"와 "정확성(Volcano)"을 각자의 영역에서 극대화한 최종 설계(Permanent Optimal Architecture)**이다.
