# LionSQL P0-23 금융 정밀도를 위한 고정소수점 DECIMAL 타입 보존 아키텍처 사후 설계서 (`notes/P0_23_DECIMAL_FIXED_POINT_PRECISION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **과제 식별 코드**: `P0-23` (Parser & Financial Precision Integrity 하위)
> - **참조 원천 SSOT**: [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](../PRE_RELEASE_AUDIT_TRIAGE.ko.md), [`notes/AI_CODE_GENERATION_VS_ADVERSARIAL_AUDIT.ko.md`](../AI_CODE_GENERATION_VS_ADVERSARIAL_AUDIT.ko.md)
> - **핵심 적용 규칙**: `[RULE:SafeDefaults]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`
> - **관련 커밋**: `b3c9e7b` `fix(proc): map DECIMAL, NUMERIC, and MONEY to TypeDecimal with precision and scale (P0-23)`

---

## 1. 아키텍처 배경 및 문제 정의

LionSQL은 핀테크, 결제, ERP, 회계 등 엔터프라이즈 미션 크리티컬 OLTP 데이터베이스를 지향합니다.
금융 시스템에서 1원의 오차나 소수점 라운딩 왜곡은 법적 분쟁과 대규모 회계 불일치를 초래하므로, 금융 도메인은 IEEE 754 2진 부동소수점(`float64`) 대신 **임의 정밀도 10진 고정소수점(`DECIMAL(p, s)`, `NUMERIC`, `MONEY`)**을 필수적으로 사용합니다.

LionSQL의 스토리지 엔진(`pkg/storage`)과 VDBE 런타임은 이미 `TypeDecimal`을 완벽하게 구현하여 10진 고정소수점 연산기를 갖추고 있었습니다.

그러나 1.0 사전 전수 감사에서, SQL 쿼리를 해석하는 최상단 구문 분석기(Parser)의 치명적 결함으로 인해 **엔진이 지원하는 고정소수점 기능이 100% 무력화되고 모든 금융 데이터가 IEEE 754 부동소수점으로 강등되어 왜곡되던 P0 결함**이 발굴되었습니다.

```
+---------------------------------------------------------------------------------------+
| P0-23: DECIMAL/NUMERIC/MONEY를 TypeFloat으로 매핑하여 금융 부동소수점 오차 유발              |
| 파서가 금융 타입을 IEEE 754 Float으로 잘못 분류하여 잔액, 단가 등에서 비트 절삭 오차 발생.    |
+---------------------------------------------------------------------------------------+
```

---

## 2. 결함 메커니즘 (Root Cause)

### 2.1 집합론적 대칭성 붕괴 ($S_{\text{storage}} \setminus S_{\text{parser}}$)
스토리지 계층(`pkg/storage/types.go`)은 다음과 같이 명확히 `TypeDecimal`과 `TypeFloat`을 독립된 타입으로 정의하고 있었습니다:
- `TypeFloat`: 과학 계산용 64비트 배정밀도 부동소수점 (IEEE 754)
- `TypeDecimal`: 금융/회계용 10진수 고정소수점 (정밀도 $p$, 스케일 $s$)

그러나 T-SQL 및 DDL을 파싱하는 컴파일러 관문(`pkg/proc/parser.go`)의 `parseDataType` 함수는 다음과 같이 순진하게 작성되어 있었습니다:
```go
// 구 결함 코드 (pkg/proc/parser.go)
func parseDataType(s string) storage.DataType {
    switch strings.ToUpper(s) {
    case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT":
        return storage.TypeInt
    case "VARCHAR", "TEXT", "NVARCHAR", "CHAR", "BPCHAR":
        return storage.TypeVarChar
    case "FLOAT", "DECIMAL", "DOUBLE", "NUMERIC", "REAL", "FLOAT4", "FLOAT8", "MONEY":
        return storage.TypeFloat // 치명적 버그: DECIMAL, NUMERIC, MONEY를 Float으로 강등!
    // ...
    }
}
```

### 2.2 실무적 재앙 시나리오 (Penny-Shaving Distortion)
사용자가 DDL로 정밀 회계 테이블을 생성합니다:
```sql
CREATE TABLE accounts (
    account_id INT PRIMARY KEY,
    balance DECIMAL(18, 4) NOT NULL
);
```
1. 파서가 `DECIMAL`을 `TypeFloat`으로 인식하여 컬럼 타입을 64비트 실수(`float64`)로 등록합니다.
2. 클라이언트가 `INSERT INTO accounts VALUES (1, 1000000000.0001)`을 삽입합니다.
3. 2진수 부동소수점 표현 한계로 인해 `0.0001`의 하위 비트가 절삭되어 `1000000000.00010001657...` 또는 `999999999.9999...`로 변조됩니다.
4. 회계 결산 쿼리(`SUM(balance)`) 실행 시 원 단위 절삭 오차가 누적되어 대차대조표가 불일치하는 금융 참사가 발생합니다.

