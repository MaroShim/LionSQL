# LionSQL Collation, 로케일 및 다국어 아키텍처 & 구현 계획 (`COLLATION_AND_LOCALE_PLAN.ko.md`)

> - **작성일**: 2026-10-18
> - **최종 갱신일**: 2026-11-12
> - **대상 컴포넌트**: `pkg/proc`, `pkg/wire/tds`, `pkg/catalog/virtual`, `pkg/config`, `pkg/engine`

본 기술 사양서는 LionSQL의 Microsoft SQL Server Collation, 로케일(Locale), 및 국제화(i18n) 호환성을 위한 아키텍처 전략과 단계별 구현 로드맵을 정의합니다. 레거시 MSSQL의 CodePage/Collation 결합 모델을 현대적인 무할당(Zero-Allocation) UTF-8 네이티브 스토리지 엔진과 조화시키고, 실무에서 빈번히 발생하는 "Collation 충돌(Error 468)"을 근본적으로 방지하는 방안을 다룹니다.

---

## 1. 개요 및 핵심 설계 불변식 (Design Invariants)

Microsoft SQL Server의 Collation 아키텍처는 문자 인코딩(CodePage: CP949, CP1252 등)과 언어별 정렬/비교 규칙(`CI_AS`, `CS_AI`, `_BIN`)을 강하게 결합하고 있습니다. 이는 레거시 환경에서 강력했으나, 다국어 지원을 위한 `NVARCHAR`(UTF-16) 테이블 비대화 및 조인 시 Collation 불일치로 인한 런타임 충돌 에러(Error 468)라는 심각한 운영 마찰을 초래했습니다.

LionSQL은 **"TypeScript식 외곽 호환성 및 기본 런타임 소멸(Collation Erasure) + Go식 사전 바인딩 런타임 SPI(Pre-bound SPI)"**를 결합한 **실용적 2계층 하이브리드 아키텍처**를 채택합니다:

```
[ 외곽 클라이언트 계층 (TDS / PGWire / REST / DDL / DML 컴파일) ]
  - MSSQL 100% 문법 호환: Korean_Wansung_CI_AS, SQL_Latin1_General_CP1_CI_AS 등 수용
  - 5바이트 TDS 바이너리 토큰 및 시스템 함수(COLLATIONPROPERTY, fn_helpcollations) 에뮬레이션
  - 콜드패스 정규화: 기본값은 compareFn = nil, 특수 로케일은 함수 포인터(&customUDF)로 사전 바인딩
                          │
                          ▼ (런타임 디스패치 분기)
[ 코어 실행 및 스토리지 엔진 (WiredTiger & In-Memory OCC) ]
  ├─ 99% 기본 핫패스 (Collation Erasure, 0.5ns 무할당):
  │    - compareFn == nil 감지 시 순수 UTF-8 strings.EqualFold / bytes.Compare 즉시 직결
  │    - TypeScript의 Type Erasure처럼 레거시 Collation 오버헤드가 런타임에서 완전히 소멸
  └─ 1% 특수 언어학적 경로 (Pay-as-you-go 사전 바인딩 SPI):
       - compareFn(a, b) 직접 호출 (맵 조회 제로, 락 제로)
       - 한국어 자모 정규화/초성 검색, 독일어 DIN 5007-2, 프랑스어 역방향 악센트 정렬 등 정밀 수행
```

### TypeScript 모델과의 비교 및 차별점:
- **TypeScript와 닮은 점 (외곽 호환성 & 기본 런타임 소멸)**:
  - TypeScript가 개발자에게는 정교한 타입 시스템을 제공하지만 런타임 V8 엔진에는 타입을 완전히 지워버려(Type Erasure) 런타임 오버헤드를 0으로 만들듯이,
  - LionSQL도 클라이언트와 관리 도구에게는 MSSQL의 복잡한 Collation 구문과 토큰을 완벽하게 받아주지만, 99%의 기본 핫패스에서는 레거시 Collation 메커니즘을 사실상 소멸시켜(Collation Erasure) 순수 UTF-8 네이티브의 서브 나노초(~0.5ns) 비교로 직결합니다.
- **TypeScript와의 결정적 차이점 (조건부 런타임 SPI)**:
  - TypeScript는 런타임에 단 1비트의 타입 코드도 남지 않는 100% 완전 소멸(Pure Erasure) 방식입니다.
  - 반면 LionSQL은 **실제 언어학적 정렬이나 특수 룰(`Korean_Chosung`, `German_PhoneBook`, `French_Backward`)**이 지정된 경우, 런타임에 맵 검색 없이 즉시 분기되는 초경량 사전 바인딩 Go UDF 함수 포인터를 통해 언어학적 규칙을 정확히 수행하는 실용적 하이브리드 모델입니다.

