# LionSQL 실시간 라이브 엔진 감사 리포트 - Round 12 (근본 결함 압축 분석)

> **감사 일시**: 2026-11-19  
> **대상 엔진**: LionSQL v0.1.0-alpha (Git: 53b48865, 스토리지 엔진: WiredTiger)  
> **감사 초점**: JSON / XML 처리, 표현식 파싱, 타입 변환 및 통신 포맷  
> **식별된 독립 근본 결함 수**: 5건 (`ENG-018` ~ `ENG-022`)  
> **감사 주체**: Antigravity Live Audit System  

---

## 1. 개요 (Executive Summary)

Round 12에서는 JSON 경로(Path) 탐색, XML 메소드 호출, `CONVERT` 날짜/시간 포맷팅 스타일, 문화권(Culture) 기반 파싱 루틴을 집중 조사하였습니다.
총 5건의 독립적인 근본 원인이 식별되었으며, 실행 중인 인스턴스(포트 5433)에서 직접 검증되었습니다.
검사 완료 후 데이터베이스는 깨끗한 상태(사용자 테이블 0개)로 복구되었습니다.

---

## 2. 결함 상세 목록 (ENG-018 ~ ENG-022)

### [ENG-018] JSON_VALUE & JSON_QUERY가 "strict" 경로 모드를 무시하고 누락된 키에 대해 사일런트 NULL 반환
- **컴포넌트**: JSON Path Engine
- **해당 파일**: `pkg/proc/builtins_json.go:12-56`, `pkg/storage/json.go:452-468`
- **심각도**: MEDIUM
- **근본 원인**:
  - `storage/json.go:456`의 `navigatePath` 함수에서 접두사 `"STRICT "`를 제거하지만, `strict` 모드가 요청되었다는 플래그를 기록하지 않습니다.
  - SQL:2016 표준 및 Microsoft SQL Server 명세에 따르면, `strict` 경로 모드에서 존재하지 않는 키를 참조하거나 유효하지 않은 경로를 쿼리하면 반드시 오류(`Msg 13608: Property cannot be found on the specified JSON path`)가 발생해야 합니다.
  - 현재 LionSQL은 `strict` 모드를 `lax` 모드와 동일하게 취급하여 오류 없이 조용히 `NULL`을 반환합니다.
- **재현 쿼리**:
  ```sql
  SELECT JSON_VALUE('{"a": 1}', 'strict $.b');
  ```
- **기대 동작**: 오류 발생 (`Msg 13608: Property 'b' cannot be found on the specified JSON path.`)
- **실제 동작**: 오류 없이 `NULL` 반환

---

### [ENG-019] JSON_MODIFY의 "append" 수식어가 스칼라 또는 객체에 적용될 때 JSON 구조를 오염시킴
- **컴포넌트**: JSON Modification Engine
- **해당 파일**: `pkg/proc/builtins_json.go:12-56`
- **심각도**: HIGH
- **근본 원인**:
  - `modifyJSONByPath` 함수에서 경로 문자열을 파싱할 때 `append`와 같은 수식어 키워드를 검사하지 않습니다.
  - `append $.key`가 전달되면 엔진은 `append`를 액션 수식어로 추출하지 못하고, 리터럴 키 `"append $": {"key": value}`를 가진 손상된 JSON 객체를 생성합니다.
- **재현 쿼리**:
  ```sql
  SELECT JSON_MODIFY('{"a": 100}', 'append $.a', 200);
  ```
- **기대 동작**: 대상이 배열이 아닌 경우 오류를 반환하거나 배열로 추가(`{"a": [100, 200]}`)해야 함.
- **실제 동작**: 비정상적인 JSON 생성 (`{"a":100,"append $":{"a":200}}`)

---

### [ENG-020] 변수 메소드 호출(@x.value(), @x.query())이 선언된 변수 원본 값을 그대로 반환
- **컴포넌트**: Expression Evaluator / Variable Method Dispatch
- **해당 파일**: `pkg/engine/sqlexec_select.go:1018-1032`
- **심각도**: MEDIUM
- **근본 원인**:
  - `sqlexec_select.go:1019`는 변수 식별자를 파싱하지만, 점(dot) 구문이 포함된 메소드 호출(예: XML 메소드 `@x.value(...)` 또는 UDT 메소드 호출)이 들어왔을 때 메소드를 호출하지 않고 변수 `@x`를 찾아 원시 문자열을 그대로 반환하거나 미지원 오류를 내지 않습니다.
- **재현 쿼리**:
  ```sql
  DECLARE @x XML = '<root><val>123</val></root>';
  SELECT @x.value('(/root/val)[1]', 'INT');
  ```
- **기대 동작**: 파싱된 값 `123` 반환 또는 XML 인스턴스 메소드 미지원 오류 발생.
- **실제 동작**: 원시 XML 문자열(`<root><val>123</val></root>`)을 사일런트하게 반환.

---

### [ENG-021] CONVERT(VARCHAR, datetime, style)가 유효하지 않은 스타일에 대해 기본값으로 사일런트 폴백
- **컴포넌트**: Type Conversion Engine (CONVERT)
- **해당 파일**: `pkg/proc/eval.go:2158-2191`
- **심각도**: MEDIUM
- **근본 원인**:
  - `formatDateTimeByStyle`의 `default` 분기에서 무조건 `t.Format("2006-01-02 15:04:05")`를 반환합니다.
  - SQL Server 명세에 따르면, 지원되지 않거나 유효하지 않은 스타일 정수(예: `99999`)가 `CONVERT`에 전달되면 반드시 `Msg 281: Cannot convert date and/or time to character string using style %d.`를 발생시켜야 합니다.
  - 현재 LionSQL은 유효하지 않은 스타일을 무시하고 표준 ISO 타임스탬프 형식으로 출력합니다.
- **재현 쿼리**:
  ```sql
  SELECT CONVERT(VARCHAR, CAST('2026-11-19 12:34:56' AS DATETIME), 99999);
  ```
- **기대 동작**: `Msg 281: "Cannot convert date and/or time to character string using style 99999."`
- **실제 동작**: 오류 없이 `'2026-11-19 12:34:56'` 반환.

---

### [ENG-022] PARSE 및 TRY_PARSE 함수가 원문 표현식 문자열을 그대로 반환
- **컴포넌트**: Cultural & Number Parser
- **해당 파일**: `pkg/proc/eval.go`
- **심각도**: MEDIUM
- **근본 원인**:
  - T-SQL의 `PARSE(string_expr AS data_type [USING culture])` 및 `TRY_PARSE(...)` 문법이 토큰 스트림에서는 인식되지만, 평가기(Evaluator)에서 실제 파싱을 수행하지 않고 인식할 수 없는 표현식으로 취급하여 원문 SQL 텍스트(`PARSE('123.45' AS DECIMAL(10,2) USING 'en-US')`)를 그대로 반환합니다.
- **재현 쿼리**:
  ```sql
  SELECT PARSE('123.45' AS DECIMAL(10,2) USING 'en-US');
  ```
- **기대 동작**: 숫자값 `123.45` 반환.
- **실제 동작**: 원시 문자열 `PARSE('123.45' AS DECIMAL(10,2) USING 'en-US')` 반환.
