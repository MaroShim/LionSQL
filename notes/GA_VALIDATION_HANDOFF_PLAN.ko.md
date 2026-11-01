# 1.0 GA 검증 인수인계 계획서 (`notes/GA_VALIDATION_HANDOFF_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01  
> - **상태**: 대기 (Pending) — 다른 모델/세션에서 `/goal`로 실행  
> - **과제 식별 코드**: `GA-VALID-1` (SLT 확장), `GA-VALID-2` (장기 소크), `GA-VALID-3` (도그푸딩), `GA-REGEX-DDL-1` (DDL 정규식 정리, 구현 과제)  
> - **성격**: `GA-VALID-*`는 구현이 아닌 테스트/관찰 과제. 실패 발견 시에만 최소 수정 + 회귀 테스트 추가.

---

## 0. 선행 상태 (2026-11-01 기준)

- 로컬 `main` HEAD는 `6e4a937` 이후. `origin/main` 대비 앞선 커밋은 미푸시 상태.
- 검증 통과 기준선: `go test -tags wiredtiger -count=1 -race ./pkg/...` 23개 패키지 PASS, `./test/slt/...` 10,887 쿼리 PASS, `./scripts/verify_report.sh` 13/13 PASS.
- TPC-C(`tpcc-runtime`, 4W/8워커/5s) 약 675k tpmC, SSB(sf 0.05) Q1.1~Q4.1 총 약 902ms. (회귀 비교용 기준값)

## 1. 공통 규칙 (모든 과제에 적용)

1. 3단계 파이프라인(Build ➔ Test `-race` ➔ Commit)을 준수한다. 문서만 바꾸면 빌드/테스트 생략 가능.
2. 실패를 발견하면 원인 쿼리를 `test/slt/suites/core/*.slt` 또는 해당 패키지 단위 테스트로 먼저 고정(재현 테스트)한 뒤 수정한다.
3. 기대값(expected result)을 엔진 출력에 맞춰 바꾸지 않는다. 정답 오라클은 SQLite 공식 결과 해시다.
4. 진행 상황과 결과는 `TODO.md` 항목과 본 문서 하단 "실행 기록"에 남긴다.
5. 커밋 메시지는 Conventional Commits, 이모지 금지.

## 2. `GA-VALID-1`: SQLLogicTest 잔여 스위트 확장

- 대상: SQLite 공식 `index/*`, `random/*`(expr, aggregates, groupby, select), `evidence/*`.
- 다운로드: `scripts/fetch_slt.sh` 확인 후 사용(현재 `select1~5`만 편입).
- 절차:
  1. 스위트를 하나씩 `test/slt/full_test.go` 패턴으로 편입하고 `-parallel` 러너로 실행.
  2. 실패 쿼리를 분류: (a) 엔진 버그, (b) 의도적 Spec-Out(GEMINI.md 3절), (c) 러너/파서 한계.
  3. (a)만 수정한다. (b)는 `notes/`에 skip 사유와 함께 명시 목록으로 기록한다.
- 완료 기준: 편입한 스위트의 통과율 100% (Spec-Out 제외 목록은 명시), `-race` 0건.

## 3. `GA-VALID-2`: 장기 소크 테스트

- 도구: `cmd/soaktest` (기존 1시간 소크 통과).
- 절차: In-Memory OCC / WiredTiger 각각 24시간, 이후 72시간 연속 실행.
- 판정: RSS/FD/디스크/p99 추세 판정, 잔액 불변식, acknowledged-commit 무결성.
- 병행: `cmd/crashtest`를 라운드 수를 늘려(예: 100라운드/엔진) 재실행.
- 완료 기준: 메모리/FD 단조 증가 없음, 무결성 위반 0건.

## 4. `GA-VALID-3`: 도그푸딩

- 연동 완료된 앱(Gitea, Grafana, Casdoor, Vaultwarden, Metabase, Umami, Answer, Woodpecker, Vikunja, IOTA-SDK) 중 최소 2~3개를 실제 사용 환경에서 1~2주 운영.
- 관찰: 슬로우 쿼리 로그, `/metrics`, 에러 로그, 재시작 후 복구.
- 발견 사항은 재현 쿼리와 함께 `TODO.md`에 등록.

## 5. `GA-REGEX-DDL-1`: DDL 정규식 정리 (구현 과제, 테스트 아님)

- 대상: `pkg/engine/sqlexec_ddl.go` 의 `reCreateTablePrefix`, `reAlter*`, `reCreateView`, `reDropView`, `reCreateStats`, `reDropStats` 등 약 40개.
- 그 외 잔여: `pkg/engine/sqlexec_eval.go`(`reEvalInClause`, `reEvalEqCondition`), `pkg/engine/sqlexec_explain.go`(`reExplain*`). 전체 `grep -rn "regexp" pkg/engine pkg/planner`로 현황 재확인할 것.
- 방식: `PLANNER-AST-2`/`ENGINE-AST-3`와 동일하게 `proc.Lexer` 또는 `sqlutil` 토큰 헬퍼로 교체. 기존 DDL 테스트와 `scripts/verify_report.sh` 전 구간 통과 필수.
- 구현이므로 검증 전용 모델이 아닌 구현 가능한 세션에서 진행할 것.

## 6. 실행 기록

| 일자 | 과제 | 결과 | 비고 |
|------|------|------|------|
| - | - | - | 미실행 |
