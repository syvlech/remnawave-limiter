# Remnawave Limiter

**Centralized device control for Remnawave**

Monitors simultaneous user connections via the panel API: collects IPs from all nodes, compares them against each user's device limit (`hwidDeviceLimit`), and reacts to violations — via a Telegram bot or automatic subscription blocking.

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

[Русская версия](README.md)

## Features

- **IP aggregation across all nodes** — full connection picture, compared against the individual limit + tolerance
- **Two modes:** `manual` — alert with inline buttons (drop, ban, whitelist); `auto` — auto-block with timer-based auto-restore
- **Violation threshold** — action only after N excesses within a window (protects against false positives)
- **Grouping** of IPs by subnet (`/24`) or by ASN providers — against CGNAT and sharing
- **Webhook** (JSON POST) on violations with HMAC signature
- **Statistics & reports** — `/stats` command (24h/week + top-5 violators) and a daily report to the chat
- **Runtime settings** via `/settings` — change parameters on the fly without restart (stored in Redis)
- **Whitelist** of users and IP/CIDR, cooldown, cache, timezone selection, ru/en
- **Liveness `/healthz`** (optional, via `HEALTH_ADDR`) for Docker/orchestrator healthchecks
- **Logs built for `docker compose logs -f`** — a summary line per check cycle, log level changeable at runtime, `LOG_FORMAT=json` for Loki/ELK

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

A single instance alongside the panel or on any server. No node installation needed — everything goes through the API. Docker Compose runs the service + Redis.

**Check cycle:** node list (`GET /api/nodes`) → parallel IP collection per node → aggregation per user → filter by `lastSeen` → compare against `limit + tolerance` → increment threshold counter → on threshold, react (alert or auto-block).

A failing node does not abort the cycle: the check proceeds on the remaining ones and logs a warning about incomplete data. If no node responded at all, the whole cycle is skipped — otherwise the absence of connections would read as "everyone is within their limit".

## Requirements