### 핵심 불변식 (Core Invariants):
1. **단일 표준 UTF-8 스토리지 불변식 (Universal Canonical UTF-8)**:
   - 물리 스토리지에 레거시 코드페이지(CP949, CP1252, CP932 등)로 저장하지 않습니다. 모든 문자열 타입(`CHAR`, `VARCHAR`, `NVARCHAR`, `TEXT`)은 WiredTiger B+Tree 및 In-Memory OCC 모두에서 순수 UTF-8 바이트 시퀀스로 저장됩니다.
2. **기본 패스트패스 ("No Collation") 무부하 불변식 (Zero-Overhead Fast-Path)**:
   - 99%의 쿼리 비교와 인덱스 조회는 무할당 인라인 패스트패스(`strings.EqualFold` 또는 `bytes.Compare`)를 통해 서브 나노초(~0.5ns) 지연시간으로 처리됩니다.
   - 기본 Collation(`SQL_Latin1_General_CP1_CI_AS`, `DATABASE_DEFAULT`, 생략 시)은 파서/플래너 등 콜드패스에서 빈 문자열(`""`) 또는 `nil` 함수 포인터로 정규화되어 핫패스에서 맵 조회가 발생하지 않습니다.
3. **플러그형 Go UDF Collation SPI (Pluggable Go UDF Collation)**:
   - 다국어 특수 정렬(`Korean_Wansung_CI_AS`, 커스텀 비즈니스 정렬)은 거대한 ICU C 라이브러리를 엔진 코어에 내장하지 않고, 인프로세스 경량 Go UDF 콜백(`RegisterCollation`)을 통해 필요할 때만 연결됩니다.
4. **무충돌 Collation 정책 (Zero Collation Conflict Policy)**:
   - MSSQL Error 468(`Cannot resolve collation conflict`)을 제거합니다. 조인이나 조건문에서 서로 다른 Collation이 충돌할 경우 트랜잭션을 강제 종료하지 않고 유니코드 표준 동등 비교로 안전하게 결합합니다.
5. **와이어 프로토콜 정합성 (Wire-Level Protocol Fidelity)**:
   - TDS 7.x/8.0 핸드셰이크(`LOGINACK`, `ENVCHANGE`)에서 정확한 5바이트 Collation 구조체를 반환하여 클라이언트 드라이버(Microsoft JDBC, ADO.NET, SSMS, DBeaver, EF Core)와의 호환성을 100% 보장합니다.

---

## 2. 레거시 MSSQL Collation 문제점 및 분석

### 2.1 CodePage와 Collation의 결합 문제
- `VARCHAR`: 컬럼의 CodePage(한국어 CP949, 서유럽 CP1252 등)에 종속되어, 해당 코드페이지 외의 문자가 입력되면 물음표(`?`) 깨짐(Mojibake)이 발생했습니다.
- `NVARCHAR`: 다국어 저장을 위해 2바이트 UCS-2/UTF-16 인코딩이 강제되어 메모리와 디스크 사용량이 2배로 증가했습니다.
- MSSQL 2019에서 `_UTF8` Collation이 도입되었으나, 수많은 레거시 데이터베이스는 여전히 이전 구조에 묶여 있습니다.

### 2.2 악명 높은 "Collation 충돌" (Error 468)
MSSQL에서는 `tempdb`에 생성된 임시 테이블(`#temp`)이 인스턴스 서버 기본 Collation을 상속받습니다. 사용자 데이터베이스가 `Korean_Wansung_CI_AS`이고 서버 인스턴스가 `SQL_Latin1_General_CP1_CI_AS`로 설치된 경우, 단순 조인 쿼리가 런타임에 중단됩니다:
```sql
-- MSSQL에서 Error 468로 실패:
SELECT * FROM #temp t JOIN users u ON t.username = u.username;

-- 개발자가 수동으로 패치해야 함:
SELECT * FROM #temp t JOIN users u ON t.username = u.username COLLATE database_default;
```
LionSQL은 이러한 마찰을 안티패턴으로 규정하며, 동등성 검사 시 유니코드 규칙으로 안전하게 상호 변환하여 실행을 보장합니다.

---

## 3. 세부 아키텍처 컴포넌트

### 3.1 TDS 프로토콜 5바이트 Collation 구조
Microsoft TDS 7.x/8.0에서 Collation은 압축된 5바이트 구조로 교환됩니다:

