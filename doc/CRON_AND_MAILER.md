# LionSQL Cron Jobs & Mailer Architecture Guide

> - **Created**: 2026-09-29
> - **Last Updated**: 2026-10-17


LionSQL includes an embedded recurring **Cron Scheduler** and an outbound **SMTP Mailer** with built-in transactional email templates. Both services operate directly inside the standalone LionSQL binary without requiring external cron daemons (like `crontab` or `systemd timers`) or third-party mailing agents.

---

## 1. Embedded Cron Jobs Scheduler

### 1.1. Overview & Architecture

The `CronScheduler` (`pkg/api/cron.go`) evaluates registered tasks against a 1-second background ticker. When a schedule matches, tasks are executed asynchronously inside an ambient database session without blocking incoming HTTP or wire protocol traffic.

```
       [ CronScheduler Background Loop (1s ticker) ]
                             │
                             ▼ Matches Schedule?
               ┌─────────────┴─────────────┐
            (Yes)                         (No)
               │                           │
               ▼                           ▼
       [ Job Dispatcher ]              (Wait tick)
               │
      ┌────────┴────────┐
      ▼                 ▼
  [ SQL Query ]   [ Stored Procedure ]
  (Ambient Session)  (Ambient Session)
      │                 │
      └────────┬────────┘
               ▼
   [ Update Job Status & Audit ]
   - last_run: timestamp
   - last_status: success | error
   - last_duration_ms: integer
   - last_error: message
```

### 1.2. Schedule Format (Standard 5-Field Cron)

LionSQL supports standard 5-field cron expressions:
```
┌───────────── minute (0 - 59)
│ ┌───────────── hour (0 - 23)
│ │ ┌───────────── day of month (1 - 31)
│ │ │ ┌───────────── month (1 - 12)
│ │ │ │ ┌───────────── day of week (0 - 7, 0 or 7 = Sunday)
│ │ │ │ │
* * * * *
```

#### Common Expressions:
- `*/5 * * * *`: Every 5 minutes
- `0 * * * *`: Top of every hour
- `0 2 * * *`: Every day at 02:00 AM
- `0 0 * * 0`: Every Sunday at midnight
- `0 0 1 * *`: First day of every month at midnight

### 1.3. Cron REST API

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/api/crons` | List all configured cron jobs |
| `POST` | `/api/crons` | Register a new cron job |
| `PUT` | `/api/crons/:id` | Update an existing cron job |
| `DELETE` | `/api/crons/:id` | Remove a cron job |
| `POST` | `/api/crons/:id/run` | Trigger an immediate manual execution |

#### Example: Register Nightly Purge Job
```http
POST /api/crons
Authorization: Bearer <TOKEN>
Content-Type: application/json

{
  "name": "Purge Expired Sessions",
  "schedule": "0 3 * * *",
  "command_type": "sql",
  "command": "DELETE FROM sessions WHERE expires_at < NOW()",
  "enabled": true
}
```

#### Example: Register Procedure Job
```http
POST /api/crons
Authorization: Bearer <TOKEN>
Content-Type: application/json

{
  "name": "Daily Balance Settlement",
  "schedule": "0 0 * * *",
  "command_type": "procedure",
  "command": "SettleDailyBalances",
  "enabled": true
}
```

---

## 2. Embedded SMTP Mailer & Template Engine

### 2.1. Overview & Architecture

The `Mailer` (`pkg/api/mailer.go`) provides transactional email delivery using Go's native `net/smtp`. It supports TLS/STARTTLS authentication, customizable HTML templates, password reset dispatching, and an in-memory delivery audit ring buffer.

### 2.2. Configuration Parameters

- `host`: SMTP server address (e.g. `smtp.gmail.com`, `smtp.mailgun.org`, `smtp.sendgrid.net`)
- `port`: Port number (`587` for STARTTLS, `465` for Direct SSL/TLS, `25`)
- `username`: SMTP auth username
- `password`: SMTP auth password (masked in API responses as `••••••••`)
- `from_email`: Sender email address (e.g. `no-reply@yourdomain.com`)
- `from_name`: Sender display name (e.g. `LionSQL Notifications`)
- `tls`: Enable direct TLS dial
- `enabled`: Global toggle to enable/disable outgoing email delivery

### 2.3. Built-in Email Templates

1. **`welcome`**: Onboarding welcome email with user name.
2. **`password_reset`**: Secure reset email with one-time action URL and expiration notice.
3. **`verification`**: Email confirmation token and activation button.

### 2.4. Mailer REST API

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/api/settings/mail` | Fetch active SMTP configuration (password masked) |
| `PUT` | `/api/settings/mail` | Update SMTP configuration |
| `POST` | `/api/settings/mail/test` | Dispatch an immediate test email to verify credentials |
| `GET` | `/api/mail/logs` | Fetch recent email dispatch audit log |
| `DELETE` | `/api/mail/logs` | Clear dispatch audit log |
| `POST` | `/api/auth/request-password-reset` | Request password reset token and dispatch email |

#### Example: Save SMTP Settings
```http
PUT /api/settings/mail
Authorization: Bearer <TOKEN>
Content-Type: application/json

{
  "host": "smtp.mailgun.org",
  "port": 587,
  "username": "postmaster@mycompany.com",
  "password": "my-secret-smtp-password",
  "from_email": "alerts@mycompany.com",
  "from_name": "Company Alerts",
  "tls": true,
  "enabled": true
}
```

#### Example: Send Test Email
```http
POST /api/settings/mail/test
Authorization: Bearer <TOKEN>
Content-Type: application/json

{
  "to": "developer@mycompany.com"
}
```

---

## 3. SQL Server DatabaseMail & External Worker Architecture

