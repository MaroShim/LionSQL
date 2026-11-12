# T-SQL AT TIME ZONE, PARSE 및 세션 타임존/로케일 컨텍스트 아키텍처 플랜 (`notes/plans/TIMEZONE_LOCALE_PARSING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-12
> - **목표 마일스톤**: `v1.1.0`
> - **관련 과제 ID**: `TSQL-FUNC-AT-TIME-ZONE`, `TSQL-FUNC-PARSE`
> - **엔지니어링 원칙**: `[RULE:PlanPersistence]`, `[RULE:RadicalKISS]`, `[RULE:SafeDefaults]`, `[RULE:ModularAbstraction]`

---

## 1. 개요 및 배경 (Background & Motivation)

LionSQL은 1.0-GA에 걸쳐 모든 핵심 트랜잭션, 감사, 토큰 만료 로직을 엄격한 `UTC` 기준으로 표준화하고, `GETDATE()`와 `GETUTCDATE()`를 명확히 분리함으로써 호스트 환경 차이로 인한 버그를 차단하였습니다.

그러나 Microsoft SQL Server 및 엔터프라이즈 BI/ERP(PowerBI, Metabase, SAP 등) 환경에서는 다음과 같은 요구사항이 빈번히 발생합니다:
1. **타임존 오프셋 변환 (`AT TIME ZONE`)**:
   - `SELECT OrderDate AT TIME ZONE 'UTC' AT TIME ZONE 'Korea Standard Time' FROM Orders;`
   - Windows 타임존 이름(`"Korea Standard Time"`, `"Pacific Standard Time"`)과 IANA 표준 타임존(`"Asia/Seoul"`, `"America/Los_Angeles"`)을 상호 호환하며 일광절약시간제(DST)를 반영해야 함.
2. **문화권(Culture) 기반 문자열 파싱 (`PARSE` / `TRY_PARSE`)**:
   - `SELECT PARSE('12/11/2026' AS DATE USING 'en-US');` (12월 11일)
   - `SELECT PARSE('12.11.2026' AS DATE USING 'de-DE');` (11월 12일)
   - `SELECT PARSE('₩1,234' AS MONEY USING 'ko-KR');`
3. **세션 컨텍스트 기반 암묵적 타임존 해석 (`sess.GetTimeZone()`)**:
   - 클라이언트가 타임존 오프셋이 생략된 날짜/시간 리터럴(`'2026-11-12 15:00:00'`)을 전송했을 때, 세션의 `SET TIME ZONE <tz>` 설정을 조회하여 정확한 로컬 시점으로 해석해야 함.

본 문서는 `v1.1.0`에서 위 3가지 기능을 LionSQL의 2-Tier 파서 및 VDBE 바이트코드 엔진에 안전하고 유연하게 통합하기 위한 표준 아키텍처와 구현 명세를 정의합니다.

---

## 2. 아키텍처 설계 (Detailed Design)

### 2.1. 세션 타임존/로케일 컨텍스트 연동 (`sess.GetTimeZone()`)

현재 `pkg/engine/session.go`의 `ClientSession` 구조체는 세션 변수 맵(`cs.Vars`)에 `timezone`을 저장하고 있습니다 (`SET TIME ZONE 'Asia/Seoul'` 시 `cs.Vars["timezone"] = tz`).

이를 VDBE 런타임과 스토리지 디코더에서 $O(1)$로 빠르게 조회할 수 있도록 정규화된 헬퍼를 노출합니다:

```go
// pkg/engine/session.go
func (cs *ClientSession) GetTimeZoneLocation() *time.Location {
    cs.Mu.RLock()
    defer cs.Mu.RUnlock()
    tzStr, ok := cs.Vars["timezone"].(string)
    if !ok || tzStr == "" {
        return time.Local // 기본값: 서버 호스트 로컬 타임존
    }
    return ResolveTimeZoneLocation(tzStr)
}
```

---

### 2.2. Windows 타임존 ➔ IANA 타임존 양방향 정적 매핑 테이블

Go 표준 라이브러리의 `time.LoadLocation`은 IANA 타임존 식별자(`"Asia/Seoul"`, `"America/New_York"`)만을 기본 인식합니다. 그러나 SQL Server 클라이언트는 전통적으로 Windows 타임존 이름(`"Korea Standard Time"`, `"Eastern Standard Time"`)을 사용합니다.

따라서 `pkg/sqlutil/timezone.go` 모듈을 신설하여 정적 매핑 테이블을 내장합니다:

```go
// pkg/sqlutil/timezone.go
var windowsToIANA = map[string]string{
    "korea standard time":            "Asia/Seoul",
    "tokyo standard time":            "Asia/Tokyo",
    "pacific standard time":          "America/Los_Angeles",
    "eastern standard time":          "America/New_York",
    "central standard time":          "America/Chicago",
    "gmt standard time":              "Europe/London",
    "w. europe standard time":        "Europe/Berlin",
    "utc":                            "UTC",
}

func ResolveTimeZoneLocation(tz string) *time.Location {
    tzClean := strings.ToLower(strings.TrimSpace(tz))
    if iana, ok := windowsToIANA[tzClean]; ok {
        tzClean = iana
    }
    loc, err := time.LoadLocation(tzClean)
    if err != nil {
        return time.UTC
    }
    return loc
}
```

