# LionSQL 공식 Docker 컨테이너 이미지 규격 및 GHCR 자동 릴리즈 파이프라인

## 1. 개요 및 배경
LionSQL은 외부 의존성이 전혀 없는 독립 단일 정적 바이너리(`lionsql`)로 빌드되는 데이터베이스 엔진입니다. 본 문서는 컨테이너 오케스트레이션(Kubernetes, Docker Compose, AWS ECS) 환경에서 LionSQL을 즉시 배포할 수 있도록 설계된 공식 Docker 이미지 규격과 GitHub Container Registry(GHCR) 자동 빌드/배포 파이프라인의 설계 및 구현 내용을 정리합니다.

---

## 2. 런타임 베이스 이미지 선정: Scratch vs Alpine 3.20

### 2.1 선정 기준 비교
| 평가 항목 | `scratch` (Pure Bare Metal) | `alpine:3.20` (경량 배포판) | 채택 사유 |
| :--- | :--- | :--- | :--- |
| **이미지 크기** | 약 35MB (바이너리 단독) | 약 45MB (+10MB Alpine 레이어) | 10MB 차이는 컨테이너 배포 속도에 무시할 수준 |
| **TLS CA 인증서** | 없음 (외부에서 별도 복사 필요) | `ca-certificates` 패키지 제공 | Database Mail, 외부 SMTP, S3 백업에 필수 |
| **시간대(Timezone)** | 기본 UTC 고정 | `tzdata` 패키지 제공 | T-SQL 시간 함수(`AT TIME ZONE`, `GETDATE()`) 필수 |
| **컨테이너 헬스체크** | 자체 Go 헬스체크 바이너리 필요 | 기본 `wget` 명령어로 HTTP 체크 | `HEALTHCHECK` 지시어 즉시 활용 가능 |
| **쉘 디버깅** | 쉘 없음 (`docker exec` 불가) | `/bin/sh` 내장 | 장애 발생 시 컨테이너 진입 및 로그 확인 가능 |

### 2.2 결정
최종 런타임 베이스로 **`alpine:3.20`**을 공식 채택하였습니다. 10MB의 미미한 이미지 크기 증가 대비 TLS CA 신뢰 저장소, 시간대 데이터, 헬스체크 및 장애 격리 운영 편의성을 압도적으로 제공합니다.

---

## 3. Dockerfile Multi-Stage 아키텍처

`Dockerfile`은 빌드 환경과 실행 환경을 완벽히 격리하는 Multi-Stage 빌드로 작성되었습니다.

```dockerfile
# syntax=docker/dockerfile:1
# -------------------------------------------------------------
# Stage 1: Build static executable (Native cross-compilation)
# -------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:alpine AS builder

WORKDIR /build
RUN apk add --no-cache git make

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -buildvcs=false \
    -ldflags="-s -w -X 'lionsql/pkg/config.Version=${VERSION}' -X 'lionsql/pkg/config.GitCommit=${COMMIT}' -X 'lionsql/pkg/config.BuildDate=${BUILD_DATE}'" \
    -o /build/bin/lionsql ./cmd/lionsql

# -------------------------------------------------------------
# Stage 2: Final runtime container
# -------------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && rm -rf /var/cache/apk/*

RUN addgroup -g 10001 -S lionsql \
    && adduser -u 10001 -S lionsql -G lionsql \
    && mkdir -p /data \
    && chown -R lionsql:lionsql /data

COPY --from=builder /build/bin/lionsql /usr/local/bin/lionsql

USER lionsql:lionsql
WORKDIR /data
VOLUME ["/data"]

EXPOSE 5433 1433 8090

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8090/api/health || exit 1

ENTRYPOINT ["lionsql"]
CMD ["-dir=/data", "-http=:8090", "-pg=:5433", "-tds=:1433"]
```

### 3.1 주요 보안 및 운영 규칙 준수
1. **비특권 사용자 실행 (`USER lionsql:lionsql`)**:
   - 컨테이너 탈옥(Container Escape) 방지를 위해 root 권한을 배제하고 UID/GID `10001`의 `lionsql` 시스템 계정으로 실행합니다.
2. **영속성 볼륨 지정 (`VOLUME ["/data"]`)**:
   - 컨테이너 재시작 및 재생성 시 WAL, AOF, 스냅샷 카탈로그의 보존을 보장합니다.
3. **네이티브 크로스 컴파일 (`$BUILDPLATFORM` 및 `TARGETARCH`)**:
   - Docker Buildx의 에뮬레이션 오버헤드 없이 호스트 Go 컴파일러가 직접 타깃 아키텍처 바이너리를 생성하여 빌드 시간을 1/10 수준으로 단축합니다.
