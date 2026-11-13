# LionSQL 메일러 아키텍처 개편 및 SQL Server DatabaseMail 외부 프로세스 분리 계획서 (`DATABASE_MAIL_EXTERNAL_WORKER_PLAN.ko.md`)

## 1. 개요 및 배경

### 1.1 현황 및 문제점
- **현재 구조**: LionSQL의 HTTP API 레이어(`pkg/api/mailer.go`, `mailer_handlers.go`) 내부에 Go 표준 라이브러리 `net/smtp` 기반 메일러가 내장되어 있습니다.
- **아키텍처적 불일치**:
  1. **데이터베이스 엔진 정체성 침해**: 초고성능 OLTP 데이터베이스 엔진 커널 내부에 직접 SMTP 소켓 통신 및 이메일 템플릿 렌더링 로직이 존재하는 것은 관심사 분리(SoC) 원칙과 `[RULE:RadicalKISS]` 원칙에 위배됩니다.
  2. **외부 인프라 장애 전파 위험**: SMTP 서버 지연, 방화벽 차단, TLS 핸드셰이크 타임아웃, 재시도 큐 적체 등의 네트워크 장애 요인이 데이터베이스 프로세스 리소스를 잠식할 위험이 있습니다.
  3. **SQL Server 호환성 부재**: 현재 메일러는 독자적인 REST API(`/api/mail/*`)와 웹 대시보드 위주로 동작하며, 실제 Microsoft SQL Server에서 널리 쓰이는 표준 T-SQL 저장 프로시저 `msdb.dbo.sp_send_dbmail` 인터페이스를 제공하지 못하고 있습니다.

### 1.2 의사결정 목표
- 데이터베이스 메인 커널에서 SMTP 네트워크 I/O를 완전히 격리합니다.
- 레거시 Microsoft SQL Server 환경과의 호환성을 위해 `sp_send_dbmail` 호출을 원활히 수용합니다.

---

## 2. 두 가지 아키텍처 대안 비교 및 결정

### 대안 1: 메일러 완전 제거 및 CDC Outbox / Webhook 일원화
- **구조**:
  - `pkg/api/mailer.go`, `mailer_handlers.go`, Web Studio Mail 탭을 완전히 제거.
  - 이메일/알림이 필요한 경우, 이미 구현된 트랜잭셔널 아웃박스(`pkg/cdc/outbox.go`, `WebhookSink`, `KafkaRESTSink`)를 통해 외부 메시지 브로커나 외부 이메일 SaaS(Resend, SendGrid, AWS SES)로 이벤트를 릴레이.
- **장점**:
  - 극단적인 단순성 달성 (`[RULE:RadicalKISS]`).
  - 추가적인 백그라운드 프로세스 관리 불필요. 완벽한 단일 바이너리 자족성.
- **단점**:
  - SQL Server의 `EXEC msdb.dbo.sp_send_dbmail` T-SQL 저장 프로시저를 사용하는 기존 엔터프라이즈 마이그레이션 스크립트를 수용할 수 없음.

---

### 대안 2: SQL Server DatabaseMail 호환 외부 프로세스 분리 (선택됨)
- **구조**:
  - SQL Server의 `DatabaseMail.exe` 아키텍처를 LionSQL에 맞게 차용.
  - **DB 내부**: 시스템 메일 큐 테이블(`msdb.dbo.sysmail_mailitems`) 및 시스템 프로시저 `sp_send_dbmail` 제공. T-SQL 실행 시 트랜잭션 내에서 큐 테이블에 원자적 `INSERT`만 수행하고 서브 마이크로초 단위로 즉시 반환.
  - **외부 워커 프로세스**: 단일 바이너리 원칙(`Invariant 4`)을 보존하기 위해 별도 바이너리 대신 `bin/lionsql dbmail-worker` 서브커맨드(또는 독립 바이너리 `bin/databasemail`)로 제공. 백그라운드에서 큐 테이블을 폴링(또는 CDC 스트림 구독)하여 비동기로 SMTP 발송을 전담.
- **장점**:
  - **100% SQL Server T-SQL 호환성**: `sp_send_dbmail` 구문을 그대로 사용할 수 있어 온프레미스 SQL Server 배치 및 트리거 마이그레이션 완벽 지원.
  - **완벽한 장애 격리(Fault Tolerance)**: 메일 서버가 다운되거나 네트워크가 끊겨도 DB 트랜잭션과 쿼리는 0%의 영향만 받음 (큐에 적재된 상태 유지).
  - **단일 바이너리 배포 편의성 유지**: 동일한 정적 실행 파일(`bin/lionsql`)에서 서브커맨드로 구동되므로 추가 패키징 배포 부담 없음.
- **결정**:
  - **이번 단계에서는 SQL Server 마이그레이션 호환성을 제공하기 위해 대안 2(외부 워커 프로세스 분리)를 채택합니다.**

---

## 3. 상세 설계 명세 (대안 2)

```
+---------------------------------------------------------------+
|                      LionSQL DB Engine                        |
|                                                               |
|  [T-SQL Batch] ---> EXEC msdb.dbo.sp_send_dbmail              |
|                             |                                 |
|                             v                                 |
|                 [Atomic INSERT (Sub-µs)]                      |
|                             |                                 |
|                             v                                 |
|            Table: msdb.dbo.sysmail_mailitems                  |
|            (mailitem_id, recipients, subject, body, status)   |
+---------------------------------------------------------------+
                               |
               Polling / CDC Stream (Async)
                               v
+---------------------------------------------------------------+
|     External Worker: lionsql dbmail-worker (독립 프로세스)      |
|                                                               |
|  1. Status = 'unsent' 항목 잠금 획득 (SELECT ... FOR UPDATE)     |
|  2. SMTP 서버 연결 및 발송 (지수 백오프, 타임아웃, 재시도)     |
|  3. 성공/실패 시 상태 갱신 (status = 'sent' / 'failed')         |
|  4. msdb.dbo.sysmail_log 에 감사 로그 기록                      |
+---------------------------------------------------------------+
                               |
                               v
                    [External SMTP Server]
```