---

### 2.3. Pratt 파서 중위 연산자 확장 (`AT TIME ZONE`)

`AT TIME ZONE`은 함수가 아니라 **이항 연산자(Infix Operator)**입니다.
- **문법**: `<Expression> AT TIME ZONE <TimeZoneExpression>`
- **우선순위(Precedence)**: 덧셈/뺄셈보다 낮고 비교 연산자보다 높은 `PrecedenceAtTimeZone` (우선순위 35).

#### 컴파일러 및 AST
```go
// pkg/proc/ast.go
type AtTimeZoneExpr struct {
    Expr     Expr
    TimeZone Expr
}

// pkg/proc/compiler.go
func (c *Compiler) compileAtTimeZoneExpr(e *AtTimeZoneExpr) (int, error) {
    srcReg, err := c.compileExpr(e.Expr)
    tzReg, err := c.compileExpr(e.TimeZone)
    outReg := c.allocReg()
    c.emit(OpAtTimeZone, srcReg, tzReg, outReg)
    return outReg, nil
}
```

#### VDBE 가상머신 실행 (`OpAtTimeZone`)
```go
// pkg/proc/runtime/vm.go
case OpAtTimeZone:
    val := vm.regs[inst.A]
    tzVal := vm.regs[inst.B]
    // 1. 입력값이 DATETIMEOFFSET인지, DATETIME2인지 검사
    // 2. tzVal 문자열로부터 time.Location 파싱
    // 3. 변환된 DATETIMEOFFSET 값 산출 후 inst.C에 저장
```

---

### 2.4. `PARSE` 및 `TRY_PARSE` 문법 및 문화권 파서

#### 문법 구조
- `PARSE ( string_value AS data_type [ USING culture ] )`
- `TRY_PARSE ( string_value AS data_type [ USING culture ] )`

`TRY_PARSE`는 파싱 실패 시 런타임 에러를 던지지 않고 `NULL`을 반환하는 Safe 버전입니다.

#### 문화권(Culture) 포맷 매핑 테이블
`pkg/sqlutil/culture.go`에 주요 문화권별 일자 및 숫자 포맷 템플릿을 등록합니다:
- `en-US`: `01/02/2006`, `$#,##0.00`
- `ko-KR`: `2006-01-02`, `2006. 01. 02`, `₩#,##0`
- `de-DE`: `02.01.2006`, `#.##0,00 €`
- `ja-JP`: `2006/01/02`, `¥#,##0`

---

## 3. 구현 단계별 로드맵 (Milestone v1.1.0)

| 단계 | 소요 추정 | 주요 작업 내용 | 담당 파일 |
| :---: | :---: | :--- | :--- |
| **Phase 1** | 1일 | • `pkg/sqlutil/timezone.go`: Windows-IANA 타임존 매핑 모듈 구현<br>• `ClientSession.GetTimeZoneLocation()` 세션 컨텍스트 연동 | `pkg/sqlutil/timezone.go`<br>`pkg/engine/session.go` |
| **Phase 2** | 2일 | • `pkg/proc/lexer.go`, `token.go`: `AT`, `ZONE` 토큰 등록<br>• `pkg/proc/parser.go`: `parseAtTimeZoneExpr` 중위 파서 확장<br>• VDBE `OpAtTimeZone` 바이트코드 명령어 추가 및 런타임 실행 | `pkg/proc/lexer.go`<br>`pkg/proc/parser.go`<br>`pkg/proc/runtime/vm.go` |
| **Phase 3** | 1.5일| • `PARSE` / `TRY_PARSE` 파서 절(`AS`, `USING`) 구현<br>• 문화권(Culture)별 날짜/숫자 파서 엔진 구축 및 에러 핸들링 | `pkg/proc/parser.go`<br>`pkg/proc/builtins_string.go` |
| **Phase 4** | 0.5일| • T-SQL 공식 호환성 회귀 테스트 스위트 작성 (DST 일광절약시간 검증 포함) | `pkg/proc/runtime/timezone_test.go` |

---

## 4. 결론 및 결의

본 기능은 LionSQL의 글로벌 엔터프라이즈 BI 도구 호환성을 대폭 끌어올릴 핵심 기능이지만, 1.0-GA 릴리스의 핵심 가치인 **"안정성 및 크래시 프리(Zero-Defect)"** 원칙을 수호하기 위해 `v1.1.0`의 첫 번째 대형 피처로 추진하기로 결의합니다.

본 설계 문서는 `[RULE:PlanPersistence]`에 따라 디스크에 영구 보존되며, 1.0 GA 태깅 직후 착수될 예정입니다.
