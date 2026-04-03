# Remnawave Limiter

**Centralized device control for Remnawave**

Automatic monitoring of simultaneous user connections from the Remnawave panel. Tracks IP addresses from all nodes via API, compares against each user's device limit, and notifies administrators via Telegram bot with instant management capabilities.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

[🇷🇺 Русская версия](README.md)

## Features

**Tracking:**
- Unique IP addresses over a configurable period
- Comparison with individual device limits from the panel (`hwidDeviceLimit`)
- Violations with tolerance support
- IP aggregation from all nodes — complete connection picture

**Admin notifications:**
- Limit and number of detected IPs
- Violation count over the last 24 hours
- List of all IP addresses with node names
- Link to user profile
- Delivery to chat, channel, group, or specific thread
- Inline buttons for instant actions

**Two operation modes:**
- **Manual** — alerts with buttons: drop connections, disable subscription, add to whitelist
- **Automatic** — auto-disable subscription for a set time or permanently, with auto-restore by timer

**Flexible settings:**
- Tolerance above the limit
- Cooldown between alerts
- Check interval and IP activity window
- User data caching
- Timezone selection
- Interface language (Russian / English)

## Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌───────────────┐
│  Remnawave  │◄────│  remnawave-      │────►│   Telegram    │
│  Panel API  │     │  limiter         │     │   Bot API     │
└─────────────┘     │  (Go binary)     │     └───────────────┘
                    │  ┌────────────┐  │
                    │  │   Redis    │  │
                    │  └────────────┘  │
                    └──────────────────┘
```

- **Single instance** — install alongside the panel or on any server
- **No node installation required** — everything via the panel API
- **Docker Compose** — service + Redis in one file

### How it works

1. Fetches the list of active nodes via API (`GET /api/nodes`)
2. Requests user IPs from each node (in parallel)
3. Aggregates IPs per user across all nodes
4. Filters by last activity time (`lastSeen`)
5. Compares active IP count against the limit + tolerance
6. On violation — reacts depending on the mode (alert or auto-block)

## Requirements

- Docker and Docker Compose
- Remnawave panel with an API token
- Telegram bot (create via [@BotFather](https://t.me/BotFather))

## Installation

### 1. Clone the repository

```bash
git clone https://github.com/syvlech/remnawave-limiter.git
cd remnawave-limiter
```

### 2. Create configuration

```bash
cp .env.example .env
nano .env
```

Fill in the required parameters:

```bash
REMNAWAVE_API_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-api-token-here
TELEGRAM_BOT_TOKEN=123456:ABC-DEF
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_ADMIN_IDS=123456789
LANGUAGE=en
```

### 3. Start

```bash
docker compose up -d
```

### Verify

```bash
docker compose logs -f limiter
```

### Update

```bash
docker compose pull
docker compose up -d
```

## Configuration

All settings via `.env` file or environment variables.

### API connection

| Parameter | Required | Description |
|-----------|:---:|-----------|
| `REMNAWAVE_API_URL` | yes | Remnawave panel address |
| `REMNAWAVE_API_TOKEN` | yes | API token (generated in the panel) |

### Monitoring

| Parameter | Default | Description |
|-----------|:---:|-----------|
| `CHECK_INTERVAL` | `30` | Check interval (seconds) |
| `ACTIVE_IP_WINDOW` | `300` | IP is considered active if `lastSeen` < this value (seconds) |
| `TOLERANCE` | `0` | Allowed excess over the limit. If limit is 3 and tolerance is 1, reaction at 5+ IPs |
| `COOLDOWN` | `300` | Cooldown between alerts for one user (seconds) |
| `USER_CACHE_TTL` | `600` | User data cache TTL (seconds) |
| `DEFAULT_DEVICE_LIMIT` | `0` | Default limit if user has no `hwidDeviceLimit`. 0 = no limit |

### Reaction mode

| Parameter | Default | Description |
|-----------|:---:|-----------|
| `ACTION_MODE` | `manual` | `manual` — alert with buttons, `auto` — auto-disable subscription |
| `AUTO_DISABLE_DURATION` | `0` | Temporary disable duration in minutes. 0 = permanent only. In `manual` mode adds a "Disable for N min" button, in `auto` mode sets auto-restore time |

### Telegram

| Parameter | Required | Description |
|-----------|:---:|-----------|
| `TELEGRAM_BOT_TOKEN` | yes | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | yes | Chat/channel/group ID for alerts |
| `TELEGRAM_THREAD_ID` | no | Thread/topic ID in a supergroup |
| `TELEGRAM_ADMIN_IDS` | yes | Admin IDs separated by commas (only they can press buttons) |

### Other

| Parameter | Default | Description |
|-----------|:---:|-----------|
| `WHITELIST_USER_IDS` | — | UUIDs to exclude from checks (comma-separated) |
| `REDIS_URL` | `redis://redis:6379` | Redis address |
| `TIMEZONE` | `UTC` | Timezone for alert timestamps (e.g. `Europe/Moscow`) |
| `LANGUAGE` | `ru` | Interface language: `ru` or `en` |