```
 0                   1                   2                   3                   4
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|             LCID (20 bits)            |      Collation Flags      |  Version ID | Collation ID|
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

- **Byte 0–1 및 Byte 2 일부**: LCID (로케일 ID, 리틀 엔디언).
  - `0x0409` (1033): en-US (미국 영어)
  - `0x0412` (1042): ko-KR (한국어)
  - `0x0411` (1041): ja-JP (일본어)
  - `0x0804` (2052): zh-CN (중국어 간체)
- **Byte 2 (상위 비트) 및 Byte 3**: 비교 플래그 (`IgnoreCase = 0x01`, `IgnoreAccent = 0x02`, `IgnoreKana = 0x04`, `IgnoreWidth = 0x08`, `Binary = 0x80` 등).
- **Byte 4**: SQL Collation ID (`0x34` = 52: `SQL_Latin1_General_CP1_CI_AS`, Windows Collation의 경우 `0x00`).

#### 표준 Collation 매핑 테이블

| Collation 명칭 | LCID | CodePage | TDS 5바이트 바이너리 토큰 | 설명 |
| :--- | :--- | :--- | :--- | :--- |
| `SQL_Latin1_General_CP1_CI_AS` | `0x0409` (1033) | 1252 | `0x09 0x04 0xD0 0x00 0x34` | 기본 미국 영어 SQL Collation |
| `Korean_Wansung_CI_AS` | `0x0412` (1042) | 949 | `0x12 0x04 0xD0 0x00 0x00` | 표준 한국어 Windows Collation |
| `Latin1_General_100_CI_AS_SC_UTF8` | `0x0409` (1033) | 65001 | `0x09 0x04 0xD0 0x02 0x00` | 최신 UTF-8 보조 문자 지원 Collation |
| `Japanese_CI_AS` | `0x0411` (1041) | 932 | `0x11 0x04 0xD0 0x00 0x00` | 표준 일본어 Windows Collation |
| `Chinese_PRC_CI_AS` | `0x0804` (2052) | 936 | `0x04 0x08 0xD0 0x00 0x00` | 중국어 간체 Windows Collation |

---

### 3.2 플러그형 Collation 아키텍처: 패스트패스 & 사전 바인딩 Go UDF

방대한 유니코드 정렬 알고리즘(UCA) 테이블과 무거운 ICU 라이브러리를 데이터베이스 코어 바이너리에 내장하면 바이너리 용량이 급증하고 인덱스 조회 성능이 저하됩니다. 또한 고빈도 문자열 비교 루프에서 매번 문자열 해시 맵을 검색하는 것은 핫패스 처리량을 떨어뜨립니다.

LionSQL은 Collation 처리를 **2계층 사전 바인딩 (Fast-Path Native vs. Pre-Bound Go UDF)** 구조로 분리합니다:

```
[ SQL 쿼리 파싱 / 실행 계획 컴파일 / DDL 바인딩 (콜드패스) ]
                              │
                              ▼
       Collation 식별자를 사전 바인딩 함수 포인터로 해석:
       - 기본값 / 빈 문자열 / SQL_Latin1_General ──▶ CompareFn = nil
       - 커스텀 등록 Collation                   ──▶ CompareFn = &customUDF
                              │
                              ▼
        [ 핵심 핫패스 비교 실행 (CompareStrings) ]
        ┌────────────────────────────────────────────────────────┐
        │ if compareFn == nil {                                  │
        │     return fastCompareCI(a, b) // 0.5ns, 무할당         │
        │ }                                                      │
        │ // 사전 해석된 함수 포인터: 맵 조회 제로!                 │
        │ return compareFn(a, b)                                 │
        └────────────────────────────────────────────────────────┘
