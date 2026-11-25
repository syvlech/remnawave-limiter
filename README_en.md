# Remnawave Limiter

**Centralized device control for Remnawave**

Monitors simultaneous user connections via the panel API: collects IPs from all nodes, compares them against each user's device limit (`hwidDeviceLimit`), and reacts to violations — via a Telegram bot or automatic subscription blocking.

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

[Русская версия](README.md) · [简体中文](README_zh.md)

## Features

- **IP aggregation across all nodes** — full connection picture, compared against the individual limit + tolerance
- **Two modes:** `manual` — alert with inline buttons (drop, ban, whitelist); `auto` — auto-block with timer-based auto-restore
- **Violation threshold** — action only after N excesses within a window (protects against false positives)
- **Grouping** of IPs by subnet (`/24`) or by ASN providers — against CGNAT and sharing
- **Webhook** (JSON POST) on violations with HMAC signature
- **Statistics & reports** — `/stats` command (24h/week + top-5 violators) and a daily report to the chat
- **Update check** — once a day compares the running version with the latest GitHub release and announces a new one in the chat
- **Traffic monitoring** — compares each user's daily traffic with their own usual volume: spikes and persistently high traffic, with no per-user requests
- **Shared HWID** — an alert when a new user's device has already been seen on another account, which is how repeated trials from the same phone are found
- **Runtime settings** via `/settings` — change parameters on the fly without restart (stored in Redis)
- **Whitelist** of users and IP/CIDR, cooldown, cache, timezone selection, ru/en/zh
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
- **Remnawave 3.0.0+** with an API token (scopes `connections:*`, `users:*` and `nodes:read`; for the HWID block in alerts and the shared HWID search also `hwid-user-devices:read`, for traffic monitoring `bandwidth-stats:node-usage`)
- Telegram bot ([@BotFather](https://t.me/BotFather))

<details>
<summary>Minimal scope set</summary>

If you want the token to hold only what limiter actually calls:

| Scope | Endpoint | Why |
|---|---|---|
| `nodes:list` | `GET /api/nodes` | list of nodes to poll — without it the cycle never starts |
| `connections:by-node` | `POST /api/connections/by-node/{nodeUuid}` | starts IP collection on a node |
| `connections:by-node-result` | `GET /api/connections/by-node/{jobId}` | collection result |
| `users:by-id` | `GET /api/users/{id}` | device limit, user data and internal squads |
| `users:disable` | `POST /api/users/{id}/actions/disable` | disabling the subscription |
| `users:enable` | `POST /api/users/{id}/actions/enable` | the "Enable" button and timed restore |
| `connections:drop` | `POST /api/connections/drop` | only the "Drop connections" button: `manual` mode alerts, soft warnings (`AUTO_NOTIFY_SOFT`) and traffic alerts |
| `users:subscription-request-history` | `GET /api/users/{id}/subscription-request-history` | the "Subscription requests" block in alerts; optional |
| `hwid-user-devices:list-by-user` | `GET /api/hwid/devices/{userId}` | the "HWID devices" block in alerts; optional |
| `bandwidth-stats:node-usage` | `POST /api/bandwidth-stats/nodes/usage` | traffic monitoring (`TRAFFIC_MONITOR`); optional |
| `hwid-user-devices:list` | `GET /api/hwid/devices` | shared HWID search (`SHARED_HWID_MONITOR`); optional |
| part of `users:*` | `PATCH /api/users` | traffic limit on a shared HWID (`SHARED_HWID_TRAFFIC_LIMIT_MB`) and the “Restore traffic” button; optional |

Read access is enough for nodes: `nodes:*` would also grant write access (restarting and disabling nodes, editing profiles and inbounds), which limiter does not need.

</details>

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
| `REMNAWAVE_HEADERS` | — | Additional HTTP headers sent with every API request. Format: `Name: Value` separated by `;` (e.g. `X-Api-Key: secret123; X-Custom-Header: value`). A `;` inside parentheses does not split headers (a User-Agent can be written as is); elsewhere escape it as `\;`. For panels behind a proxy/Cloudflare/protection |
| `TELEGRAM_BOT_TOKEN` | **required** | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | **required** | Chat/channel/group ID for alerts |
| `TELEGRAM_ADMIN_IDS` | **required** | Admin IDs (comma-separated); only they can press buttons |
| `TELEGRAM_THREAD_ID` | — | Thread/topic ID in a supergroup |
| `TELEGRAM_PROXY` | — | Proxy for the Telegram API. Schemes: `http`, `https`, `socks5`, `socks5h`. Format: `scheme://[user:pass@]host:port` |
| `CHECK_INTERVAL` | `60` | Check interval (sec) |
| `ACTIVE_IP_WINDOW` | `120` | IP is active if `lastSeen` < this value (sec) |
| `TOLERANCE` | `0` | Fixed allowed excess over the limit. Limit 3 + tolerance 1 → reaction at 5+ |
| `TOLERANCE_MULTIPLIER` | `0` | Proportional tolerance: `TOLERANCE + floor(limit × multiplier)`. 0 disables. Allowed `0..100` |
| `COOLDOWN` | `300` | Cooldown between alerts per user (sec) |
| `USER_CACHE_TTL` | `600` | User data cache TTL (sec) |
| `DEFAULT_DEVICE_LIMIT` | `0` | Limit when `hwidDeviceLimit` is unset. 0 = no limit |
| `ACTION_MODE` | `manual` | `manual` — alert with buttons; `auto` — auto-disable subscription |
| `AUTO_DISABLE_DURATION` | `15` | Temporary disable duration (min). 0 = permanent. In `manual` adds a button, in `auto` sets auto-restore time |
| `AUTO_NOTIFY_SOFT` | `false` | `auto` only. Excess **within tolerance** (`limit < devices <= limit+TOLERANCE`) triggers an informational alert with no ban. Ban only above `limit+TOLERANCE`. The tolerance here is the full one: `TOLERANCE + floor(limit × TOLERANCE_MULTIPLIER)` |
| `WEBHOOK_URL` | — | URL for webhooks on violations (POST JSON). Empty = disabled |
| `WEBHOOK_SECRET` | — | Webhook secret for the HMAC-SHA256 signatures `X-Signature` (body) and `X-Signature-V2` (`<X-Timestamp>.<body>`). See [Webhook](#webhook) (optional) |
| `WEBHOOK_SECRET_HEADER` | `true` | Whether to send the raw secret in the `X-Webhook-Secret` header. Kept for compatibility; turn it off if the receiver verifies the signature |
| `WHITELIST_USER_IDS` | — | Numeric user IDs to exclude from checks (comma-separated). UUIDs used before version 4.0 do not work here |
| `IGNORED_NODE_UUIDS` | — | Node UUIDs skipped during IP collection (not in reports or decisions). For technical/test nodes |
| `IP_WHITELIST` | — | IPs and/or CIDR subnets (comma-separated) excluded from counting. Drops node/bridge/relay IPs. IPv4/IPv6. Example: `203.0.113.5,10.0.0.0/8,2001:db8::/32` |
| `IGNORE_DURATION` | `0` | TTL of the "Ignore" button action (min). `0` = permanent. `> 0` = temporary whitelist with TTL |
| `VIOLATION_THRESHOLD` | `3` | Violations required before action. 1 = instant reaction |
| `VIOLATION_THRESHOLD_WINDOW` | `3600` | Violation counting window (sec). Must exceed `(VIOLATION_THRESHOLD − 1) × (COOLDOWN + CHECK_INTERVAL)`, otherwise the threshold is unreachable and the config is rejected |
| `SUBNET_GROUPING` | `false` | Group IPv4 by `/SUBNET_PREFIX_V4` — counts subnets instead of IPs (reduces CGNAT false positives). IPv6 is per-IP |
| `SUBNET_PREFIX_V4` | `24` | IPv4 prefix length (8..32). 24 is standard; 16 suits mobile-heavy audiences. When `SUBNET_GROUPING=true` |
| `ASN_GROUPING` | `false` | Count unique ASN providers instead of IPs/subnets — strongest signal against sharing. Each IP without an ASN counts as its own group. Takes priority over `SUBNET_GROUPING`. Requires the MaxMind ASN database |
| `ASN_DATABASE_PATH` | `./geoip/GeoLite2-ASN.mmdb` | Path to `GeoLite2-ASN.mmdb`. Directory is created automatically. Override only for non-standard layouts |
| `MAXMIND_LICENSE_KEY` | — | MaxMind key. If set, the missing database is downloaded on startup + refreshed in the background. [Register](https://www.maxmind.com/en/geolite2/signup) |
| `MAXMIND_UPDATE_INTERVAL` | `168h` | Auto-refresh interval (min `1h`). Only when `MAXMIND_LICENSE_KEY` is set |
| `REDIS_URL` | `redis://redis:6379` | Redis address |
| `TIMEZONE` | `UTC` | Timezone for alert timestamps (e.g. `Europe/Moscow`) |
| `LANGUAGE` | `ru` | Interface language: `ru`, `en` or `zh` |
| `DAILY_REPORT` | `true` | Daily violation report to the chat (top violators + counts). Toggleable at runtime via `/settings` |
| `DAILY_REPORT_TIME` | `21:00` | Local time to send the report (`HH:MM`, in `TIMEZONE`). Restart required to change |
| `UPDATE_CHECK` | `true` | Once a day, check GitHub for a new version and announce it in the chat, once per version. See [Update check](#update-check-update_checktrue). Toggleable at runtime via `/settings` |
| `TRAFFIC_MONITOR` | `true` | Traffic monitoring: spikes against the user's usual volume and persistently high traffic. See [Traffic monitoring](#traffic-monitoring-traffic_monitortrue). Toggleable at runtime via `/settings` |
| `TRAFFIC_SPIKE_FACTOR` | `10` | How many times a day's traffic must exceed the usual volume (median of the days with traffic over 14 days) to count as a spike. `1.5..100`. Editable in `/settings` |
| `TRAFFIC_SPIKE_MIN_GB` | `150` | Minimum daily traffic for a spike, GB. Editable in `/settings` |
| `TRAFFIC_HEAVY_GB` | `200` | Daily traffic from which a day counts as heavy, GB. `0` turns the rule off. Editable in `/settings` |
| `TRAFFIC_HEAVY_DAYS` | `5` | How many heavy days out of the last 7 trigger the persistently high traffic alert (`1..7`). Editable in `/settings` |
| `TRAFFIC_IGNORED_NODE_UUIDS` | — | Comma-separated node UUIDs whose traffic is not counted (on top of `IGNORED_NODE_UUIDS`), for example nodes where heavy traffic is normal |
| `SHARED_HWID_MONITOR` | `false` | Alert when a new device's HWID has already been seen on another account. See [Shared HWID](#shared-hwid-shared_hwid_monitortrue). Can be toggled at runtime via `/settings` |
| `SHARED_HWID_SQUAD_UUIDS` | — | Comma-separated internal squad UUIDs: only new devices of users in these squads trigger an alert (for example, the trial squad). Matches are searched among all users. Empty means every user is checked. Restart required |
| `SHARED_HWID_HISTORY_DAYS` | `30` | How many days the limiter remembers the HWID devices it has seen (`0..365`), to find accounts already deleted from the panel. `0` means no history of its own: only accounts still in the panel are compared. See [Shared HWID](#shared-hwid-shared_hwid_monitortrue). Can be changed in `/settings` |
| `SHARED_HWID_TRAFFIC_LIMIT_MB` | `0` | Traffic limit in MB that the limiter sets itself on the new account of a shared-HWID alert, e.g. `100`. Works only when `SHARED_HWID_SQUAD_UUIDS` is set. `0` = off. See [Traffic limit](#traffic-limit-shared_hwid_traffic_limit_mb). Can be changed in `/settings` |
| `HEALTH_ADDR` | — | Address of the HTTP liveness endpoint `/healthz` (e.g. `:8080`). Empty = disabled |
| `LOG_LEVEL` | `info` | Log verbosity: `trace`, `debug`, `info`, `warn`, `error`. At `info` — one summary line per check cycle plus every action taken; `debug` adds the per-IP breakdown and Telegram transport details. Changeable at runtime via `/settings` |
| `LOG_FORMAT` | `text` | `text` for humans, `json` for log shippers (Loki, ELK). Restart required to change |

**ASN in alerts.** When the MaxMind database is available, each IP in the alert's IP list shows its provider (the “Provider” column), webhooks carry the `asn` and `asn_org` fields, and the “📡 Detected” row shows the unique ASN count (`4 IP · 3 ASN`). It never affects the limiting logic — decisions use IP/subnet/ASN counts only.

## Limit logic

| `hwidDeviceLimit` | Behavior |
|:-:|-----------|
| `> 0` | Used as the device limit |
| `null` | Uses `DEFAULT_DEVICE_LIMIT` from config |
| `0` | No limit — user is skipped |

## Violation threshold

With `VIOLATION_THRESHOLD=1` the limiter reacts to every excess. With a higher value (the default is 3), action runs only after N violations accumulate within `VIOLATION_THRESHOLD_WINDOW`: excess → cooldown check → counter increment (TTL = window) → on threshold, action + counter reset. If more than the window passes between violations, the counter resets.

The cooldown is set before the increment, so the counter grows at most once per `COOLDOWN`, and the next violation is noticed on the next check. The window must fit `N − 1` such steps: `VIOLATION_THRESHOLD_WINDOW > (VIOLATION_THRESHOLD − 1) × (COOLDOWN + CHECK_INTERVAL)`. A config that breaks this is rejected at startup and in `/settings` — otherwise the threshold would never be reached and the limiter would silently stop acting.

Example with `VIOLATION_THRESHOLD=3`, `VIOLATION_THRESHOLD_WINDOW=3600`:

| Time | Counter | Action |
|:---:|:---:|--------|
| 12:00 | 1/3 | Logged, no action |
| 12:05 | 2/3 | Logged, no action |
| 12:10 | 3/3 | Alert/block, reset |
| 12:15 | 1/3 | Logged, no action |

## Telegram bot

Every bot message is a Telegram **rich message** (Bot API 10.3, `sendRichMessage`): headings, tables, collapsible blocks, times shown in the reader's time zone (`tg-time`) and colored buttons inside the message itself. If Telegram rejects the rich markup, the message is still delivered as plain text with inline buttons. Rich messages need a Telegram client that supports them.

### Commands

Available to admins from `TELEGRAM_ADMIN_IDS` only. Registered in the bot's command menu automatically on startup.

| Command | What it does |
|---------|--------------|
| `/settings` | Interactive runtime-settings menu. Changes safe parameters on the fly (no restart); values are stored in Redis and survive restarts. See the "Runtime settings" section below |
| `/stats` | Violation statistics: counts for the last 24 hours and last week + top-5 violators of the week. The "🔄 Refresh" button redraws the report in place |

### Runtime settings (`/settings`)

Source priority: **Redis override > `.env` / environment > defaults**. Changes apply on the fly (e.g. `CHECK_INTERVAL` resets the ticker). Structural and secret keys (API URL/token, all `TELEGRAM_*`, `REDIS_URL`, `TIMEZONE`, `LANGUAGE`, `WEBHOOK_*`, `IP_WHITELIST`, `IGNORED_NODE_UUIDS`, `MAXMIND_*`, `DAILY_REPORT_TIME`, `HEALTH_ADDR`, `LOG_FORMAT`, `REMNAWAVE_COOKIES`, `REMNAWAVE_HEADERS`, `WHITELIST_USER_IDS`, `ASN_DATABASE_PATH`, `TRAFFIC_IGNORED_NODE_UUIDS`, `SHARED_HWID_SQUAD_UUIDS`) require an `.env` change + restart. "Reset to .env" (per key, or all with a confirmation) removes the override.

The menu lives in a single message that is redrawn in place:

- **Main screen** — a table of every setting grouped by section (🎯 Detection, 🛡 Actions, ⏳ Thresholds and cooldowns, 🌐 Grouping, 📈 Traffic, 🔁 Shared HWID, 🧰 Service); ♻️ marks values changed in the bot.
- **Section** — values, a hint for each setting and buttons: flags toggle with one tap (enabled ones are green), the rest open the edit screen.
- **Edit screen** — current value, its source (`.env` or the bot), the variable name, quick presets (minutes are shown in readable form, `0` = "forever") and typing your own value as a message. A validation error is shown on the same screen and you can retry right away.

### Daily report (`DAILY_REPORT=true`)

Once a day at `DAILY_REPORT_TIME` (local time in `TIMEZONE`) the bot posts the same report as `/stats` (24h/week counts + top-5) to the chat. Only real post-threshold violations (`VIOLATION_THRESHOLD`) are counted; soft warnings are excluded from statistics. `DAILY_REPORT` is toggleable at runtime via `/settings`; `DAILY_REPORT_TIME` requires a restart.

With traffic monitoring on (`TRAFFIC_MONITOR=true`), the report has a “📈 Persistently high traffic” section. It lists users who had at least `TRAFFIC_HEAVY_DAYS` days of `TRAFFIC_HEAVY_GB` or more in the last 7 closed days, sorted by weekly traffic, with the number of such days and the 7-day total. Up to 10 users are shown and whitelisted users are skipped. A separate alert for such a user comes no more than once every 7 days, but the report shows them every day.

### Update check (`UPDATE_CHECK=true`)

A minute after start and then once a day the limiter fetches the latest release of the GitHub repository (`api.github.com`, no token). If it is newer than the running version, the bot posts the current and new versions, the update command and a “📝 What's new” button that opens the release page. Each version is announced once: the last announced version is kept in Redis (`update:notified`), so restarts do not repeat the notice. Drafts and pre-releases are ignored. Network errors and GitHub rate limits are only logged (`warn`). The request goes out directly, or through `HTTPS_PROXY` when it is set. Turn it off with `UPDATE_CHECK=false` or in `/settings` → “🧰 Service”.

### Traffic monitoring (`TRAFFIC_MONITOR=true`)

The limiter compares each user's daily traffic with that user's own usual volume and reports two situations:

- **Spike.** In a day the user used `TRAFFIC_SPIKE_FACTOR` times their usual volume and at least `TRAFFIC_SPIKE_MIN_GB`. The usual volume is the median traffic of those of the previous 14 days when the user actually used the subscription, so a single odd day barely moves it. A day without traffic is missing data, not zero traffic. So until there are 5 such days (a new subscription, a return after a break), the user gets no spike alerts. This surfaces a hijacked account or the start of sharing or reselling.
- **Persistently high traffic.** At least `TRAFFIC_HEAVY_DAYS` of the last 7 days with `TRAFFIC_HEAVY_GB` or more, and the latest day is heavy too. This surfaces seedboxes and resellers: their usual volume is already high, so there is no spike.

The alert goes to the same chat with the same buttons as the device-limit alert. It shows the day's traffic, the usual volume and how many times it was exceeded, the three busiest nodes, device-limit violations this week and a 14-day chart. Monitoring only notifies and never disables anyone itself. Whitelisted users are skipped. A repeat spike alert for the same user comes no sooner than 3 days later, a repeat persistently-high alert no sooner than 7 days. The daily report (`DAILY_REPORT=true`) shows such users every day in its “📈 Persistently high traffic” section.

**Example.** `ivan`'s usual volume is 6 GB a day (14-day median). Every 30 minutes the limiter fetches today's traffic for all nodes in one request and adds it up per user:

- at 09:00 `ivan` has 60 GB (DE-1 40 + NL-1 20): that is ×10, but less than 150 GB, so no alert;
- at 11:30 `ivan` has 180 GB: more than max(10 × 6, 150) = 150 GB, and “📈 Unusual traffic” arrives with ×30.

`petr` usually uses 15 GB and downloaded 130 GB today. That is ×8.7, below ×10, so no alert: for a heavy user it is a one-off large download. Over the last 7 days `seedbox` used 240, 190, 280, 260, 220, 300, 250 GB. Its usual volume is itself about 250 GB, so there is no spike. But 6 of the 7 days exceed 200 GB, and “📈 Persistently high traffic” arrives after the day rolls over.

**Days and history.** The panel counts days in UTC. After the day rolls over, the limiter requests the finished day once in full, writes the totals to its own history in Redis (16 days, every user with at least 100 MB on a node) and checks them against both rules. On first start it loads the last 14 days from the panel right away (14 requests), so spikes are detected from the first poll. If the panel does not keep old history, spike detection starts after 5 days.

**Nodes.** The request covers every panel node except `IGNORED_NODE_UUIDS` and `TRAFFIC_IGNORED_NODE_UUIDS` (for example, nodes where heavy traffic is normal). Disabled nodes are counted too, since they may have had traffic today. The number of nodes does not affect the cost: the panel stores rows only where the user actually was. If a user can reach 20 of 100 nodes, they have at most 20 rows a day.

The panel applies the threshold to each node and user pair, not to the user's total across nodes. So during the day the limiter asks with a per-node threshold of one tenth of `TRAFFIC_SPIKE_MIN_GB` (15 GB for 150 GB), which still catches someone who spread their traffic evenly over 10 nodes. When a day is closed the threshold is 100 MB per node: someone who spread 200 GB over 16 nodes (12.5 GB on each) is invisible during the day but is found after the rollover. On a real panel a user goes through about 5 nodes a day on average.

**Panel load.** About 49 statistics requests a day (48 during the day and one at night) whatever the number of subscriptions, plus a light node-list request before each poll. There are no per-user requests. Each request reads only one day of rows through the primary key of `nodes_user_usage_history`. Measured on a panel with 25 nodes and about 2,500 active users a day:

- the daytime request takes 0.4 s and 34 KB;
- the nightly one takes 1.4 s and 420 KB;
- the first start with the 14-day backfill takes about 5 s;
- the history in Redis takes about 3 MB.

With 50,000 subscriptions (about 10 times as many active users) expect 1–4 s for the daytime request, 5–15 s and about 4 MB for the nightly one, and about 30 MB of history.

**Why these defaults.** They are based on measurements on the same panel. Its median daily traffic is 3.5 GB, and 99% of users stay under 34 GB. With ×5 and 30 GB a spike would fire about 9 times a day, with ×10 and 100 GB about 1.3 times. Those are real anomalies: 185 GB against a usual 6 GB, 105 GB against 2.6 GB. The “100 GB on 5 of 7 days” rule fired on one user in a week, who used 1.1 TB a day. The number of alerts grows with the number of users, so the defaults leave a margin: 150 GB for a spike and 200 GB for persistently high traffic. On a small panel they can be lowered to 100 GB. The panel itself tracks traffic limits of plans (`BANDWIDTH_USAGE_NOTIFICATIONS_THRESHOLD`).

It needs the `bandwidth-stats:node-usage` scope; without it the limiter logs one warning and skips the polls. If user traffic records are turned off in the panel (`SERVICE_DISABLE_USER_USAGE_RECORDS=true`), there is no data, and the limiter warns about it.

### Shared HWID (`SHARED_HWID_MONITOR=true`)

Some customers create a new account to get the trial again but connect from the same phone. The client app sends the device HWID to the panel, and the second account gets the same one. The limiter notices this and sends a “👥 One HWID on several accounts” alert.

Every 5 minutes the limiter makes one request for the newest HWID devices (`GET /api/hwid/devices`, newest first) and goes through the ones that appeared since the last poll. For each new device it looks up the user. If `SHARED_HWID_SQUAD_UUIDS` is set, only users in those squads are checked further, for example users in the trial squad. For them the limiter looks for the same HWID on other accounts: with an exact lookup in the panel (indexed, no `LIKE`) and in its own history in Redis. Only an account on which this device appeared earlier counts as a match, so the alert is always about the newer account. The alert goes out when at least one such account is found. Matches are searched among all users, not only in those squads, so a paying customer who took a trial on a new account shows up too.

**Why its own history.** Many bots delete a trial subscription as soon as it ends, and the panel deletes the user's devices along with the user. By the time the same phone shows up on a new account, the old one is gone from the panel. So the limiter keeps every device it has seen for `SHARED_HWID_HISTORY_DAYS` days, 30 by default (`hwid:seen:{hwid}`: account id, name and Telegram ID, when the device appeared). Older records are ignored and expire from Redis on their own. In the alert such an account is marked “🗑 deleted from the panel”. When first enabled, the limiter fills the history from the panel without alerts: it reads the devices of the last `SHARED_HWID_HISTORY_DAYS` days in pages of 1000 and stops at older ones. On a panel with 10,500 devices the last 30 days held about 3,000 devices, which took 3 requests in ~1.3 s. The same happens if the limiter has not polled the panel for more than a day, so that a downtime does not end in a burst of old alerts.

With `SHARED_HWID_HISTORY_DAYS=0` there is no history of its own: the limiter compares a new device only with accounts that still exist in the panel, and the first start is one request. Devices are remembered for 7 hours, only so they are not handled twice. If the bot deletes a trial subscription when it ends, a repeated trial cannot be found in this mode.

**What does not count as a match.**

- Accounts with the same Telegram ID as the new one. That is one bot customer with several subscriptions; on a real panel this covers more than half of the shared HWIDs. The alert says so when such accounts are hidden.
- Whitelisted users, on either side.

The alert goes to the same chat with the same buttons as the device-limit alert, and the buttons act on the new account. The alert shows:

- the new account: subscription status, when it was created, squads;
- the device and app, the HWID;
- a table of the other accounts with this HWID: status and squads, when the account was created, when the device appeared on it. A deleted account is marked “🗑 deleted from the panel”, and an account that removed this device itself is marked “device removed”.

Each pair of accounts gives one alert per 30 days (`hwid:reported:{id}`): if a pair shares several devices, say a phone and a tablet, the second one does not give a new alert. At most 20 alerts go out per poll; the rest follow on the next polls. If more than ~540 devices appeared since the last poll (for example, after a downtime shorter than a day), they are recorded in the history without alerts and a warning goes to the log, so a backlog never turns into thousands of requests in a row. If Telegram or the panel did not answer, the device is handled again on the next poll. If it still cannot be handled after 6 hours, it is recorded in the history without an alert and a warning goes to the log. This alert is not sent to the webhook. The first poll runs 90 seconds after start.

**Panel load.** One list request every 5 minutes, as fast as the panel's own “HWID Inspector” page. On top of that, the limiter looks up the user once for each new device. For a user in the watched squads it adds an exact HWID lookup and a lookup of every account found. Measured on a panel with about 10,500 devices and about 150 new ones a day:

- list request: ~0.3 s;
- filling the history on first start: 3 requests in ~1.3 s (30 days, ~3,000 devices);
- per day: about 600 light requests when every user is checked and about 480 with a single trial squad (288 of them are the list polls);
- alerts per day: 6 without a squad filter, 0 with the trial squad filter;
- history in Redis: about 0.6 MB (estimated at ~0.2 KB per device).

**A large panel.** On a panel with about 100,000 subscriptions and ~1% trials a day, the load on the panel itself stays small. This is an estimate from the measurements above, not a measurement:

- ~240,000 devices and ~3,500 new ones a day;
- about 5,500 light requests a day, roughly one every 15 s;
- a poll handles about 12 new devices with a few seconds of sequential requests.

Two things matter more:

- **The first history fill.** About 100 pages for 30 days: a few minutes of work for the panel database, once and after a downtime longer than a day.
- **Redis memory.** ~20 MB at 30 days and ~65 MB at 90. `docker-compose.yml` sets no memory limit for Valkey, so the AOF file grows with it.

Pick the retention to match how soon people come back for another trial.

**Limits.** The HWID comes from the client app. Devices are recorded only when the HWID device limit is enabled in the panel. On some platforms reinstalling the app changes the HWID, and it can be spoofed on purpose. So the alert catches those who hide nothing, and it is a reason to check the account, not proof.

It needs the `hwid-user-devices:list` scope; without it the limiter logs one warning and skips the polls.

#### Traffic limit (`SHARED_HWID_TRAFFIC_LIMIT_MB`)

The alert can come with an action: with `SHARED_HWID_TRAFFIC_LIMIT_MB=100` the limiter itself sets a 100 MB traffic limit on the new account (`PATCH /api/users`, `trafficLimitBytes`) right before the alert is sent. A repeated trial is then nearly useless, and the customer sees that the traffic has run out and can buy a subscription.

- It works only when `SHARED_HWID_SQUAD_UUIDS` is set, that is, for the trial squad. Without the filter only the alert is sent, because a paying customer could be cut otherwise.
- The limit is absolute. If more has already been used, the panel moves the account to `LIMITED`. A limit below the configured one is never raised, and the alert says “left alone: the limit is already …”.
- The reset strategy (`trafficLimitStrategy`) is not changed. If the trial plan resets daily, the 100 MB are available every day.
- The previous limit is kept in `hwid:traffic:{id}` for 30 days. The “📶 Restore traffic” button in the alert puts it back and lifts `LIMITED` when the previous limit is enough. If the limit was changed in the panel or by the bot meanwhile (for example, the customer paid), the button changes nothing. The button stays in the alert after other actions.
- If the panel rejects the change, the alert still goes out, marked “could not be limited”.
- A bot that sells subscriptions does not know about the cut. On a purchase a bot usually sets the plan's limit again and adds a traffic package to the panel's current limit. Check that your bot does the same.

### Manual mode (`ACTION_MODE=manual`)

On excess the bot sends an alert with buttons:

```
⚠️ Device limit exceeded
┌──────────────────────┬──────────────────────┐
│ 👤 User              │ username123          │
│ 🔗 Subscription      │ open                 │
│ 👥 Squad             │ Germany              │
│ 📊 Limit             │ 3                    │
│ 📡 Detected          │ 5 IP · 2 ASN         │
│ 🚦 Over limit        │ +2                   │
│ 📈 Violations in 24h │ 3                    │
│ 🕐 Time              │ Sat, 11/29 04:15     │  ← in the reader's time zone
└──────────────────────┴──────────────────────┘
▾ 📍 IP addresses (5)
  IP          Provider       Node
  10.0.1.10   Hetzner        node-1
  10.0.3.30   Cloudflare     node-2
▸ 📥 Subscription requests (24 in 3 h)
▸ 📱 HWID devices (2)

[🔄 Drop connections]
[🔒 Disable permanently] [🔒 Disable for 10 min]  ← the second one if AUTO_DISABLE_DURATION > 0
[🔇 Ignore for 15 min]                            ← if IGNORE_DURATION > 0, otherwise "🔇 Ignore"
```

“👥 Squad” lists the subscription's internal squads (`activeInternalSquads`) from the same `GET /api/users/{id}` request, so the panel gets no extra calls. With several squads the row is called “Squads” and shows up to five names. A subscription without squads has no such row. Traffic alerts carry the same row.

**Sharing evidence.** The alert's main table has two summary rows, and two collapsed blocks with the details follow the IP list:

- “📥 Subscription requests” — the last 24 subscription requests from the panel, grouped by IP: provider, apps (Happ, v2rayNG, browser…) and how many times. 🟢 marks an IP that is connected right now. Summary row: `9 IP · 4 ASN · apps: 4`.
- “📱 HWID devices” — the devices the panel registered for the user: model, OS, app and last activity. Summary row: `2 / 3` (devices / limit).

One phone that switches between Wi-Fi and LTE requests the subscription with one app from two or three networks. A link handed to friends shows many networks and different apps. More IPs than the limit with HWID devices within the limit suggests exported keys. The data is requested only when an alert is sent (two parallel requests, at most 8 s) and needs the `users:subscription-request-history` and `hwid-user-devices:list-by-user` scopes. Without them, on an older panel, or with request history turned off (`SERVICE_DISABLE_SRH_RECORDS`), the alert arrives without these blocks and a warning is logged once. The HWID block is empty when the panel's HWID limit is off.

After a button is pressed the alert keeps its content, the buttons disappear and a summary is added at the bottom: what was done, by whom and when. After a subscription is disabled an "🔓 Enable subscription" button remains, so the action can be undone. If the alert cannot be edited (deleted, too old, or the edit was rejected), the result is posted as a separate reply to the alert.

The duration of "Disable for N min" and "Ignore for N min" is stored in the button itself when the alert is sent: changing `AUTO_DISABLE_DURATION`/`IGNORE_DURATION` via `/settings` does not change what already-sent buttons do. "Disable permanently" and "Enable subscription" cancel a scheduled auto-restore, so an old timer cannot re-enable the user.

| Button | Action |
|--------|--------|
| Drop connections | Reset active user connections via API |
| Disable permanently | Permanently deactivate the subscription |
| Disable for N min | Temporary deactivation with auto-restore (when `AUTO_DISABLE_DURATION > 0`) |
| Ignore | Add to whitelist: temporary if `IGNORE_DURATION > 0`, otherwise permanent |

### Automatic mode (`ACTION_MODE=auto`)

The subscription is disabled automatically; the bot sends an informational alert with an "Enable subscription" button. With `AUTO_DISABLE_DURATION > 0`, the subscription is restored by timer, and the alert shows when that will happen.

**Within-tolerance alerts (`AUTO_NOTIFY_SOFT=true`).** When you want to ban only on a noticeable excess (`TOLERANCE`) but still know about users who already crossed the HWID limit:

| Device count | Action |
|--------------|--------|
| `devices <= limit` | silence |
| `limit < devices <= limit+TOLERANCE` | 🔔 informational alert, **no ban** (with "Drop connections" and "Ignore" buttons) |
| `devices > limit+TOLERANCE` | 🔒 ban + alert |

Soft warnings use a separate cooldown (`cooldown:soft:`), do **not** increment the 24h violation counter, are **not** counted toward `VIOLATION_THRESHOLD`, and send a webhook with the `soft_violation_detected` event.

## Webhook

On a violation the limiter can send an HTTP POST to `WEBHOOK_URL` (works in both modes). Every request carries `X-Timestamp` (unix send time). When `WEBHOOK_SECRET` is set, these headers are added:

| Header | Contents |
|--------|----------|
| `X-Signature-V2` | `sha256=<hex>` — HMAC-SHA256 of the string `<X-Timestamp>.<body>`. **Recommended check:** the signature covers the time too, so a replayed request with a fresh `X-Timestamp` fails it. Reject requests whose `X-Timestamp` is older than a few minutes |
| `X-Signature` | `sha256=<hex>` — HMAC-SHA256 of the body only. Kept for compatibility: protects against tampering, not against replay |
| `X-Webhook-Secret` | The raw secret, for a simple match. Anyone who sees one request learns the secret and can sign anything — set `WEBHOOK_SECRET_HEADER=false` if the receiver verifies the signature |

Receiver check (pseudocode): `hmac_sha256(secret, ts + "." + body) == header["X-Signature-V2"]` and `now − ts < 300`.

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

**What if the panel API is unavailable?** The service logs an error, skips the cycle, and retries after `CHECK_INTERVAL`. API requests retry up to 3 times with jittered exponential backoff; 429 and 408 are retried too, and the panel's `Retry-After` header is honoured. A cycle does not count as successful when no node could be polled, when the panel has no active node at all, or when no user could be checked (Redis or the user request is unavailable) — `/healthz` will surface the problem.

**Can I use Redis from Remnawave?** You can, but it's not recommended — the project runs its own Redis. For an existing one, set `REDIS_URL`.

**How to change a user's limit?** Via the `hwidDeviceLimit` field in the user's subscription settings in the Remnawave panel.

## Support & License

[GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues) · GNU GPL v3.0 — see [LICENSE](LICENSE)