## Limit logic

| `hwidDeviceLimit` | Behavior |
|:-:|-----------|
| `> 0` | Used as the device limit |
| `null` | Uses `DEFAULT_DEVICE_LIMIT` from config |
| `0` | No limit — user is skipped |

## Telegram bot

### Manual mode (`ACTION_MODE=manual`)

When the limit is exceeded, the bot sends an alert with buttons:

```
⚠️ Device limit exceeded

👤 User: username123
📊 Limit: 3 | Detected: 5 IP
📈 Violations in 24h: 3
🕐 2025-11-29 04:15:30

📍 IP addresses:
  • 178.66.157.246 (node: yandex-1)
  • 90.188.59.122 (node: yandex-1)
  • 46.250.75.216 (node: germany-2)

🔗 Profile

[🔄 Drop connections] [🔒 Disable permanently]
[🔒 Disable for 10 min]        ← if AUTO_DISABLE_DURATION > 0
[🔇 Ignore]
```

| Button | Action |
|--------|--------|
| Drop connections | Reset active user connections via API |
| Disable permanently | Permanently deactivate subscription via API |
| Disable for N min | Temporarily deactivate with auto-restore by timer (shown when `AUTO_DISABLE_DURATION > 0`) |
| Ignore | Add to whitelist (no more alerts) |

### Automatic mode (`ACTION_MODE=auto`)

The subscription is disabled automatically, the bot sends an informational alert with an "Enable subscription" button.

If `AUTO_DISABLE_DURATION > 0` — the subscription is automatically restored by timer.

## Remnawave API

The project uses the following endpoints:

| Endpoint | Purpose |
|----------|---------|
| `GET /api/nodes` | List of active nodes |
| `POST /api/ip-control/fetch-users-ips/{nodeUuid}` | User IPs on a node |
| `GET /api/ip-control/fetch-users-ips/result/{jobId}` | IP request result |
| `GET /api/users/by-id/{id}` | User data and limit |
| `POST /api/ip-control/drop-connections` | Drop connections |
| `POST /api/users/{uuid}/actions/disable` | Disable subscription |
| `POST /api/users/{uuid}/actions/enable` | Enable subscription |

## Project structure

```
cmd/limiter/main.go              → Entry point
internal/
├── config/config.go             → Configuration (.env)
├── api/client.go                → Remnawave API HTTP client
├── api/types.go                 → API types
├── monitor/monitor.go           → Main monitoring loop
├── cache/cache.go               → Redis: cache, cooldowns, whitelist
├── telegram/bot.go              → Telegram bot
├── telegram/messages.go         → Message formatting
├── i18n/i18n.go                 → Internationalization (ru/en)
└── version/version.go           → Version
```

## FAQ

### How to find my Telegram Chat ID?

Add [@userinfobot](https://t.me/userinfobot) and send `/start`. For a group/channel — add the bot to the group and use the API or [@getidsbot](https://t.me/getidsbot).

### What happens if the panel API is unavailable?

The service logs an error, skips the current check cycle, and retries after `CHECK_INTERVAL` seconds. API requests are automatically retried up to 3 times with exponential backoff.

### Can I use Redis from Remnawave?

You can, but it's not recommended. The project runs its own Redis in Docker Compose. If you want to use an existing one — specify its address in `REDIS_URL`.

### How to add a user to the whitelist via the bot?

Press the "Ignore" button in an alert. The user will be added to the whitelist in Redis and will no longer be checked. The whitelist persists across restarts.

### How to change the limit for a specific user?

The limit is taken from the `hwidDeviceLimit` field in the Remnawave panel. Change it in the user's subscription settings.

## Support

- **Issues**: [GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues)

## License

MIT License — see [LICENSE](LICENSE)
