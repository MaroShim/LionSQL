# LionSQL & SSMS (SQL Server Management Studio) 연동 가이드 (`SSMS_INTEGRATION_GUIDE.ko.md`)

이 문서는 Microsoft의 공식 데이터베이스 관리 도구인 **[SQL Server Management Studio (SSMS)](https://learn.microsoft.com/ko-kr/sql/ssms/download-sql-server-management-studio-ssms)**를 **LionSQL**의 네이티브 Microsoft SQL Server TDS(Tabular Data Stream) 리스너(`TCP :1433`)와 연동하여 데이터베이스를 관리하고 T-SQL 쿼리를 실행하는 공식 사용자 가이드입니다.

> [English Version](SSMS_INTEGRATION_GUIDE.md)

---

## 1. 개요 (Overview)

LionSQL은 외부 에뮬레이터나 드라이버 변환 레이어 없이 마이크로소프트의 공식 **TDS 7.x 및 8.0 프로토콜(`TCP :1433`)**을 네이티브로 직접 리스닝합니다. 따라서 Windows 데스크톱의 표준 관리 도구인 SSMS 18.x, 19.x, 20.x에서 순정 Microsoft SQL Server와 완전히 동일한 방식으로 접속하여 개체 탐색, T-SQL 쿼리 실행, 저장 프로시저 디버깅 및 트랜잭션 관리를 수행할 수 있습니다.

```
[ 클라이언트 환경: Windows ]
   ┌────────────────────────────────────────────────────────┐
   │ Microsoft SQL Server Management Studio (SSMS 18/19/20) │
   │  - 개체 탐색기 (Object Explorer)                       │
   │  - T-SQL 쿼리 편집기 (Query Editor)                    │
   └──────────────────────────┬─────────────────────────────┘
                              │ Microsoft TDS Protocol (:1433)
                              ▼
[ 서버 환경: LionSQL Standalone Binary (Windows / macOS / Linux) ]
   ┌────────────────────────────────────────────────────────┐
   │ TDS 7.x / 8.0 Wire Listener (:1433)                    │
   │  ├── PreLogin & SSL/TLS 핸드셰이크                     │
   │  ├── 토큰 스트림 파서 & 가상 카탈로그 DMV 라우터       │
   │  └── T-SQL VDBE 바이트코드 엔진 & In-Memory/WT 코어    │
   └────────────────────────────────────────────────────────┘
```

---

## 2. 사전 준비 사항 (Prerequisites)

1. **LionSQL 서버 기동**:
   LionSQL 바이너리가 TDS 리스너(`:1433`)와 함께 정상 구동 중이어야 합니다.
   ```bash
   # Windows PowerShell / CMD
   .\bin\lionsql.exe server --data-dir .\data --tds-port 1433

   # Linux / macOS
   ./bin/lionsql server --data-dir ./data --tds-port 1433
   ```
2. **SSMS 설치**:
   - Microsoft 공식 웹사이트에서 SSMS 19.x 또는 20.x 최신 버전을 다운로드하여 설치합니다.
3. **네트워크 포트 확인**:
   - `TCP 1433` 포트가 열려 있는지 확인합니다 (원격 접속 시 Windows 방화벽 인바운드 규칙 등록 필요).

---

## 3. SSMS 접속 설정 단계별 가이드 (Step-by-Step Connection)

SSMS를 실행하고 **서버에 연결 (Connect to Server)** 대화 상자에서 다음과 같이 구성합니다:

### 3.1. 로그인 탭 (Login Tab)

| 항목 | 입력값 / 설정 | 설명 |
| :--- | :--- | :--- |
| **서버 유형 (Server type)** | `데이터베이스 엔진` (`Database Engine`) | 기본값 유지 |
| **서버 이름 (Server name)** | `127.0.0.1,1433` 또는 `localhost,1433` | **주의**: 포트 번호 앞은 콜론(`:`)이 아닌 **쉼표(`,`)**를 사용해야 합니다. |
| **인증 (Authentication)** | `SQL Server 인증` (`SQL Server Authentication`) | 표준 TDS 계정 인증 사용 |
| **로그인 (Login)** | `sa` (또는 생성한 사용자 계정) | 기본 시스템 관리자 계정 |
| **암호 (Password)** | `1234567890` (또는 지정한 패스워드) | LionSQL 초기 기본 관리자 암호 |

### 3.2. 연결 속성 탭 (Connection Properties - 필수 수정 항목 1개)

나머지 모든 네트워크 및 세션 옵션은 **디폴트 기본값을 그대로 유지**하면 되며, SSMS 19.x / 20.x 최신 버전 기준 **단 하나의 옵션만 필수로 체크**합니다:

1. 로그인 창 우측 하단의 **옵션 (Options >>)** 버튼을 누릅니다.
2. **연결 속성 (Connection Properties)** 탭으로 이동합니다.
3. **`서버 인증서 신뢰 (Trust server certificate)`** 항목을 **체크 (`True`)**합니다.
   > **이유**: 사설 개발 환경의 자체 생성 TLS 핸드셰이크 시 `pre-login handshake` 인증서 신뢰 거부 오류를 방지하기 위함입니다.
4. **연결 (Connect)**을 누르면 즉시 접속됩니다.

---

## 4. SSMS 개체 탐색기 (Object Explorer) 지원 내역

LionSQL은 SSMS 개체 탐색기가 계층형 트리를 렌더링할 때 질의하는 핵심 시스템 카탈로그 및 동적 관리 뷰(DMV)를 완벽히 내장 지원합니다:

### 4.1. 지원되는 시스템 DMV 및 메타데이터 뷰
- **호스트 및 시스템 정보**:
  - `sys.dm_os_host_info`: 호스트 OS 플랫폼(`Windows`, `Linux`, `macOS`), 아키텍처(`64-bit`), 빌드 릴리스 정보 제공.
  - `sys.dm_os_sys_info`: 논리 CPU 코어 수, 물리/가상 메모리 용량 및 서버 기동 시간 제공.
- **세션 및 연결 상태**:
  - `sys.dm_exec_sessions`: 활성 세션 SPID, 로그인 계정, 호스트명, 상태(`running`, `sleeping`).
  - `sys.dm_exec_connections`: 클라이언트 IP, 네트워크 전송 프로토콜(`TCP`), 연결 시간.
- **카탈로그 객체 탐색**:
  - `sys.databases` / `sysdatabases`: 데이터베이스 목록 및 기본 상태.
  - `sys.tables` / `sysobjects`: 사용자 정의 테이블 목록, 오브젝트 ID, 스키마 ID.
  - `sys.views` / `sysviews`: 뷰 정의 및 생성 일자.
  - `sys.columns`: 테이블/뷰의 컬럼명, 데이터 타입 ID, 최대 길이, 널 허용 여부.
  - `sys.indexes` / `sys.index_columns`: 기본키(PK), 유니크 인덱스, 보조 인덱스 정의.
  - `sys.procedures` / `sys.parameters`: 저장 프로시저 명칭 및 입력/출력 매개변수 명세.
- **보안 및 서버 엔드포인트**:
  - `sys.server_principals`: 서버 로그인 계정(`sa`, `public`, `sysadmin`).
  - `sys.endpoints`: TDS TCP 수신 엔드포인트 상태(`STARTED`).

---

## 5. T-SQL 쿼리 편집기 활용 (Query Editor)

SSMS 툴바에서 **새 쿼리 (New Query)**를 누르거나 `Ctrl + N`을 눌러 T-SQL 편집기를 열고 즉시 고성능 쿼리를 실행할 수 있습니다:

### 5.1. DDL 및 테이블 생성
```sql
CREATE TABLE dbo.Customers (
    CustomerID INT PRIMARY KEY,
    CustomerName NVARCHAR(100) NOT NULL,
    Email VARCHAR(100) UNIQUE,
    Balance DECIMAL(18, 2) DEFAULT 0.00,
    CreatedAt DATETIME DEFAULT GETDATE()
);
GO
```

### 5.2. 트랜잭션 및 DML 실행
LionSQL의 Non-blocking ACID 트랜잭션 엔진 위에서 표준 T-SQL 문법이 동작합니다:
```sql
BEGIN TRANSACTION;

INSERT INTO dbo.Customers (CustomerID, CustomerName, Email, Balance)
VALUES (1, N'홍길동', 'hong@example.com', 150000.00);

UPDATE dbo.Customers
SET Balance = Balance + 50000.00
WHERE CustomerID = 1;

COMMIT TRANSACTION;
GO

SELECT * FROM dbo.Customers WHERE CustomerID = 1;
GO
```

### 5.3. 저장 프로시저 작성 및 호출
```sql
CREATE PROCEDURE dbo.usp_AddCustomer
    @ID INT,
    @Name NVARCHAR(100),
    @Email VARCHAR(100),
    @Balance DECIMAL(18, 2)
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.Customers (CustomerID, CustomerName, Email, Balance)
    VALUES (@ID, @Name, @Email, @Balance);
END;
GO

-- 저장 프로시저 호출
EXEC dbo.usp_AddCustomer @ID = 2, @Name = N'이순신', @Email = 'lee@example.com', @Balance = 200000.00;
GO
```

---

## 6. 트러블슈팅 및 문제 해결 (Troubleshooting)

### Q1. `sys.dm_os_host_info` 50000 에러가 발생하며 접속이 끊어집니다.
- **원인**: 구버전 LionSQL 바이너리에는 SSMS 최신 버전이 접속 시 요구하는 `sys.dm_os_host_info` 가상 뷰가 누락되어 있었습니다.
- **해결책**: 최신 릴리스 바이너리(`bin/lionsql.exe`)로 교체하여 재기동하십시오. LionSQL v1.0 최신 패치에서 해당 DMV가 공식 내장되어 완벽히 호환됩니다.

### Q2. `A connection was successfully established with the server, but then an error occurred during the pre-login handshake` 오류가 발생합니다.
- **원인**: SSMS 19/20은 기본적으로 SSL 암호화 검증을 강제하지만, 로컬 자체 서명 인증서를 신뢰할 수 없어 발생합니다.
- **해결책**:
  1. SSMS 로그인 창 하단의 **옵션 (Options >>)**을 엽니다.
  2. **연결 속성 (Connection Properties)** 탭에서 **서버 인증서 신뢰 (Trust server certificate)** 항목을 **체크 (`True`)**합니다.
  3. **암호화 (Encrypt)**를 `선택 사항 (Optional)`으로 변경하고 재접속합니다.

### Q3. 서버 이름에 포트를 콜론(`:1433`)으로 넣었더니 접속이 안 됩니다.
- **원인**: Microsoft SQL Server 및 SSMS의 공식 TDS 주소 표기법은 콜론이 아닌 **쉼표(`,`)**입니다.
- **해결책**: `127.0.0.1:1433`이 아니라 반드시 **`127.0.0.1,1433`**으로 입력하십시오.

### Q4. SSMS에서 `USE [DatabaseName]` 구문을 실행하면 어떻게 동작하나요?
- **원인**: LionSQL은 안정성과 고동시성 격리를 위해 프로세스당 단일 카탈로그(Single Catalog per Process) 불변식을 유지합니다 ([`GEMINI.md`](../GEMINI.md)).
- **동작 방식**: SSMS 세션 호환성을 위해 `USE master`, `USE [SalesDB]` 등의 구문은 **에러 없이 정상 수용되는 Tolerant Virtual No-Op**으로 작동하며, `DB_NAME()` 및 세션 상태 토큰을 동기화합니다.

---

## 7. 관련 참고 문서
- [LionSQL 클라이언트 도구 연동 종합 가이드 (`CLIENT_TOOL_INTEGRATION.md`)](../docs/CLIENT_TOOL_INTEGRATION.md)
- [T-SQL 문법 레퍼런스 매뉴얼 (`TSQL_REFERENCE.md`)](../docs/TSQL_REFERENCE.md)
- [LionSQL 웹 스튜디오 가이드 (`WEB_STUDIO_GUIDE.ko.md`)](../docs/WEB_STUDIO_GUIDE.ko.md)
