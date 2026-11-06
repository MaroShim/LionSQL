# PGWire Parameter Translation Security & Hardening Plan (`notes/PGWIRE_PARAM_TRANSLATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **상태**: [x] Completed (완료)
> - **과제 식별 코드**: `SEC-PGWIRE-PARAM-1`
> - **대상 파일**:
>   - `pkg/sqlutil/params.go` (신규: PGWire 파라미터 상태머신 모듈)
>   - `pkg/sqlutil/params_test.go` (신규: 단위 테스트)
>   - `pkg/wire/server.go` (정규식 제거 및 sqlutil 전환)
> - **핵심 불변식**:
>   - `[RULE:Invariant 7]` (Zero-Regex SQL Parsing in Production Paths)
>   - `[RULE:ModularAbstraction]` (`pkg/sqlutil` 도메인 패키지 모듈화 및 Rule of Two 준수)
>   - `[RULE:ZeroAllocation]` (정규식 컴파일/서브매칭 힙 할당 제거)
>   - `[RULE:PipelineVerification]` (Build ➔ Test `-race` ➔ Commit 3단계 검증)

---

## 1. 개요 및 배경

PostgreSQL 와이어 프로토콜(PGWire v3)에서는 바인드 파라미터를 `$1`, `$2`, `$3` 형태의 달러 플레이스홀더로 전달합니다. LionSQL의 VDBE 가상머신 및 내부 T-SQL 쿼리 엔진은 `@p1`, `@p2`, `@p3` 형태의 명명된 파라미터를 사용하므로, 클라이언트 쿼리를 수신했을 때 이를 변환해야 합니다.

현재 `pkg/wire/server.go`는 이를 위해 패키지 전역 정규식 `var pgParamRe = regexp.MustCompile(\$(\d+))`을 사용하고 있습니다:
1. `compileQueryToProgram` (줄 847): `pgParamRe.ReplaceAllString(query, "@p$1")`
2. `handleParse` (줄 875): `pgParamRe.FindAllStringSubmatch(queryStr, -1)` (최대 파라미터 인덱스 추론)
3. `buildPreviewSQL` (줄 1185): `pgParamRe.ReplaceAllStringFunc(...)` (EXPLAIN 샘플 값 대체)

### 취약점 및 결함 분석
1. **문자열 리터럴 내부 오염 및 쿼리 변조**:
   - `SELECT * FROM items WHERE desc = 'Price is $10' AND id = $1` 쿼리에서 `'Price is $10'`이 `'Price is @p10'`으로 오변환되어 쿼리 문법 에러 또는 엉뚱한 비즈니스 데이터 필터링/갱신을 초래.
2. **PostgreSQL 달러 인용(Dollar-Quoted String: `$$...$$` 및 `$tag$...$tag$`) 파괴**:
   - `INSERT INTO t VALUES ($$Hello $1$$)` 본문 내의 텍스트가 임의로 `@p1`으로 변조됨.
3. **DoS / 메모리 낭비 유발**:
   - `SELECT '$9999'`와 같이 따옴표 안에 큰 숫자의 달러 기호가 있으면 파라미터가 0개임에도 `make([]int32, 9999)` 슬라이스를 할당.

---

## 2. 세부 구현 계획

### 2.1 `pkg/sqlutil/params.go` 상태머신 토크나이저 개발
따옴표와 주석을 안전하게 건너뛰고 오직 최상위 SQL 표현식의 `$1`만 감지하는 결정론적 함수군을 개발:
1. `ReplacePgParams(sql string, replacer func(idx int) string) string`:
   - 작은따옴표 문자열 (`'...'`, `''` 이스케이프 지원)
   - 큰따옴표 식별자 (`"..."`, `""` 이스케이프 지원)
   - 달러 인용 문자열 (`$$...$$`, `$tag$...$tag$`)
   - 단일행 주석 (`-- ...\n`)
   - 블록 주석 (`/* ... */`)
   - 최상위 `$1`, `$2`... 감지 시 `replacer(idx)` 호출 및 치환
2. `TranslatePgParamsToAtParams(sql string) string`:
   - `ReplacePgParams`를 기반으로 `$1` ➔ `@p1`, `$2` ➔ `@p2` 고속 변환
3. `MaxPgParamIndex(sql string) int`:
   - 동일한 상태머신을 통해 따옴표 밖의 `$N` 중 최댓값 반환 (따옴표 내부 무시)

### 2.2 `pkg/wire/server.go` 연동 및 `pgParamRe` 완전 삭제
- `pgParamRe` 변수 및 관련 정규식 호출 전량 제거:
  - `compileQueryToProgram`: `sqlutil.TranslatePgParamsToAtParams` 호출
  - `handleParse`: `sqlutil.MaxPgParamIndex` 호출
  - `buildPreviewSQL`: `sqlutil.ReplacePgParams` 호출
  - `substituteParams`: `sqlutil.ReplacePgParams`로 단일화하여 중복 로직 제거
- `server.go` 상단 `"regexp"` import 영향도 점검 및 정리

---

## 3. 검증 계획 (3-Stage Verification)
1. **단위 테스트 (`pkg/sqlutil/params_test.go`)**:
   - 일반 파라미터 `$1, $2` 변환 검증
   - 작은따옴표 내부 `'$1, $100'` 보존 검증
   - 큰따옴표 내부 `"$1"` 식별자 보존 검증
   - 달러 인용 `$$ $1 $$` 및 `$body$ $1 $body$` 보존 검증
   - 주석 `-- $1` 및 `/* $1 */` 내부 무시 검증
   - `MaxPgParamIndex` 따옴표 내부 오탐 방지 검증
2. **PGWire 통합 테스트 (`pkg/wire/...`)**:
   - `go test -tags wiredtiger -count=1 -race ./pkg/wire/...` 100% 통과
3. **전체 파이프라인 무결성**:
   - `go build -tags wiredtiger ./...` 및 0 data races 검증