---

## 3. 해결 아키텍처: 듀얼 파서 레벨의 `TypeDecimal` 완전 격리 보존

LionSQL은 Tier 1 Fast-Path Native Pratt 파서와 Tier 2 ANTLR4 전체 문법 파서 모두에서 **금융 데이터 타입을 `TypeDecimal`로 완전 격리 분류**하고, 정밀도(`Precision`)와 스케일(`Scale`) 메타데이터를 스토리지 카탈로그까지 무손실 전파하도록 개편했습니다.

```
[SQL 타입 파싱 및 스토리지 매핑 파이프라인]

SQL Text: "DECIMAL(18, 2)"
           │
           ▼
[어휘 분석기 (Lexer)]
  - Token: "DECIMAL", "(", "18", ",", "2", ")"
           │
           ▼
[구문 분석기 (Pratt / ANTLR4 Parser)]
  - Type Mapping:
      FLOAT, DOUBLE, REAL, FLOAT4, FLOAT8 ──► storage.TypeFloat (IEEE 754)
      DECIMAL, NUMERIC, MONEY, SMALLMONEY  ──► storage.TypeDecimal (Fixed-Point)
  - Parameter Extraction:
      Precision = 18, Scale = 2
           │
           ▼
[스토리지 카탈로그 & VDBE VM]
  - Column.Type = storage.TypeDecimal
  - Column.Precision = 18, Column.Scale = 2
  - 연산기: 10진 고정소수점 엔진을 통한 무손실 사칙연산
```

#### 핵심 구현 코드 (`pkg/proc/parser.go`)
```go
func parseDataType(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT":
		return storage.TypeInt
	case "VARCHAR", "TEXT", "NVARCHAR", "CHAR", "BPCHAR":
		return storage.TypeVarChar
	case "FLOAT", "DOUBLE", "REAL", "FLOAT4", "FLOAT8":
		return storage.TypeFloat
	case "DECIMAL", "NUMERIC", "MONEY", "SMALLMONEY":
		return storage.TypeDecimal // P0-23: 금융 정밀도를 위한 독립 타입 보존
	case "BIT", "BOOL", "BOOLEAN":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP", "TIMESTAMPTZ", "DATETIME2", "SMALLDATETIME", "DATETIMEOFFSET":
		return storage.TypeDateTime
    // ...
	}
}
```

---

## 4. TDD 회귀 검증 매트릭스

1. **`TestParser_Regression_P0_23_DecimalTypes`**:
   - `DECIMAL(10, 2)`, `NUMERIC(18, 4)`, `MONEY`, `SMALLMONEY` 구문 파싱.
   - 반환된 `storage.DataType`이 `TypeFloat`이 아닌 `TypeDecimal`임을 100% 검증.
   - `FLOAT`, `REAL`, `FLOAT8`은 표준 과학 실수 규격인 `TypeFloat`으로 정확히 분리 유지됨을 검증.
2. **`TestEngine_Regression_P0_23_FinancialPrecisionMath`**:
   - `CREATE TABLE bank_ledger (amt DECIMAL(18, 4))` 생성 후 `0.1`, `0.2` 삽입.
   - `SELECT SUM(amt)`의 결과가 부동소수점 오차(`0.30000000000000004`) 없이 정확히 `0.3000`으로 일치함을 검증.

---

## 5. 결론 및 타입 시스템 불변성

1. **금융 타입 분리 불변성**: `DECIMAL`, `NUMERIC`, `MONEY` 계열은 영구히 `TypeDecimal`에 매핑되어야 하며, 어떠한 편의 최적화나 직렬화 과정에서도 `float64`로 자동 캐스팅되어서는 안 됩니다.
2. **2진수 vs 10진수 엄격 분리**: 과학 기술 부동소수점(`FLOAT`, `REAL`)과 상업 금융 고정소수점(`DECIMAL`, `MONEY`)은 파서, 옵티마이저, VM 런타임 전 계층에서 상호 배타적으로 처리되어야 합니다.
