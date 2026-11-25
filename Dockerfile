# syntax=docker/dockerfile:1
# ==============================================================================
# LionSQL Official Multi-Stage Dockerfile (Alpine Runtime)
#
# Ports exposed:
#   - 5433: PostgreSQL v3 Wire Protocol
#   - 1433: Microsoft SQL Server TDS 7.x/8.0 Wire Protocol
#   - 8090: Embedded Web Management Studio & REST/JSON API
#
# Volume:
#   - /data: Database storage directory (WAL, catalog, data files)
# ==============================================================================

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

LABEL org.opencontainers.image.title="LionSQL" \
      org.opencontainers.image.description="Autonomous, high-concurrency embedded OLTP database engine" \
      org.opencontainers.image.url="https://github.com/MaroShim/LionSQL" \
      org.opencontainers.image.source="https://github.com/MaroShim/LionSQL" \
      org.opencontainers.image.licenses="Apache-2.0"

# Install CA certificates for TLS Database Mail and tzdata for T-SQL temporal functions
RUN apk add --no-cache ca-certificates tzdata \
    && rm -rf /var/cache/apk/*

# Create dedicated unprivileged user and persistent data volume directory (UID/GID 10001)
RUN addgroup -g 10001 -S lionsql \
    && adduser -u 10001 -S lionsql -G lionsql \
    && mkdir -p /data \
    && chown -R lionsql:lionsql /data

# Copy compiled standalone executable
COPY --from=builder /build/bin/lionsql /usr/local/bin/lionsql

# Run container as non-root user
USER lionsql:lionsql
WORKDIR /data
VOLUME ["/data"]

# Wire protocols and Web Studio ports
EXPOSE 5433 1433 8090

# Container healthcheck via Web Studio healthz endpoint
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8090/api/health || exit 1

ENTRYPOINT ["lionsql"]
CMD ["-dir=/data", "-http=:8090", "-pg=:5433", "-tds=:1433"]
