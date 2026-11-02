# LionSQL Umami 연동 가이드

> - **최초 작성일**: 2026-11-01
> - **대상 애플리케이션**: Umami v2 (웹 분석 플랫폼)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`)

---

## 1. 개요

Umami는 구글 애널리틱스를 대체할 수 있는 경량 오픈소스 웹 분석 플랫폼입니다.
LionSQL은 PostgreSQL 와이어 프로토콜(TCP :5433)을 기본 내장하고 있어, 별도의 프록시나 외부 데이터베이스 없이 LionSQL 단일 실행 파일만으로 Umami를 구동할 수 있습니다.

---

## 2. 사전 준비 사항

- LionSQL 바이너리 (`bin/lionsql`)
- Docker 또는 Podman (공식 Umami 이미지 구동용)

---

## 3. 구동 절차

### 1단계: LionSQL 데몬 실행
```bash
./bin/lionsql -dir data_umami -engine wiredtiger -pg 0.0.0.0:5433 -admin-pass="YourSecurePassword!"
```

### 2단계: Umami 컨테이너 구동
Umami 컨테이너 실행 시 `DATABASE_URL` 환경 변수에 LionSQL PGWire 접속 주소를 지정합니다. (호스트 환경 연결을 위해 `host.docker.internal` 사용)

```bash
docker run -d --name umami \
  -p 3001:3000 \
  -e DATABASE_URL="postgresql://admin:YourSecurePassword!@host.docker.internal:5433/lionsql?sslmode=disable" \
  -e APP_SECRET="lionsql-umami-secret-key-32chars-min!" \
  ghcr.io/umami-software/umami:postgresql-latest
```

### 3단계: 접속 및 초기 로그인
브라우저에서 `http://localhost:3001`에 접속하여 기본 관리자 계정으로 로그인합니다:
- **Username**: `admin`
- **Password**: `umami`

---

## 4. 웹사이트 등록 및 트래킹 스크립트 삽입

1. 대시보드 상단의 **Settings** > **Websites** > **Add Website**를 클릭합니다.
2. 사이트 이름(Name)과 도메인(Domain)을 입력하고 저장합니다.
3. 생성된 웹사이트의 **Tracking code**를 복사하여 모니터링할 웹사이트의 `<head>` 태그 내에 삽입합니다:
   ```html
   <script defer src="http://localhost:3001/script.js" data-website-id="<생성된_웹사이트_ID>"></script>
   ```
