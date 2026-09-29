# SQLion Cron Jobs & Mailer Architecture Guide

SQLion includes an embedded recurring **Cron Scheduler** and an outbound **SMTP Mailer** with built-in transactional email templates. Both services operate directly inside the standalone SQLion binary without requiring external cron daemons (like `crontab` or `systemd timers`) or third-party mailing agents.

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

SQLion supports standard 5-field cron expressions:
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
- `from_name`: Sender display name (e.g. `SQLion Notifications`)
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
