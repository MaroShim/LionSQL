# LionSQL 코드 리뷰 및 결함 감사 트래커 (CODE_REVIEW.ko.md)

본 문서는 LionSQL의 전체 코드베이스를 대상으로 진행되는 **정밀 정적 분석 ➔ 이슈 등록 ➔ 회귀 테스트 및 수정 ➔ 3단계 검증(Build & Race-Test & Commit)** 루프의 진행 상황과 백로그를 추적하는 전용 트래커입니다.

---

## 1. 검증 파이프라인 원칙 (3-Stage Invariant)

모든 이슈 수정은 반드시 아래 3단계를 순서대로 통과한 후 커밋 및 반영합니다.
1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
   ```
2. **Test (Race-Free Verification)**:
   ```bash
   go test -tags wiredtiger -count=1 -race ./...
   ```
   (전체 패키지 100% PASS 및 데이터 레이스 0건 필수)
3. **Commit**:
   - Conventional Commits 형식 (`fix(engine): ...`, `refactor(...): ...`)
   - 장식용 이모지 사용 금지

---

## 2. 상태 범례

- `[ ]` : 분석 및 등록 완료 (수정 대기)
- `[~]` : 수정 및 테스트 작성 진행 중
- `[x]` : 3단계 검증 완료 및 커밋 완료

---

## 3. 엔진 계층 (`pkg/engine`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-ENG-001** | P1 | 버그 | [`pkg/engine/sqlexec_dml.go:18`](pkg/engine/sqlexec_dml.go#L18) | `reDmlInsertValues` 정규식에서 `\s+INTO\s+` 강제로 인해 `INSERT tbl VALUES (...)` 형태 쿼리 파싱 실패 | `[x]` | 정규식 `(?:\s+INTO)?` 선택사항으로 수정 및 라우터 정규식 연동 완료 (`db57751`) |
| **REV-ENG-002** | P3 | 오류 | [`pkg/engine/sqlexec_dml.go:1248`](pkg/engine/sqlexec_dml.go#L1248) | DELETE 실행 중 PK 부재 시 `identify row for update`로 출력되는 복사-붙여넣기 에러 메시지 오류 | `[x]` | `identify row for delete`로 수정 완료 (`db57751`) |
| **REV-ENG-003** | P2 | 취약점 | [`pkg/engine/sqlexec_dml.go:248-253`](pkg/engine/sqlexec_dml.go#L248-L253) | UPSERT 접두사 처리 시 다중 공백이나 탭(`\t`) 처리 미흡으로 치환 누락 가능성 | `[x]` | `reDmlUpsertPrefix` 정규식 도입 및 ExecuteSQL 라우팅 정규화 완료 (`db57751`) |
| **REV-ENG-004** | P1 | 버그 | [`pkg/engine/sqlexec_eval.go:930-948`](pkg/engine/sqlexec_eval.go#L930-L948) | `evalUpdateExpr` 정수 나눗셈 시 `rightI == 0` 미검사로 인한 `integer divide by zero` 런타임 패닉 위험 | `[x]` | `rightF == 0 \|\| (targetType == TypeInt && rightI == 0)` 검사 및 divide by zero 에러 반환 |
| **REV-ENG-005** | P2 | 정합성 | [`pkg/engine/sqlexec_eval.go:137-145, 821-826`](pkg/engine/sqlexec_eval.go#L137) | `splitLogicalConditions` 및 `findArithmeticOp`의 SQL 연속 작은따옴표 이스케이프(`''`) 처리 누락 | `[x]` | `quoteChar` 연속 출현 시 이스케이프 건너뛰기 로직 보강 |

---

## 4. 스토리지 계층 (`pkg/storage`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| - | - | - | - | (엔진 완료 후 순차 점검 예정) | - | - |

---

## 5. 프로시저 & VDBE 계층 (`pkg/proc`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| - | - | - | - | (엔진 완료 후 순차 점검 예정) | - | - |

---

## 6. 와이어 프로토콜 & API 계층 (`pkg/wire`, `pkg/tds`, `pkg/api`)

| ID | 우선순위 | 구분 | 파일 및 라인 | 설명 | 상태 | 커밋 / 비고 |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| - | - | - | - | (엔진 완료 후 순차 점검 예정) | - | - |
