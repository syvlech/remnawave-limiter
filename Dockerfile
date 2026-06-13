FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/remnawave-limiter ./cmd/limiter/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /bin/remnawave-limiter /usr/local/bin/remnawave-limiter
RUN adduser -D -u 10001 app \
    && mkdir -p /app/geoip \
    && chown -R app:app /app
WORKDIR /app
USER app
ENTRYPOINT ["remnawave-limiter"]