```

#### 1. 콜드패스 사전 바인딩 및 정규화 (파서 / 플래너 / 바인더)
- **핫패스 맵 조회 제거 ([RULE:BoundedLookup])**: 테이블 스캔이나 B+Tree 탐색 도중 문자열 Collation 이름을 검색하지 않습니다. 쿼리 계획 단계에서 문자열 Collation 식별자를 UDF 레지스트리와 매핑하여 함수 포인터(`CollationCompareFunc`) 또는 사전 할당된 슬롯으로 즉시 바인딩합니다.
- 기본 Collation(`SQL_Latin1_General_CP1_CI_AS`, `DATABASE_DEFAULT`, `SERVER_DEFAULT` 또는 생략)은 `compareFn = nil`로 단일화됩니다.

#### 2. 핫패스 무부하 패스트패스 (0.5ns 불변식, [RULE:LatencyBudget])
- 테이블 스캔, 보조 인덱스 검색, 해시 조인 내부에서는 **단 한 번의 포인터 nil 체크(`if compareFn == nil`)**만 수행합니다.
- 인라인된 `strings.EqualFold`(대소문자 무시) 또는 `bytes.Compare`(`_BIN`/`_BIN2`)로 즉시 직결되어 서브 나노초 레이턴시를 유지합니다.

#### 3. 사전 바인딩 Go UDF Collation 실행
- 특수 다국어 정렬(`Korean_Wansung_CI_AS`, 악센트 구분, 커스텀 정렬)은 사전 해석된 함수 포인터(`compareFn(a, b)`)를 직접 호출하여 런타임 락이나 동기화 오버헤드 없이 실행됩니다.
- 개발자는 서버 시작 시 인프로세스 Go SPI로 커스텀 Collation을 등록할 수 있습니다:
  ```go
  // CollationCompareFunc: a와 b를 비교하여 -1, 0, 1 반환
  type CollationCompareFunc func(a, b string) int

  // 커스텀 또는 로케일 Collation 제공자 등록
  proc.RegisterCollation("KOREAN_WANSUNG_CI_AS", koreanCollateFunc)
  ```

---

### 3.3 언어학적 실무 사례 연구: 한국어, 독일어, 프랑스어

단순 바이너리 바이트 비교가 글로벌 데이터베이스 환경에서 왜 실패하는지, 그리고 왜 LionSQL의 정규화 및 플러그형 Go UDF 아키텍처가 필수적인지를 보여주는 실무 언어학적 사례입니다:

#### 1. 한국어 (`Korean_Wansung_CI_AS`, `Korean_100_CI_AS`)
- **macOS NFD vs Windows/Linux NFC 자모 분리 대참사 방지**:
  - macOS 및 iOS 환경(파일 시스템, 클립보드 등)은 한글을 조합형 자모가 분리된 NFD(`ㅎ` + `ㅗ` + `ㅇ` + `ㄱ` + `ㅣ` + `ㄹ` + `ㄷ` + `ㅗ` + `ㅇ`) 형태로 생성하는 경우가 매우 흔합니다.
  - 반면 Windows와 일반 Linux DB 클라이언트는 완성형 단일 글자인 NFC(`홍길동`)를 사용합니다.
  - 단순 바이너리 비교(`valA == valB`)를 수행할 경우 화면에는 똑같이 `홍길동`으로 보여도 바이트 스트림이 달라 `WHERE name = '홍길동'`이 **0건 조회**되는 치명적인 데이터 정합성 장애가 발생합니다. LionSQL Collation은 NFD와 NFC를 유니코드 정규화하여 일치시킵니다.
- **한/영 혼합 컬럼의 대소문자 무시 (CI)**:
  - 한국 기업 데이터에는 순수 한글만 있는 컬럼이 드물며, 브랜드명과 영문 인명이 항상 혼용됩니다(`삼성 Galaxy S24 Ultra`, `John Park`).
  - `WHERE model = '삼성 galaxy s24 ultra'` 검색 시 영문 대소문자를 무시하면서 한글 정합성을 유지해야 합니다. CI Collation이 없으면 `LOWER(model) = LOWER(...)`를 강제하여 B+Tree 인덱스를 파괴하고 풀 테이블 스캔을 유발합니다.
- **국어사전식 정렬 vs 단순 바이트 정렬**:
  - 단순 바이트 정렬 시 특수문자 -> 숫자 -> 영문 대/소문자 -> 한글 순으로 정렬되며, 낱자 자음(`ㄱ`, `ㄴ`)과 완성형 음절(`가`, `나`)의 위치가 멀리 떨어집니다.
  - 표준 한국어 사전식 정렬은 낱자 자음과 완성형 음절이 자연스러운 언어학적 순서로 이어지도록 처리합니다.
- **한국 비즈니스 실무 킬러 기능: 초성 검색 (Chosung Search)**:
  - 배달, 메신저, 금융 등 국내 주요 앱에서는 '홍길동'을 'ㅎㄱㄷ'으로 검색하는 기능이 필수적입니다.
  - 기존에는 테이블마다 `name_chosung VARCHAR(50)` 컬럼을 중복 생성하고 애플리케이션이나 트리거로 동기화해야 했습니다.
  - LionSQL의 Go UDF Collation 구조를 활용하면 `Korean_Chosung`을 등록하여 다음과 같이 네이티브로 처리할 수 있습니다:
    ```sql
    SELECT * FROM users WHERE name COLLATE Korean_Chosung = 'ㅎㄱㄷ';
    ```

#### 2. 독일어 (`German_PhoneBook_CI_AS` vs `German_CI_AS`)
- **DIN 5007-1 (사전 정렬) vs DIN 5007-2 (전화번호부/인명부 정렬)**:
  - 독일어에는 법적으로 규정된 2가지 표준 정렬 방식이 존재합니다:
    - **DIN 5007-1 (표준 사전 정렬)**: 움라우트(`ä`, `ö`, `ü`)를 기본 모음(`a`, `o`, `u`)과 동일하게 취급합니다. 예: `Müller`는 `Muller` 바로 옆에 정렬됩니다.
    - **DIN 5007-2 (전화번호부/인명부 정렬 - `German_PhoneBook_CI_AS`)**: 움라우트를 모음 + `e`로 확장하여 정렬합니다(`ä` -> `ae`, `ö` -> `oe`, `ü` -> `ue`). 예: `Müller`는 `Mueller`로 취급되어 `Muller` **뒤에** 정렬됩니다.
  - 통신사, 관공서, CRM 고객 명부에서는 DIN 5007-2 정렬을 따르지 않으면 고객 조회가 누락되는 문제가 발생합니다.
- **에스체트 / 날카로운 S (`ß`) 및 대문자 변환**:
  - 독일어 `ß`(Eszett)는 전통적으로 대문자가 없어 대문자 변환 시 `SS`로 확장되었습니다(예: `Straße` -> `STRASSE`).
  - 최근 대문자 Eszett인 `ẞ`(U+1E9E)가 유니코드 표준에 도입되었습니다. 독일어 Collation은 `ß`, `SS`, `ẞ` 간의 동등성을 대소문자 무시(`CI`) 환경에서 매끄럽게 처리합니다.

#### 3. 프랑스어 (`French_CI_AS`, `French_CI_AI`)
- **역방향 악센트 정렬 (Backward Accent Sorting)**:
  - 대다수 유럽 언어가 왼쪽에서 오른쪽으로 악센트를 비교하는 반면, 프랑스어 국어사전 표준 정렬은 단어의 보조 악센트 차이를 **오른쪽에서 왼쪽(뒤에서 앞으로, Backward)** 평가합니다.
  - 고전적인 예시: `cote`, `côte`, `coté`, `côté`
    - 일반 좌->우 / 바이트 순 정렬: `cote` < `coté` < `côte` < `côté` (`e`의 악센트를 `o`의 악센트보다 먼저 평가).
    - 표준 프랑스어 사전식 정렬: `cote` < `côte` < `coté` < `côté` (맨 오른쪽 글자 `e`를 먼저 비교하므로, 기본 `e`로 끝나는 `cote`와 `côte`가 악센트 `é`로 끝나는 `coté`와 `côté`보다 반드시 앞에 정렬됨).
  - 역방향 악센트 정렬이 없으면 프랑스어권 행정 및 도서 목록에서 잘못된 정렬이 발생합니다.
- **악센트 구분(`AS`) vs 무시(`AI`)의 정확도**:
  - 프랑스어에서 악센트는 단어의 의미를 완전히 바꿉니다(`peche` = 낚시/죄, `pêche` = 복숭아/낚시, `pêché` = 죄를 지은).
  - `French_CI_AI`는 사용자의 관대한 검색을 지원하고, `French_CS_AS`는 법적/계약 문서의 엄밀성을 보장합니다.
- **합자(Ligature) 처리**:
  - `œ`(`cœur`, `sœur`) 및 `æ`(`ex æquo`)와 같은 합자는 분리된 알파벳 `oe` 및 `ae`와 동등하게 정렬 및 비교되어야 합니다.

---

### 3.4 무컴파일 선언적 커스텀 로케일 확장 아키텍처 (Config-to-Closure Compilation)

LionSQL은 Go 컴파일러가 없는 프로덕션 환경에서도 운영자가 독자적인 로케일 및 정렬 규칙을 즉시 추가할 수 있도록 **"Go 일급 함수(First-Class Function) 및 클로저(Closure) 기반의 무컴파일 선언적 확장"**을 지원합니다. 무거운 JIT 컴파일러나 불안정한 C 동적 라이브러리(`dlopen`) 없이도 순수 Go 프리미티브만으로 나노초 단위의 실행 속도를 보장합니다:

```
[ config/collations.toml 선언적 룰 정의 ]
                  │
                  ▼ (서버 기동 시 1회 콜드패스)