- Docker and Docker Compose
- **Remnawave 3.0.0+** with an API token (scopes `connections:*` and `users:*`)
- Telegram bot ([@BotFather](https://t.me/BotFather))

### Which limiter version to use

| Remnawave panel version | limiter version |
|---|---|
| **3.0.0 and newer** | **4.0.0+** — current, `latest` tag |
| below 3.0.0 (the 2.x line) | [3.0.0](https://github.com/syvlech/remnawave-limiter/releases/tag/3.0.0) — last release supporting 2.x panels |

The versions are not interchangeable: Remnawave 3.0.0 renamed the `ip-control` module to `connections` and identifies users by a numeric `id` instead of `uuid`, removing the old endpoints. So limiter 4.0.0+ does not work with a 2.x panel, and limiter 3.0.0 does not work with a 3.x one.

If your panel is not upgraded yet, pin the image to the 3.0.0 release in `docker-compose.yml`:

```yaml
image: ghcr.io/syvlech/remnawave-limiter:b422f43
```

`b422f43` is the 3.0.0 release commit. There is no dedicated `3.0.0` image tag in ghcr: CI only builds semver tags for `v*` refs, while the release is tagged `3.0.0` without the prefix.

## Upgrading to 4.2.0

No data migration is needed — `docker compose pull && docker compose up -d` is enough. But configuration validation is now strict, and the service **refuses to start** when `.env` contains:

- **a typo in a numeric or boolean parameter** (`CHECK_INTERVAL=6O`, `AUTO_NOTIFY_SOFT=yes`). Previously such a value was silently replaced by the default, so the limiter ran with thresholds the administrator never set. Startup now prints every invalid parameter at once;
- **a negative value** for `TOLERANCE`, `TOLERANCE_MULTIPLIER`, `USER_CACHE_TTL`, `DEFAULT_DEVICE_LIMIT`, `AUTO_DISABLE_DURATION`, `IGNORE_DURATION`;
- **`REMNAWAVE_API_URL` without a scheme** (`panel.example.com` instead of `https://panel.example.com`) — the address used to be accepted, and every panel request failed at runtime instead;
- **a non-numeric `WHITELIST_USER_IDS`** — a UUID left over from pre-4.0 versions would never match a user ID and silently did nothing;
- **an unknown timezone, language, `LOG_LEVEL` or `LOG_FORMAT`**.

The reason is always in the first lines of `docker compose logs limiter`.

Notable behaviour changes:

- **Logging** was reworked for `docker compose logs -f`: one summary line per check cycle (`Проверка  nodes=2/2 took=308ms users=17 violations=1`), a single consistent format, and no duplicate lines from the Telegram library. Adds `LOG_LEVEL` (changeable at runtime via `/settings`) and `LOG_FORMAT=json` for log shippers.
- **Timer-based restore** no longer loses a user when the panel was unreachable at the moment the timer fired: the ID goes back into the queue, and once attempts are exhausted the admins get a chat notification.
- **`/healthz`** only counts a check as successful when at least one node was polled.
- **The "violations in 24h" counter** now actually expires after 24 hours (previously it never reset for a repeat offender and grew without bound).
- **Panel requests** survive `429` and honour `Retry-After` — a rate limit used to abort the whole check.
- **Webhooks** retry on transient failures and add an `X-Timestamp` header.

## Upgrading from limiter 3.x to 4.0.0

1. Upgrade the panel to Remnawave 3.0.0+.
2. Reissue (or edit) the API token in the panel: the `ip-control:*` scope was renamed to `connections:*`. Without this every request returns 403.
3. Update limiter and restart.

No Redis migration is required: the numeric user ID is written to the same keys as before (`user:<id>`, `cooldown:<id>`, `whitelist`, stats). Exceptions:

- **Restore queue.** `restore:queue` entries written by 3.x contain UUIDs and are skipped with a log warning — those users must be re-enabled manually. To clear the queue up front: `redis-cli DEL restore:queue`.
- **Buttons in old Telegram messages.** Alerts sent before the upgrade carry a UUID in their callback data; pressing them answers "Button is outdated".
- **`WHITELIST_USER_IDS`** must contain numeric user IDs (as before); UUIDs in that list will never match.
- **Webhook.** The `user.uuid` field is gone and `user.user_id` is now a number rather than a string — update your receiver.

## Installation

```bash
mkdir -p /opt/remnawave-limiter && cd /opt/remnawave-limiter

curl -O https://raw.githubusercontent.com/syvlech/remnawave-limiter/master/docker-compose.yml
curl -O https://raw.githubusercontent.com/syvlech/remnawave-limiter/master/.env.example

cp .env.example .env && nano .env
```

Required parameters in `.env`:

```bash
REMNAWAVE_API_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-api-token-here
TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11X
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_ADMIN_IDS=123456789
LANGUAGE=en
```

Start, logs, and update:

```bash
docker compose pull && docker compose up -d   # start / update
docker compose logs -f limiter                # verify
```

## Configuration

All settings via `.env` or environment variables.

| Parameter | Default | Description |
|-----------|:---:|-----------|
| `REMNAWAVE_API_URL` | **required** | Remnawave panel address |
| `REMNAWAVE_API_TOKEN` | **required** | API token (generated in the panel) |
| `REMNAWAVE_COOKIES` | — | Additional cookie auth. Format: `key=value` separated by `;` (e.g. `cf_clearance=abc; session=xyz`). For panels behind Cloudflare/WAF |
| `REMNAWAVE_HEADERS` | — | Additional HTTP headers sent with every API request. Format: `Name: Value` separated by `;` (e.g. `X-Api-Key: secret123; X-Custom-Header: value`). For panels behind a proxy/Cloudflare/protection |
| `TELEGRAM_BOT_TOKEN` | **required** | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | **required** | Chat/channel/group ID for alerts |
| `TELEGRAM_ADMIN_IDS` | **required** | Admin IDs (comma-separated); only they can press buttons |
| `TELEGRAM_THREAD_ID` | — | Thread/topic ID in a supergroup |
| `TELEGRAM_PROXY` | — | Proxy for the Telegram API. Schemes: `http`, `https`, `socks5`, `socks5h`. Format: `scheme://[user:pass@]host:port` |
| `CHECK_INTERVAL` | `30` | Check interval (sec) |
| `ACTIVE_IP_WINDOW` | `300` | IP is active if `lastSeen` < this value (sec) |
| `TOLERANCE` | `0` | Fixed allowed excess over the limit. Limit 3 + tolerance 1 → reaction at 5+ |
| `TOLERANCE_MULTIPLIER` | `0` | Proportional tolerance: `TOLERANCE + floor(limit × multiplier)`. 0 disables |
| `COOLDOWN` | `300` | Cooldown between alerts per user (sec) |
| `USER_CACHE_TTL` | `600` | User data cache TTL (sec) |
| `DEFAULT_DEVICE_LIMIT` | `0` | Limit when `hwidDeviceLimit` is unset. 0 = no limit |
| `ACTION_MODE` | `manual` | `manual` — alert with buttons; `auto` — auto-disable subscription |
| `AUTO_DISABLE_DURATION` | `0` | Temporary disable duration (min). 0 = permanent. In `manual` adds a button, in `auto` sets auto-restore time |
| `AUTO_NOTIFY_SOFT` | `false` | `auto` only. Excess **within tolerance** (`limit < devices <= limit+TOLERANCE`) triggers an informational alert with no ban. Ban only above `limit+TOLERANCE` |
| `WEBHOOK_URL` | — | URL for webhooks on violations (POST JSON). Empty = disabled |
| `WEBHOOK_SECRET` | — | Webhook secret. Sent in the `X-Webhook-Secret` header and used to HMAC-SHA256 sign the body in `X-Signature: sha256=<hex>` (optional) |
| `WHITELIST_USER_IDS` | — | UUIDs to exclude from checks (comma-separated) |
| `IGNORED_NODE_UUIDS` | — | Node UUIDs skipped during IP collection (not in reports or decisions). For technical/test nodes |
| `IP_WHITELIST` | — | IPs and/or CIDR subnets (comma-separated) excluded from counting. Drops node/bridge/relay IPs. IPv4/IPv6. Example: `203.0.113.5,10.0.0.0/8,2001:db8::/32` |
| `IGNORE_DURATION` | `0` | TTL of the "Ignore" button action (min). `0` = permanent. `> 0` = temporary whitelist with TTL |
| `VIOLATION_THRESHOLD` | `1` | Violations required before action. 1 = instant reaction |
| `VIOLATION_THRESHOLD_WINDOW` | `3600` | Violation counting window (sec). Counter resets if no new violations occur within it |
| `SUBNET_GROUPING` | `false` | Group IPv4 by `/SUBNET_PREFIX_V4` — counts subnets instead of IPs (reduces CGNAT false positives). IPv6 is per-IP |
| `SUBNET_PREFIX_V4` | `24` | IPv4 prefix length (8..32). 24 is standard; 16 suits mobile-heavy audiences. When `SUBNET_GROUPING=true` |
| `ASN_GROUPING` | `false` | Count unique ASN providers instead of IPs/subnets — strongest signal against sharing. IPs without ASN are a separate group. Takes priority over `SUBNET_GROUPING`. Requires the MaxMind ASN database |
| `ASN_DATABASE_PATH` | `./geoip/GeoLite2-ASN.mmdb` | Path to `GeoLite2-ASN.mmdb`. Directory is created automatically. Override only for non-standard layouts |
| `MAXMIND_LICENSE_KEY` | — | MaxMind key. If set, the missing database is downloaded on startup + refreshed in the background. [Register](https://www.maxmind.com/en/geolite2/signup) |
| `MAXMIND_UPDATE_INTERVAL` | `168h` | Auto-refresh interval (min `1h`). Only when `MAXMIND_LICENSE_KEY` is set |
| `REDIS_URL` | `redis://redis:6379` | Redis address |
| `TIMEZONE` | `UTC` | Timezone for alert timestamps (e.g. `Europe/Moscow`) |
| `LANGUAGE` | `ru` | Interface language: `ru` or `en` |
| `DAILY_REPORT` | `false` | Daily violation report to the chat (top violators + counts). Toggleable at runtime via `/settings` |
| `DAILY_REPORT_TIME` | `09:00` | Local time to send the report (`HH:MM`, in `TIMEZONE`). Restart required to change |
| `HEALTH_ADDR` | — | Address of the HTTP liveness endpoint `/healthz` (e.g. `:8080`). Empty = disabled |
| `LOG_LEVEL` | `info` | Log verbosity: `trace`, `debug`, `info`, `warn`, `error`. At `info` — one summary line per check cycle plus every action taken; `debug` adds the per-IP breakdown and Telegram transport details. Changeable at runtime via `/settings` |
| `LOG_FORMAT` | `text` | `text` for humans, `json` for log shippers (Loki, ELK). Restart required to change |

**ASN in alerts.** When the MaxMind database is available, each IP in alerts and webhooks is annotated with the provider (`• 91.107.96.11 - Hetzner Online GmbH (Chicago-1)`), and the header shows the unique ASN count (`Detected: 4 IP (3 ASN)`). It never affects the limiting logic — decisions use IP/subnet/ASN counts only.

## Limit logic

| `hwidDeviceLimit` | Behavior |
|:-:|-----------|
| `> 0` | Used as the device limit |
| `null` | Uses `DEFAULT_DEVICE_LIMIT` from config |
| `0` | No limit — user is skipped |

## Violation threshold

With `VIOLATION_THRESHOLD=1` (default) the limiter reacts to every excess. With a higher value, action runs only after N violations accumulate within `VIOLATION_THRESHOLD_WINDOW`: excess → cooldown check → counter increment (TTL = window) → on threshold, action + counter reset. If more than the window passes between violations, the counter resets.

Example with `VIOLATION_THRESHOLD=3`, `VIOLATION_THRESHOLD_WINDOW=3600`:

| Time | Counter | Action |
|:---:|:---:|--------|
| 12:00 | 1/3 | Logged, no action |
| 12:05 | 2/3 | Logged, no action |
| 12:10 | 3/3 | Alert/block, reset |
| 12:15 | 1/3 | Logged, no action |

## Telegram bot

### Commands

Available to admins from `TELEGRAM_ADMIN_IDS` only. Registered in the bot's command menu automatically on startup.

| Command | What it does |
|---------|--------------|
| `/settings` | Interactive runtime-settings menu. Changes safe parameters on the fly (no restart); values are stored in Redis and survive restarts. See the "Runtime settings" section below |
| `/stats` | Violation statistics: counts for the last 24 hours and last week + top-5 violators of the week by violation count |

### Runtime settings (`/settings`)

Source priority: **Redis override > `.env` / environment > defaults**. Changes apply on the fly (e.g. `CHECK_INTERVAL` resets the ticker). Structural and secret keys (API URL/token, all `TELEGRAM_*`, `REDIS_URL`, `TIMEZONE`, `LANGUAGE`, `WEBHOOK_*`, `IP_WHITELIST`, `IGNORED_NODE_UUIDS`, `MAXMIND_*`, `DAILY_REPORT_TIME`, `HEALTH_ADDR`, `LOG_FORMAT`) require an `.env` change + restart. "Reset to .env" (per key or all) removes the override.

### Daily report (`DAILY_REPORT=true`)

Once a day at `DAILY_REPORT_TIME` (local time in `TIMEZONE`) the bot posts the same report as `/stats` (24h/week counts + top-5) to the chat. Only real post-threshold violations (`VIOLATION_THRESHOLD`) are counted; soft warnings are excluded from statistics. `DAILY_REPORT` is toggleable at runtime via `/settings`; `DAILY_REPORT_TIME` requires a restart.

### Manual mode (`ACTION_MODE=manual`)

On excess the bot sends an alert with buttons:

```
⚠️ Device limit exceeded

👤 User: username123
📊 Limit: 3 | Detected: 5 IP
📈 Violations in 24h: 3
🕐 2025-11-29 04:15:30

📍 IP addresses:
  • 10.0.1.10 (node: node-1)
  • 10.0.3.30 (node: node-2)

🔗 Profile

[🔄 Drop connections] [🔒 Disable permanently]
[🔒 Disable for 10 min]        ← if AUTO_DISABLE_DURATION > 0
[🔇 Ignore for 15 min]         ← if IGNORE_DURATION > 0, otherwise "🔇 Ignore"
```

| Button | Action |
|--------|--------|
| Drop connections | Reset active user connections via API |
| Disable permanently | Permanently deactivate the subscription |
| Disable for N min | Temporary deactivation with auto-restore (when `AUTO_DISABLE_DURATION > 0`) |
| Ignore | Add to whitelist: temporary if `IGNORE_DURATION > 0`, otherwise permanent |

### Automatic mode (`ACTION_MODE=auto`)

The subscription is disabled automatically; the bot sends an informational alert with an "Enable subscription" button. With `AUTO_DISABLE_DURATION > 0`, the subscription is restored by timer.

**Within-tolerance alerts (`AUTO_NOTIFY_SOFT=true`).** When you want to ban only on a noticeable excess (`TOLERANCE`) but still know about users who already crossed the HWID limit:

| Device count | Action |
|--------------|--------|
| `devices <= limit` | silence |
| `limit < devices <= limit+TOLERANCE` | 🔔 informational alert, **no ban** |
| `devices > limit+TOLERANCE` | 🔒 ban + alert |

Soft warnings use a separate cooldown (`cooldown:soft:`), do **not** increment the 24h violation counter, are **not** counted toward `VIOLATION_THRESHOLD`, and send a webhook with the `soft_violation_detected` event.

## Webhook

On a violation the limiter can send an HTTP POST to `WEBHOOK_URL` (works in both modes). Every request carries `X-Timestamp` (unix send time) so the receiver can reject replayed requests. When `WEBHOOK_SECRET` is set, two more headers are added: `X-Webhook-Secret` (the raw secret, for a simple match) and `X-Signature: sha256=<hex>` — an HMAC-SHA256 of the request body with that secret (for integrity / tamper protection).

Delivery does not block the monitoring loop. On a network error, 5xx or 429 it is retried (up to 3 attempts); a 4xx is treated as a deliberate rejection by the receiver and is not retried. The event survives shutdown: on SIGTERM the limiter waits for delivery instead of dropping it.

**Example payload:**

```json
{
  "event": "violation_detected",
  "action_mode": "auto",
  "user": {
    "user_id": 123,
    "username": "john",
    "email": "john@example.com",
    "telegram_id": 123456789,
    "subscription_url": "https://panel.example.com/sub/abc"
  },
  "violation": {
    "ips": [
      { "ip": "1.2.3.4", "node_name": "DE-1", "node_uuid": "node-uuid-1", "last_seen": "2025-11-29T12:00:00Z", "asn": 24940, "asn_org": "Hetzner Online GmbH" },
      { "ip": "5.6.7.8", "node_name": "US-1", "node_uuid": "node-uuid-2", "last_seen": "2025-11-29T12:01:00Z" }
    ],
    "ip_count": 5,
    "device_limit": 3,
    "tolerance": 1,
    "effective_limit": 4,
    "violation_count_24h": 3,
    "device_group_count": 5,
    "grouping_mode": "ip"
  },
  "action": { "auto_disable_duration_min": 10 },
  "timestamp": "2025-11-29T12:05:00Z"
}
```

| Field | Description |
|-------|-------------|
| `event` | `violation_detected` on a ban; `soft_violation_detected` for a within-tolerance soft warning (`AUTO_NOTIFY_SOFT`) |
| `action_mode` | `manual` or `auto` |
| `user` | User data (user_id, username, email, telegram_id, subscription_url) |
| `violation.ips` | Active IPs with node name and last activity time |
| `violation.ip_count` | Number of unique IPs |
| `violation.device_limit` / `tolerance` / `effective_limit` | Limit, tolerance, and effective limit (`device_limit + tolerance`) |
| `violation.violation_count_24h` | Violations in the last 24 hours |
| `violation.grouping_mode` | What devices were counted by: `ip`, `subnet` or `asn` |
| `violation.device_group_count` | Final "device" count in the selected mode — this is what gets compared to the limit |
| `violation.subnet_count` / `asn_group_count` | Number of subnets / ASN groups. Present only when the mode is enabled |
| `violation.ips[].asn` / `asn_org` | Provider number and name. Present only when the MaxMind database is loaded and the ASN resolved |
| `action.auto_disable_duration_min` | Disable duration in minutes (0 = permanent) |
| `timestamp` | Detection time (ISO 8601) |

## FAQ

**How to find my Telegram Chat ID?** Add [@userinfobot](https://t.me/userinfobot) and send `/start`. For a group/channel — [@getidsbot](https://t.me/getidsbot).

**What if the panel API is unavailable?** The service logs an error, skips the cycle, and retries after `CHECK_INTERVAL`. API requests retry up to 3 times with jittered exponential backoff; 429 and 408 are retried too, and the panel's `Retry-After` header is honoured. A cycle in which no node could be polled does not count as successful — `/healthz` will surface the problem.

**Can I use Redis from Remnawave?** You can, but it's not recommended — the project runs its own Redis. For an existing one, set `REDIS_URL`.

**How to change a user's limit?** Via the `hwidDeviceLimit` field in the user's subscription settings in the Remnawave panel.

## Support & License

[GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues) · GNU GPL v3.0 — see [LICENSE](LICENSE)
