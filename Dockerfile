# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

# Пусто по умолчанию: тогда версия берётся из internal/version/version.go.
# Значение вроде "dev" здесь перетирало бы её при любой сборке без --build-arg.
ARG VERSION=""
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
        -ldflags="-s -w ${VERSION:+-X github.com/remnawave/limiter/internal/version.Version=$VERSION}" \
        -o /bin/remnawave-limiter ./cmd/limiter/

FROM alpine:3.24

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