LionSQL implements full Microsoft SQL Server **DatabaseMail** compatibility (`FEAT-DATABASE-MAIL-EXTERNAL-WORKER-1`). To protect database OLTP performance and isolate external SMTP latency, stored procedure invocations do not block on external sockets; instead, messages are atomically queued into `msdb.dbo.sysmail_mailitems` and dispatched asynchronously by the dedicated `lionsql dbmail-worker`.

### 3.1. Architecture Diagram

```
       [ Client / T-SQL Batch ]
                  │
                  ▼ EXEC msdb.dbo.sp_send_dbmail
       ┌──────────────────────────┐
       │     LionSQL DB Kernel    │
       │ (Atomic Sub-µs INSERT)   │
       └──────────┬───────────────┘
                  │
                  ▼
       [ msdb.dbo.sysmail_mailitems ] (sent_status = 'unsent')
                  │
                  ▼ Async Polling / Processing
       ┌──────────────────────────┐
       │   lionsql dbmail-worker  │ ◄── External Process (CLI)
       └──────────┬───────────────┘
                  │
                  ├────────────────────────┐
                  ▼                        ▼
     [ msdb.dbo.sysmail_log ]     [ External SMTP Server ]
     (Status: sent / failed)      (TLS / STARTTLS Dispatch)
```

### 3.2. T-SQL Stored Procedure: `msdb.dbo.sp_send_dbmail`

#### Syntax & Parameters:
```sql
EXEC msdb.dbo.sp_send_dbmail
    [@profile_name =] 'profile_name',
    [@recipients =] 'recipients [; ...n]',
    [@copy_recipients =] 'copy_recipients [; ...n]',
    [@blind_copy_recipients =] 'blind_copy_recipients [; ...n]',
    [@from_address =] 'from_address',
    [@reply_to =] 'reply_to',
    [@subject =] 'subject',
    [@body =] 'body',
    [@body_format =] 'body_format', -- 'TEXT' (default) or 'HTML'
    [@importance =] 'importance',   -- 'LOW', 'NORMAL' (default), 'HIGH'
    [@sensitivity =] 'sensitivity', -- 'NORMAL' (default), 'PERSONAL', 'PRIVATE', 'CONFIDENTIAL'
    [@file_attachments =] 'attachments [; ...n]',
    [@query =] 'query',
    [@mailitem_id =] @mailitem_id OUTPUT;
```

#### Example Usage in T-SQL:
```sql
DECLARE @mailId INT;
EXEC msdb.dbo.sp_send_dbmail
    @profile_name = 'Notifications',
    @recipients = 'ops@mycompany.com; dev@mycompany.com',
    @copy_recipients = 'manager@mycompany.com',
    @subject = 'Daily Settlement Complete',
    @body = '<h1>Settlement Succeeded</h1><p>Processed 10,240 records.</p>',
    @body_format = 'HTML',
    @mailitem_id = @mailId OUTPUT;

SELECT @mailId AS QueuedMailID;
```

### 3.3. DatabaseMail Queue Tables

1. **`msdb.dbo.sysmail_mailitems`**:
   - `mailitem_id INT PRIMARY KEY`: Unique mail identifier.
   - `profile_name VARCHAR(128)`: Associated configuration profile.
   - `recipients VARCHAR`: Primary destination addresses.
   - `copy_recipients VARCHAR`: Carbon copy (CC) addresses.
   - `blind_copy_recipients VARCHAR`: Blind carbon copy (BCC) addresses.
   - `from_address VARCHAR`: Custom sender address (falls back to worker default).
   - `subject VARCHAR`: Message subject.
   - `body VARCHAR`: Plain text or HTML payload.
   - `body_format VARCHAR(20)`: `TEXT` or `HTML`.
   - `send_request_date DATETIME`: Enqueue timestamp.
   - `sent_date DATETIME`: Completion timestamp (NULL while pending).
   - `sent_status VARCHAR(20)`: `unsent`, `sent`, `failed`.
   - `last_mod_date DATETIME`: Last state modification timestamp.

2. **`msdb.dbo.sysmail_log`**:
   - `log_id INT PRIMARY KEY`: Sequential log identifier.
   - `mailitem_id INT`: Reference to target item.
   - `event_type VARCHAR(20)`: `information`, `warning`, `error`.
   - `log_date DATETIME`: Event timestamp.
   - `description VARCHAR`: Detailed transmission report or SMTP server diagnostic.

### 3.4. Running the DatabaseMail Worker CLI

To process the queue and dispatch outgoing emails, execute the standalone worker:
```bash
# Basic invocation with STARTTLS
lionsql dbmail-worker \
    -db-url "postgres://127.0.0.1:5433/master?sslmode=disable" \
    -smtp-host "smtp.gmail.com" \
    -smtp-port 587 \
    -smtp-user "alerts@mycompany.com" \
    -smtp-pass "super-secret-app-password" \
    -from-email "alerts@mycompany.com" \
    -from-name "LionSQL Alerts"

# Advanced configuration options
lionsql dbmail-worker \
    -db-url "postgres://127.0.0.1:5433/master?sslmode=disable" \
    -smtp-host "smtp.mailgun.org" \
    -smtp-port 587 \
    -smtp-user "postmaster@mycompany.com" \
    -smtp-pass "key-xxxx" \
    -from-email "no-reply@mycompany.com" \
    -poll-interval-ms 500 \
    -batch-size 50 \
    -tls=true
```

### 3.5. REST API for DatabaseMail Queue Inspection

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/api/dbmail/items?limit=50` | Inspect queued mail items and their delivery status |
| `GET` | `/api/dbmail/logs?limit=50` | Inspect transmission event logs and diagnostics |

