# LionSQL Umami Integration Guide

> - **Created**: 2026-11-01
> - **Target Application**: Umami v2 (Web Analytics Platform)
> - **Protocol**: PostgreSQL Wire Protocol v3 (`TCP :5433`)

---

## 1. Overview

Umami is a privacy-focused open-source alternative to Google Analytics.
LionSQL natively supports the PostgreSQL Wire Protocol (TCP :5433), enabling you to run Umami directly against a single self-contained LionSQL executable without external database daemons or connection proxies.

---

## 2. Prerequisites

- LionSQL binary (`bin/lionsql`)
- Docker or Podman (for running the official Umami container)

---

## 3. Step-by-Step Setup

### Step 1: Start LionSQL Daemon
```bash
./bin/lionsql -dir data_umami -engine wiredtiger -pg 0.0.0.0:5433 -admin-pass="YourSecurePassword!"
```

### Step 2: Run Umami Container
Point the `DATABASE_URL` environment variable to LionSQL's PGWire listener (`host.docker.internal` allows the container to reach the host):

```bash
docker run -d --name umami \
  -p 3001:3000 \
  -e DATABASE_URL="postgresql://admin:YourSecurePassword!@host.docker.internal:5433/lionsql?sslmode=disable" \
  -e APP_SECRET="lionsql-umami-secret-key-32chars-min!" \
  ghcr.io/umami-software/umami:postgresql-latest
```

### Step 3: Access and Initial Login
Open `http://localhost:3001` in your browser and sign in with the default credentials:
- **Username**: `admin`
- **Password**: `umami`

---

## 4. Website Registration & Tracking Script

1. In the dashboard, navigate to **Settings** > **Websites** > **Add Website**.
2. Enter your site Name and Domain, then save.
3. Copy the tracking snippet and paste it into the `<head>` section of your website:
   ```html
   <script defer src="http://localhost:3001/script.js" data-website-id="<YOUR_WEBSITE_ID>"></script>
   ```