4. **자체 헬스체크 (`HEALTHCHECK`)**:
   - `http://127.0.0.1:8090/api/health` 엔드포인트를 주기적으로 호출하여 컨테이너 오케스트레이터의 자동 복구를 지원합니다.

---

## 4. 환경변수 기반 동적 설정 지원 (`pkg/config/config.go`)

컨테이너 환경(12-Factor App) 표준에 맞추어 CLI 인자 외에 환경변수를 통한 엔진 구동 설정을 지원합니다.

| 환경변수 | 기본값 | 설명 |
| :--- | :--- | :--- |
| `LIONSQL_DATA_DIR` | `/data` | 데이터 저장소 루트 경로 |
| `LIONSQL_HTTP` | `:8090` | Web Studio 및 REST API 리스너 주소 |
| `LIONSQL_PG` | `:5433` | PostgreSQL Wire v3 리스너 주소 |
| `LIONSQL_TDS` | `:1433` | Microsoft SQL Server TDS 리스너 주소 |
| `LIONSQL_DIALECT` | `tsql` | 기본 세션 다이얼렉트 (`tsql` 또는 `postgres`) |
| `LIONSQL_ADMIN_USER` | `admin` | 초기 슈퍼유저 관리자 ID |
| `LIONSQL_ADMIN_PASS` | (무작위 생성) | 초기 슈퍼유저 관리자 패스워드 |

---

## 5. GitHub Actions GHCR 릴리즈 파이프라인 (`.github/workflows/release.yml`)

릴리즈 태그(`v*.*.*`) 생성 시 자동으로 다중 아키텍처 Docker 이미지를 빌드하여 `ghcr.io`에 배포합니다.

### 5.1 주요 파이프라인 구성
```yaml
build-and-push-docker:
  name: Build & Push Official Docker Image (Alpine Multi-Arch)
  runs-on: ubuntu-latest
  needs:
    - publish-release
  permissions:
    contents: read
    packages: write
  steps:
    - uses: actions/checkout@v4
      with:
        fetch-depth: 0
    - uses: docker/setup-qemu-action@v3
    - uses: docker/setup-buildx-action@v3
    - uses: docker/login-action@v3
      with:
        registry: ghcr.io
        username: ${{ github.actor }}
        password: ${{ secrets.GITHUB_TOKEN }}
    - uses: docker/metadata-action@v5
      id: meta
      with:
        images: ghcr.io/${{ github.repository }}
        tags: |
          type=semver,pattern={{version}}
          type=semver,pattern={{major}}.{{minor}}
          type=raw,value=latest,enable=${{ !contains(github.ref_name, 'beta') && !contains(github.ref_name, 'alpha') && !contains(github.ref_name, 'rc') }}
    - uses: docker/build-push-action@v5
      with:
        context: .
        file: ./Dockerfile
        platforms: linux/amd64,linux/arm64
        push: true
        tags: ${{ steps.meta.outputs.tags }}
        labels: ${{ steps.meta.outputs.labels }}
```

### 5.2 이미지 태깅 전략
- 정규 릴리즈(`v0.95.0`):
  - `ghcr.io/maroshim/lionsql:0.95.0`
  - `ghcr.io/maroshim/lionsql:0.95`
  - `ghcr.io/maroshim/lionsql:latest`
- 프리릴리즈(`v0.95.0-rc1`):
  - `ghcr.io/maroshim/lionsql:0.95.0-rc1` (latest 태그 부여 제외)

---

## 6. Docker 실행 예제

### 6.1 단일 컨테이너 실행
```bash
docker run -d \
  --name lionsql \
  -p 5433:5433 \
  -p 1433:1433 \
  -p 8090:8090 \
  -v lionsql_data:/data \
  -e LIONSQL_ADMIN_USER=admin \
  -e LIONSQL_ADMIN_PASS=StrongPassword123! \
  ghcr.io/maroshim/lionsql:latest
```

### 6.2 Docker Compose 구성
```yaml
version: '3.8'

services:
  lionsql:
    image: ghcr.io/maroshim/lionsql:latest
    container_name: lionsql-server
    restart: unless-stopped
    ports:
      - "5433:5433" # PostgreSQL Protocol
      - "1433:1433" # TDS Protocol (SSMS)
      - "8090:8090" # Web Studio & REST API
    volumes:
      - ./data:/data
    environment:
      - LIONSQL_ADMIN_USER=admin
      - LIONSQL_ADMIN_PASS=StrongPassword123!
      - LIONSQL_DIALECT=tsql
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8090/api/health"]
      interval: 10s
      timeout: 3s
      retries: 3
```
