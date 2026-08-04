# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
        -ldflags="-s -w -X github.com/remnawave/limiter/internal/version.Version=${VERSION}" \
        -o /bin/remnawave-limiter ./cmd/limiter/

FROM alpine:3.23

# ca-certificates — HTTPS к панели и MaxMind, tzdata — TIMEZONE,
# wget — healthcheck в docker-compose.
RUN apk add --no-cache ca-certificates tzdata wget

COPY --from=builder /bin/remnawave-limiter /usr/local/bin/remnawave-limiter

RUN adduser -D -u 10001 app \
    && mkdir -p /app/geoip \
    && chown -R app:app /app

WORKDIR /app
USER app

ENTRYPOINT ["remnawave-limiter"]