[ BuildDeclarativeCollation 팩토리 컴파일 ]
  - 룰을 정적 룬(Rune) 룩업 테이블/Trie로 인메모리 플랫 빌드
  - 룩업 테이블을 캡처(Capture)하는 Go 익명 클로저 함수 생성
                  │
                  ▼
[ proc.RegisterCollation() SPI 등록 ]
  - 내장 하드코딩 Collation과 100% 동일한 함수 포인터 취급
                  │
                  ▼
[ 쿼리 플래너 사전 바인딩 ──▶ 핫패스 무할당 스트리밍 대조 ]
```

#### 1. Go 일급 함수 및 클로저 캡처 (Zero-JIT Function Factory)
- Go 언어에서 함수는 값으로 취급(First-Class Citizen)되며, 상위 스코프의 변수를 캡처하는 클로저를 자유롭게 생성할 수 있습니다.
- 팩토리 함수 `BuildDeclarativeCollation(cfg)`는 설정 파일에 선언된 문자 치환/확장 룰을 파싱하여 O(1) 조회가 가능한 정적 룬 매핑 배열(또는 플랫 트라이)로 빌드한 후, 이 테이블을 캡처하는 `CollationCompareFunc` 클로저를 반환합니다:
  ```go
  // pkg/proc/collation_factory.go
  func BuildDeclarativeCollation(cfg CollationRuleConfig) CollationCompareFunc {
      // 1. 치환/확장 룰을 O(1) 룬 룩업 테이블로 사전 조립
      lookup := buildStaticRuneLookup(cfg.Expansions, cfg.Substitutions)

      // 2. 룩업 테이블을 캡처한 고성능 비교 클로저 반환
      return func(a, b string) int {
          return streamingCompareWithTable(a, b, lookup, cfg.CaseSensitive)
      }
  }
  ```

#### 2. 무할당 스트리밍 문자열 대조 (`[RULE:ZeroAllocation]`)
- 두 문자열 `a`, `b`를 비교할 때 전체 문자열을 새 메모리에 복사/치환(Allocating String Replace)하지 않습니다.
- 룬 커서(Rune Cursor)를 전진시키며 캡처된 룩업 테이블에 따라 다음 유효 룬을 즉석에서 방출(Streaming Generator)하고 대조하므로, 힙 메모리 할당이 전혀 발생하지 않고 GC 압박을 0으로 억제합니다.

#### 3. 선언적 설정 사양 (`config/collations.toml`)
운영자는 소스코드를 건드리지 않고 TOML 파일만 수정하여 서버 재시작 시 새로운 Collation을 즉시 활성화할 수 있습니다:
```toml
# config/collations.toml
[collations.German_Custom_PhoneBook]
case_sensitive = false
accent_sensitive = true
# 문자 확장 규칙 (Umlaut -> 모음 + e)
expansions = [
  { from = "ä", to = "ae" },
  { from = "ö", to = "oe" },
  { from = "ü", to = "ue" },
  { from = "ß", to = "ss" }
]