### 3.1 DB 내부 스키마 및 프로시저 명세
1. **메일 큐 테이블 (`msdb.dbo.sysmail_mailitems`)**:
   - `mailitem_id INT IDENTITY(1,1) PRIMARY KEY`
   - `recipients VARCHAR(MAX) NOT NULL`
   - `copy_recipients VARCHAR(MAX)`
   - `blind_copy_recipients VARCHAR(MAX)`
   - `subject NVARCHAR(255)`
   - `body NVARCHAR(MAX)`
   - `body_format VARCHAR(20) DEFAULT 'TEXT'` (`TEXT` 또는 `HTML`)
   - `send_request_date DATETIME DEFAULT CURRENT_TIMESTAMP`
   - `sent_date DATETIME NULL`
   - `sent_status VARCHAR(50) DEFAULT 'unsent'` (`unsent`, `sent`, `failed`, `retrying`)
   - `last_mod_date DATETIME DEFAULT CURRENT_TIMESTAMP`

2. **메일 감사 로그 테이블 (`msdb.dbo.sysmail_log`)**:
   - `log_id INT IDENTITY(1,1) PRIMARY KEY`
   - `mailitem_id INT REFERENCES sysmail_mailitems(mailitem_id)`
   - `event_type VARCHAR(50)` (`information`, `warning`, `error`)
   - `log_date DATETIME DEFAULT CURRENT_TIMESTAMP`
   - `description NVARCHAR(MAX)`

3. **시스템 저장 프로시저 (`sp_send_dbmail`)**:
   - 시그니처:
     ```sql
     CREATE PROCEDURE msdb.dbo.sp_send_dbmail
         @profile_name sysname = NULL,
         @recipients VARCHAR(MAX),
         @copy_recipients VARCHAR(MAX) = NULL,
         @blind_copy_recipients VARCHAR(MAX) = NULL,
         @subject NVARCHAR(255) = NULL,
         @body NVARCHAR(MAX) = NULL,
         @body_format VARCHAR(20) = 'TEXT',
         @mailitem_id INT OUTPUT
     ```
   - 동작: 내부 큐 테이블에 `INSERT`하고 생성된 `@mailitem_id`를 OUTPUT으로 반환. (외부 네트워크 호출 일절 없음).

### 3.2 외부 워커 프로세스 (`bin/lionsql dbmail-worker`)
- **CLI 실행 명령**:
  ```bash
  lionsql dbmail-worker --db-url "postgres://localhost:5433/master" --smtp-host "smtp.gmail.com" --smtp-port 587 --smtp-user "..." --smtp-pass "..."
  ```
- **주요 기능**:
  - `sysmail_mailitems` 테이블에서 `sent_status = 'unsent'` 레코드를 원자적으로 배치 폴링.
  - 외부 SMTP 전송 (TLS/STARTTLS).
  - 지수 백오프 기반 최대 5회 재시도 (`[RULE:NonBlockingSink]`, `[RULE:LatencyBudget]`).
  - 결과에 따라 `sysmail_mailitems.sent_status`를 `sent` 또는 `failed`로 업데이트하고 `sysmail_log`에 기록.
  - 우아한 종료(`SIGINT`/`SIGTERM`) 지원 (`[RULE:CleanTeardown]`).

---

## 4. 단계별 구현 마일스톤

- [ ] **Phase 1: DB 스키마 및 sp_send_dbmail 구현**
  - `msdb.dbo.sysmail_mailitems` 및 `msdb.dbo.sysmail_log` 시스템 카탈로그 테이블 시딩
  - `sp_send_dbmail` T-SQL 저장 프로시저 내장 등록 및 파라미터 유효성 검증
  - 단위 테스트: `sp_send_dbmail` 호출 시 큐 테이블에 올바르게 적재되고 `@mailitem_id` 반환 검증
- [ ] **Phase 2: DatabaseMail 워커 엔진 (`pkg/dbmail/`) 및 CLI 서브커맨드**
  - `pkg/dbmail/worker.go`, `config.go`, `dispatcher.go` 구현
  - `cmd/lionsql`에 `dbmail-worker` 서브커맨드 등록
  - SMTP 모의 서버(`net/smtp` mock)를 활용한 결정적 E2E 테스트 작성 (`[RULE:DeterministicTesting]`)
- [ ] **Phase 3: 기존 HTTP API 메일러 정리 및 Web Studio 연동**
  - DB 커널 내부의 불필요한 직결 발송 코드 정리
  - Web Studio 대시보드의 Mail 탭을 "DatabaseMail Queue & Worker Monitor"로 전환하여 큐 적재 현황 및 감사 로그 조회 지원
- [ ] **Phase 4: 회귀 검증 및 문서화**
  - 3-Stage Pipeline 통과 (`go build` ➔ `go test -race` ➔ `git commit`)
  - `doc/CRON_AND_MAILER.md` 및 `CHANGELOG.md` 업데이트

---

## 5. 실행 순서 안내

1. **현재 활성 과제 매듭**:
   - 현재 구현 및 전체 race 테스트를 100% 통과한 **`#13 FIX-TDS-RPC-RETURN-STATUS-1`** 작업을 먼저 3-Stage Pipeline에 따라 커밋하고 태스크 상태를 동기화합니다.
2. **DatabaseMail 구현 착수**:
   - `#13` 완료 직후 본 계획서의 Phase 1부터 구현을 시작합니다.
