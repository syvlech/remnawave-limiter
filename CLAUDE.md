# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
make build          # Compile both binaries (limiter + limiter-cli) to bin/
make test           # Run all tests: go test -v ./...
make lint           # Run go vet and go fmt
make install        # Build and copy binaries to /usr/local/bin
make deploy         # Build, install, and clean source artifacts + mod cache
make clean          # Remove bin/ directory
```

Run a single test: `go test -v -run TestName ./internal/parser/`

Build outputs go to `bin/remnawave-limiter` and `bin/limiter-cli`. Binaries are compiled with `-ldflags="-s -w"` for size optimization. Requires Go 1.26+.

## Version Tracking

Version constant lives in `internal/version/version.go`. Bump it on every code change. It is logged at daemon startup and shown in `limiter-cli version` and `limiter-cli help`.

## Project Overview

Go application that monitors Remnawave VPN node access logs and blocks IPs via fail2ban when a single subscription key is used from too many simultaneous IPs. Deployed per-node. Written in Russian (README, comments, UI strings).

Two binaries:
- **remnawave-limiter** (`cmd/limiter/`) — daemon service that polls access logs and writes violations
- **limiter-cli** (`cmd/limiter-cli/`) — management CLI with commands: status, violations, banned, unban, unban-all, active, logs, clear, version

## Architecture

```
cmd/limiter/main.go            → Entry point: loads config, sets up loggers, starts Limiter.Run()
cmd/limiter-cli/main.go        → CLI entry point: parses subcommands, calls fail2ban-client

internal/config/config.go      → Loads .env file, provides Config struct with defaults
internal/parser/parser.go      → Regex-based log parser: extracts IP, email, timestamp from access logs
internal/limiter/limiter.go    → Core loop: polls log → groups IPs by subscription → detects violations
internal/limiter/ban_watcher.go → Goroutine monitoring fail2ban banned.log for BAN/UNBAN events
internal/limiter/webhook.go    → Async HTTP POST notifications with template variable substitution
internal/version/version.go    → Single const Version string

pkg/logger/logger.go           → Dual logger setup: main (stdout+file) and violation-specific (file only)
```

**Data flow:** Access log → parser extracts (IP, email, timestamp) → limiter groups by email, counts active IPs (seen < 60s ago) → excess IPs written to violation log → fail2ban reads violation log → bans IP → ban_watcher detects ban → webhook sends notification.

## Key Design Details

- **Violation deduplication:** In-memory cache with RWMutex prevents logging the same IP+email violation more than once per 60 seconds. Stale entries (>5 min) evicted on log clear.
- **Active IP detection:** An IP is considered "active" only if its last log entry was < 60 seconds ago (handles network switching LTE↔Wi-Fi). IPs are sorted by first-seen time; the earliest IPs are kept, newest are banned.
- **Access log clearing:** Only on timer interval (LOG_CLEAR_INTERVAL), never on violation detection — preserves evidence for accurate detection.
- **Fail2ban threshold:** 3 violations within 5 minutes (findtime=300) triggers a ban (configured in fail2ban jail, not in Go code).
- **Webhook:** Async with up to 2 retries, exponential backoff. Template variables: `%email`, `%ip`, `%server`, `%action`, `%duration`, `%timestamp`. Values are JSON-escaped. Ban events older than 60s are skipped for webhook.
- **Configuration:** All settings via `.env` file (loaded by godotenv), deployed to `/opt/remnawave-limiter/.env`.
- **Whitelist:** `WHITELIST_EMAILS` config value — comma-separated subscription IDs excluded from limit checks.
- **Hardcoded paths in CLI:** The CLI has hardcoded log paths and service name (`remnawave-limiter`). The daemon reads paths from config. Keep these in sync if changing defaults.
- **Localhost filtering:** Parser skips `127.0.0.1` and `::1` entries.

## Dependencies

Minimal external dependencies (see go.mod):
- `github.com/joho/godotenv` — .env file loading
- `github.com/sirupsen/logrus` — structured logging

# AI Assistant Guidelines

- Проект используется для отслеживания и ограничения пользователей VPN сервиса, которые могут делиться подпиской с посторонними людей сверх того лимита, который установлен сервисом.
В связи с этим на каждую VPN ноду, которая есть в сервисе ставится этот проект, который отслеживает IP-адреса, подключаемые к определенной подписке, которая определяется через ID подписки в логах.