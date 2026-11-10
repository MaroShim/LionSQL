# LionSQL 듀얼 AI 적대적 감사 프로토콜 (Dual-AI Adversarial Audit Protocol)

> - **최초 작성일**: 2026-11-10
> - **적용 대상**: 모든 정식 및 마이너/패치 릴리즈 (`vX.Y.Z`) 배포 전 필수 관문
> - **핵심 원칙**: [RULE:RadicalKISS], [RULE:PipelineVerification], [RULE:ExplicitError], [RULE:ZeroOrphan]

---

## 1. 배경 및 목적

단일 개발자나 단일 AI 에이전트가 코드를 작성하고 검증할 경우, 필연적으로 **생성자 편향(Confirmation Bias)**이 발생합니다. 자신이 구현한 로직의 가정(Happy Path)에 맞추어 단위 테스트를 구성하므로, 다음과 같은 미세하고 치명적인 사각지대를 스스로 적발하기 어렵습니다:

- 64비트 정수 최솟값 나눗셈 오버플로 (`math.MinInt64 / -1` 및 `% -1`)
- Go 인터페이스 임베딩으로 인한 기저 구조체 메서드 은닉 (Method Shadowing)
- 듀얼 슬라이스 정렬 불일치 및 비정렬 인덱스 참조
- 컨텍스트 취소 시 백그라운드 타이머/고루틴 누수 (`time.After` vs `time.NewTimer`)

LionSQL은 **Antigravity(구현 및 오케스트레이션)**와 **Claude(적대적 감사 및 공격적 에지 케이스 발굴)**의 상호보완적 시너지를 제도화하여, 릴리즈 시점의 결함 발생률을 0%에 수렴시키는 정형화된 **듀얼 AI 릴리즈 감사 게이트(Dual-AI Release Audit Gate)**를 운영합니다.

---

## 2. 듀얼 AI 역할 분담 체계

```
┌────────────────────────────────────────────────────────┐
│               1. Antigravity (Builder)                 │
│  - 기능 구현, 리팩토링, 최적화                          │
│  - 3단계 파이프라인 (Build ➔ Test ➔ Commit) 실행         │
└───────────────────────────┬────────────────────────────┘
                            │
                            │ Diff 추출 (./scripts/audit_diff.sh)
                            ▼
┌────────────────────────────────────────────────────────┐
│           2. Claude (Adversarial Auditor)              │
│  - 극단적 비대칭 경계값, Go 런타임 함정 공격           │
│  - 자원 누수, 데드락, Fail-Closed 위반 탐지             │
└───────────────────────────┬────────────────────────────┘
                            │
                            │ 결함 리포트 도출
                            ▼
┌────────────────────────────────────────────────────────┐
│           3. Antigravity (Remediator & QA)             │
│  - 결함 패치 및 회귀 테스트(Regression Test) 작성        │
│  - `go test -v -race ./...` 100% 통과 검증             │
│  - ./scripts/release.sh 6-Gate 릴리즈 자동화             │
└────────────────────────────────────────────────────────┘
```

---

## 3. 4대 감사 중점 점검 축 (Audit Checklist)

Claude 감사 수행 시 반드시 다음 4대 축을 기준으로 코드 diff를 점검합니다:

### 1) 자원 및 동시성 누수 (Resource & Concurrency)
- [ ] 고루틴 생명주기가 부모 컨텍스트에 묶여 있는가? ([RULE:ZeroOrphan])
- [ ] 루프 내에서 `time.After`가 호출되어 타이머가 누수되지 않고 `time.NewTimer`로 회수되는가?
- [ ] 채널 전송 시 `select/default`로 슬로우 컨슈머 블로킹을 방지하는가? ([RULE:NonBlockingSink])
- [ ] 뮤텍스를 쥔 채 블로킹 I/O나 콜백을 호출하여 데드락 위험이 없는가? ([RULE:LockHierarchy])

### 2) 비대칭 경계값 및 산술 안정성 (Boundary & Arithmetic)
- [ ] `math.MinInt64 / -1` 및 `math.MinInt64 % -1` 연산이 가드되어 런타임 패닉이나 부동소수점 오염을 방지하는가?
- [ ] 0 나눗셈 및 `NaN`/`+Inf`가 옵티마이저 비용 계산이나 집계 함수에 유입되지 않는가?
- [ ] 슬라이스 인덱싱이 음수나 경계 초과로부터 방어되는가? ([RULE:DefensiveIndexing])

### 3) 예외 처리 및 Fail-Closed 원칙 (Failure Propagation)
- [ ] 에러를 침묵하고 삼키는 코드(`_ = err`)가 없는가? ([RULE:ExplicitError])
- [ ] 중간 연산 실패 시 열려 있는 커서, 임시 테이블, 트랜잭션이 확실하게 롤백/회수되는가?
- [ ] `work_mem` 초과 시 OOM 크래시 대신 외부 정렬 스필 또는 명시적 에러로 실패하는가?

### 4) 인터페이스 임베딩 및 영구 불변식 (Invariants & Interface)
- [ ] 구조체가 인터페이스를 임베딩할 때 기저 구현체의 부가 메서드가 가려지지 않는가?
- [ ] `GEMINI.md`에 명시된 11대 핵심 불변식(TDS Windows Parity, Zero transaction_sync, WAL Fail-Closed 등)을 준수하는가?

---

## 4. 표준 실행 절차 (SOP)

### 1단계: 변경 Diff 및 프롬프트 생성
릴리즈 준비 시 직전 릴리즈 태그와 `HEAD` 사이의 프로덕션 Go 코드 diff를 자동 추출합니다:
```bash
./scripts/audit_diff.sh $(git describe --tags --abbrev=0) HEAD
```
출력된 `/tmp/lionsql_audit_prompt_<range>.md` 내용을 Claude에게 전달하여 감사를 요청합니다.

### 2단계: 결함 패치 및 회귀 테스트 작성
감사에서 지적된 항목 중 유효한 결함에 대해:
1. 원자적 수정을 적용합니다.
2. 해당 결함의 재발을 막는 명시적인 회귀 테스트(`*_regression_test.go`)를 추가합니다.

### 3단계: 3단계 파이프라인 검증
```bash
# 1. 컴파일 무결성 검증
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime

# 2. Race-Free 전수 테스트 검증
go test -tags wiredtiger -v -race ./...

# 3. Conventional Commits 형식으로 패치 커밋
git commit -m "fix(<domain>): remediate pre-release audit findings in ..."
```

### 4단계: 릴리즈 스크립트 실행
감사 및 패치가 완료된 후 공식 릴리즈 스크립트를 구동합니다:
```bash
./scripts/release.sh vX.Y.Z
```