[collations.Korean_Chosung_Search]
type = "hangul_chosung"
ignore_space = true
```

---

## 4. 단계별 구현 로드맵 (Phased Implementation Roadmap)

### Phase 1: 관용적 DDL 및 표현식 파싱 (`pkg/proc`, `pkg/engine`)

1. **렉서 확장 (`pkg/proc/lexer.go`)**:
   - `COLLATE`를 예약 키워드 토큰(`tokCOLLATE`)으로 등록.
2. **DDL 컬럼 정의 파싱**:
   - `CREATE TABLE` 및 `ALTER TABLE`에서 컬럼 타입 뒤의 선택적 `COLLATE <ident>` 구문 파싱 지원:
     ```sql
     CREATE TABLE tbl (
         name VARCHAR(100) COLLATE Korean_Wansung_CI_AS NOT NULL
     );
     ```
   - 파싱된 Collation 이름을 `storage.ColumnDef.Collation`에 저장. 생략 시 데이터베이스 기본 Collation 적용.
3. **표현식 파싱**:
   - `WHERE`, `HAVING`, `ORDER BY` 절에서 후치 표현식 수정자(Postfix Modifier)로 `COLLATE` 지원:
     ```sql
     SELECT * FROM tbl WHERE username = @user COLLATE Latin1_General_BIN;
     SELECT * FROM tbl ORDER BY name COLLATE Korean_Wansung_CI_AS;
     ```
4. **실행 시맨틱**:
   - `_BIN` 또는 `_BIN2`로 끝나는 Collation의 경우 엄격한 바이너리 바이트 비교(`valA == valB` 또는 `bytes.Compare`) 수행.
   - 그 외의 경우 무할당 대소문자 무시 비교(`strings.EqualFold`) 수행.

### Phase 2: 동적 서버 Collation 구성 (`pkg/config`, `pkg/wire/tds`)

1. **CLI 플래그 및 설정 파일 바인딩**:
   - CLI 플래그: `--collation="Korean_Wansung_CI_AS"` (기본값: `SQL_Latin1_General_CP1_CI_AS`).
   - `config.toml` 설정:
     ```toml
     [server]
     collation = "Korean_Wansung_CI_AS"
     ```
2. **TDS 핸드셰이크 동기화**:
   - `pkg/wire/tds/server.go`에서 하드코딩된 기본 Collation을 `cfg.Server.Collation`에 해당하는 5바이트 토큰으로 교체.
   - `LOGINACK` 및 `ENVCHANGE`(토큰 타입 `0x07`) 과정에서 활성 Collation 토큰 전송.
3. **메타데이터 동기화**:
   - `SERVERPROPERTY('Collation')` 및 `DATABASEPROPERTYEX(db, 'Collation')`이 구성된 서버 Collation을 동적으로 반환하도록 보장.

### Phase 3: 내장 Collation 시스템 함수 (`pkg/proc/builtins_sys.go`)

SSMS, DBeaver, ORM의 스키마 분석을 지원하기 위해 표준 T-SQL `COLLATIONPROPERTY` 내장 함수 구현:

```sql
SELECT COLLATIONPROPERTY('Korean_Wansung_CI_AS', 'CodePage');   -- 949 반환
SELECT COLLATIONPROPERTY('Korean_Wansung_CI_AS', 'LCID');       -- 1042 반환
SELECT COLLATIONPROPERTY('Korean_Wansung_CI_AS', 'ComparisonStyle'); -- 196609 반환
SELECT COLLATIONPROPERTY('SQL_Latin1_General_CP1_CI_AS', 'CodePage'); -- 1252 반환
```

지원 속성 키:
- `CodePage`: 대응하는 ANSI 코드페이지 (int).
- `LCID`: Windows 로케일 식별자 (int).
- `ComparisonStyle`: 비교 플래그 비트마스크.
- `Version`: Collation 버전 ID.

### Phase 4: 가상 테이블 반환 함수 `::fn_helpcollations()` (`pkg/catalog/virtual/`)

GUI 관리 도구(SSMS, DBeaver)에서 Collation 선택 드롭다운을 구성하기 위해 `fn_helpcollations()` 구현:

- **구현**: `pkg/catalog/virtual/`에 가상 카탈로그 함수 등록.
- **스키마 출력**:
  - `name (VARCHAR(128))`
  - `description (VARCHAR(500))`
- **데이터 내용**: 한국어, 미국 영어, 일본어, 중국어, 독일어, 스페인어, 프랑스어 및 UTF-8 변형을 포함하는 주요 40개 국제 및 SQL Collation 사전 등록.

---

## 5. 엔지니어링 가이드라인 준수 매트릭스

| 가이드라인 / 규칙 | 아키텍처 적용 내역 |
| :--- | :--- |
| **[RULE:PlatformPrimitives] Platform Primitives First** | 무거운 외부 C 라이브러리(ICU) 없이 Go 표준 라이브러리의 네이티브 UTF-8 및 유니코드 비교 프리미티브를 기본 활용. |
| **[RULE:RadicalKISS] Radical KISS & DRY** | 내부 물리 스토리지를 100% UTF-8로 단일화하여 복잡한 레거시 코드페이지 변환 매트릭스를 원천 제거. |
| **[RULE:LexicalSafety] Lexical Robustness** | 파서가 모든 표준 DDL/DML 컨텍스트에서 `COLLATE` 절을 패닉 없이 안전하게 흡수 및 처리. |
| **[RULE:SafeDefaults] Safe Default Configurations** | 설정 생략 시 표준 `SQL_Latin1_General_CP1_CI_AS`를 기본값으로 적용하여 완벽한 하위 호환성 유지. |
| **[RULE:StaticBinary] Pure Static Binaries** | CGo 종속성 없이 순수 Go 구현으로 모든 Collation 메타데이터와 토큰 생성 처리. |
| **[RULE:BoundedLookup] Direct Slice Indexing & Bounded Lookups** | 콜드패스에서 Collation 함수 포인터를 사전 바인딩하여 핫패스 문자열 비교 시 런타임 맵 조회 완전 제거. |
| **[RULE:LatencyBudget] Responsive Latency & Performance Budget** | 기본 무할당 패스트패스에서 서브 나노초(~0.5ns) 비교 레이턴시 보장. |

---

## 부록 (Appendix): 구현 및 사용자 관점의 잠재적 모호성과 해법 (Ambiguities & Resolutions)

프로덕션 환경 배포 시 발생할 수 있는 데이터 불일치 및 런타임 결함을 예방하기 위해, 사용자(DBA/개발자)와 엔진 구현자 관점의 핵심 모호성을 사전 규정하고 아키텍처적 해법을 정의합니다.

### A. 사용자(DBA / 개발자) 관점의 모호성 및 해법

#### 1. 인덱스(B+Tree) 생성 후 설정 변경 시의 정합성 문제
- **모호성**: `collations.toml`에 커스텀 정렬을 정의하고 B+Tree 인덱스를 생성한 후, 나중에 운영자가 TOML의 치환 규칙을 수정하고 서버를 재시작하면 디스크의 인덱스 물리 순서와 런타임 검색 룰이 어긋나 **인덱스 오염(Index Corruption)**이 발생할 수 있습니다.
- **해법**:
  - `collations.toml`의 각 룰셋에 `version = <int>` 및 SHA-256 체크섬을 기록합니다.
  - 인덱스 메타데이터에 생성 당시의 Collation 버전/해시를 보관하고, 기동 시 불일치가 감지되면 명시적 경고 로그 출력 및 해당 인덱스 사용을 격리하며 `REINDEX TABLE <name>` 명령을 강제합니다 (`[RULE:ExplicitError]`).

#### 2. 동등 비교(`=`)와 초성 검색의 대칭성 (단방향 vs 양방향)
- **모호성**: `WHERE name COLLATE Korean_Chosung = 'ㅎㄱㄷ'` 검색 시 `홍길동`이 매칭되는 것은 자명하나, 거꾸로 `WHERE name COLLATE Korean_Chosung = '홍길동'`을 수행했을 때 테이블의 원본 데이터 `ㅎㄱㄷ`도 매칭되어야 하는가에 대한 사용자 혼란이 있습니다.
- **해법 (SQL 표준 동등류 모델)**:
  - SQL의 `COLLATE`는 양방향 동등 비교(`compareFn(a, b) == 0`)를 전제로 하므로, 양쪽 인수를 모두 초성으로 정규화하여 대조하는 대칭적 모델(`'ㅎㄱㄷ' == 'ㅎㄱㄷ'`)로 통일합니다.
  - 이를 통해 `ORDER BY`의 정렬 순서와 `WHERE`의 동등성 판정이 수학적으로 완벽한 일관성을 유지합니다.

#### 3. 서로 다른 Collation 컬럼 간 조인 시 우선순위 (Coercion Rule)
- **모호성**: `colA (Korean_Wansung)`와 `colB (Latin1_General)`를 `colA = colB`로 조인할 때 MSSQL은 Error 468을 발생시킵니다. LionSQL은 무충돌 정책을 취하는데, 누구의 정렬/비교 룰을 따라야 하는가?
- **해법 (SQL-92 표준 강제 변환 순위)**:
  1. 쿼리 내 명시적 `COLLATE` 절이 최우선 승리.
  2. 한쪽이 특수 로케일이고 다른 쪽이 서버 기본값(`SQL_Latin1_General` / Default)인 경우, **더 구체적인 특수 로케일의 룰을 우선 적용**.
  3. 서로 다른 두 특수 로케일이 충돌할 경우, 트랜잭션을 중단하지 않고 **기본 UTF-8 대소문자 무시(CI) 정규화 규칙으로 폴백**하여 실행 보장.

---

### B. 엔진 구현자 관점의 기술적 과제 및 해법

#### 4. 해시 조인(Hash Join)과 GROUP BY에서의 해시 불일치 (치명적 함정)
- **과제**: 정렬(B+Tree, `ORDER BY`)은 `compareFn(a, b)`만 있으면 동작하지만, `GROUP BY`, `DISTINCT`, `Hash Join`은 **해시 함수(`hash(string) uint64`)**를 사용합니다.
  - 수학적 불변식: `compareFn(a, b) == 0` (두 값이 같다면) ➔ **반드시 `hash(a) == hash(b)`여야 합니다.**
  - 예: 독일어 전화번호부 룰에서 `Müller` == `Mueller`라면, 두 문자열의 64비트 해시값도 동일해야 같은 해시 버킷에 들어갑니다.
- **해법**:
  - Collation SPI는 단순 비교 함수만 제공해서는 안 되며, **정규화 키 생성기(`CollationKeyFunc`)** 또는 **일치된 해시 함수(`CollationHashFunc`)**를 반드시 쌍(Pair)으로 등록하도록 강제합니다.
  - 해시 조인 플래너는 Collation이 적용된 컬럼에 대해 해당 전용 해시 함수를 바인딩하여 데이터 누락을 원천 차단합니다.

#### 5. 1:N 문자 확장 시 상태 머신 복잡도 해소
- **과제**: `ä` -> `ae`처럼 1개의 룬이 2개의 룬으로 늘어나는 1:N 확장을 무할당 스트리밍으로 순회하려면 커서와 잔여 버퍼를 추적하는 복잡한 상태 머신이 필요하여 코드 복잡도와 버그 위험이 증가합니다.
- **해법 (`[RULE:BoundedBuffers]`, `[RULE:ZeroAllocation]`)**:
  - 핫패스 문자열의 대다수(99%)는 128바이트 미만입니다.
  - 복잡한 인라인 스트리밍 상태 머신 대신, `sync.Pool`에서 대여한 256바이트 고정 슬라이스에 치환 결과를 일괄 전개(Flatten)한 뒤 CPU 고속 메모리 비교(`bytes.Compare`)를 수행합니다.
  - 힙 할당은 제로(0)로 유지되면서 상태 머신 분기 예측 실패(Branch Misprediction)를 방지하여 실질적인 실행 속도가 더 향상됩니다.

#### 6. 유니코드 NFC/NFD 정규화 시점 최적화 (핫패스 지연 방지)
- **과제**: 수십만 건의 테이블 스캔 핫패스에서 매번 NFC/NFD 정규화 함수를 수행하면 서브 나노초(0.5ns) 성능 목표가 완전히 무너집니다 (수십~수백 ns로 저하).
- **해법 (입력 시 정규화 원칙 - Ingestion-Time Normalization)**:
  - `INSERT` 및 `UPDATE` 시점에 모든 문자열을 표준 유니코드 **NFC로 단 1회 정규화하여 스토리지에 저장**합니다.
  - SQL 리터럴 검색 파라미터(`WHERE name = '홍길동'`)도 파서 단계(콜드패스)에서 1회 NFC로 정규화합니다.
  - 스토리지의 튜플과 쿼리 파라미터가 이미 완벽한 NFC 상태이므로, **핫패스 비교 루프는 무거운 유니코드 분해/결합 연산 없이 즉각적인 0.5ns 비교 속도를 100% 유지**합니다.

