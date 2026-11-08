# Remnawave Limiter

**Remnawave 的集中式设备控制**

通过面板 API 监控用户的同时连接：收集所有节点的 IP，与每个用户的设备数量限制（`hwidDeviceLimit`）比较，并对超限作出响应——通过 Telegram 机器人通知，或自动封禁订阅。

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

[英文版](README_en.md)

## 功能

- **汇总所有节点的 IP**——全面掌握连接情况，与用户各自的限额 + 容差（tolerance）比较
- **两种模式：**`manual`——发送带内联按钮的告警（断开连接、封禁、加入白名单）；`auto`——自动封禁，并通过定时任务自动恢复
- **违规次数阈值**——仅在时间窗口内超限 N 次后执行操作（防止误判）
- 按子网（`/24`）或 ASN 网络服务商对 IP **分组**——应对 CGNAT 和账号共享
- 违规时发送带 HMAC 签名的 **Webhook**（JSON POST）
- **统计与报告**——`/stats` 命令（24 小时/一周统计 + 违规最多的前 5 名用户），以及发送到聊天的每日报告
- 通过 `/settings` 修改**运行时设置**——无需重启即可即时更改参数（保存在 Redis 中）
- 用户和 IP/CIDR **白名单**、冷却时间、缓存、时区选择、俄语/英语
- **存活检查 `/healthz`**（可选，通过 `HEALTH_ADDR` 配置），用于 Docker/编排器健康检查
- **适合通过 `docker compose logs -f` 查看日志**——每轮检查输出一条摘要，可在运行时修改日志级别，支持通过 `LOG_FORMAT=json` 接入 Loki/ELK

## 架构

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

在面板旁或任意服务器上运行一个实例。无需在节点上安装——所有操作均通过 API 完成。Docker Compose 启动服务和 Redis。

**检查流程：**获取节点列表（`GET /api/nodes`）→ 并行收集各节点 IP → 按用户汇总 → 根据 `lastSeen` 过滤 → 与 `限额 + 容差` 比较 → 递增阈值计数器 → 达到阈值时执行操作（告警或自动封禁）。

单个节点故障不会中断检查：继续检查其他节点，并在日志中警告数据不完整。如果所有节点均未响应，则跳过整轮检查——否则，连接数据缺失会被误认为“所有用户均未超限”。

## 要求

- Docker 和 Docker Compose
- **Remnawave 3.0.0+**，以及 API 令牌（权限范围为 `connections:*`、`users:*` 和 `nodes:read`）
- Telegram 机器人（[@BotFather](https://t.me/BotFather)）

<details>
<summary>最小权限范围</summary>

如果希望令牌仅具备 limiter 实际调用所需的权限：

| 权限范围 | 端点 | 用途 |
|---|---|---|
| `nodes:list` | `GET /api/nodes` | 获取待轮询节点列表——缺少此权限将无法开始检查 |
| `connections:by-node` | `POST /api/connections/by-node/{nodeUuid}` | 启动节点 IP 收集任务 |
| `connections:by-node-result` | `GET /api/connections/by-node/{jobId}` | 获取收集结果 |
| `users:by-id` | `GET /api/users/{id}` | 获取设备限额及用户数据 |
| `users:disable` | `POST /api/users/{id}/actions/disable` | 停用订阅 |
| `users:enable` | `POST /api/users/{id}/actions/enable` | “启用”按钮及定时恢复 |
| `connections:drop` | `POST /api/connections/drop` | 仅用于 `manual` 模式下的“断开连接”按钮 |

节点仅需读取权限：`nodes:*` 还会授予写入权限（重启和停用节点、修改配置文件及入站），limiter 并不需要这些权限。

</details>

### 应选择哪个 limiter 版本

| Remnawave 面板版本 | limiter 版本 |
|---|---|
| **3.0.0 及更新版本** | **4.0.0+**——当前版本，标签为 `latest` |
| 低于 3.0.0（2.x 分支） | [3.0.0](https://github.com/syvlech/remnawave-limiter/releases/tag/3.0.0)——最后一个支持 2.x 面板的版本 |

这些版本不能互换使用：Remnawave 3.0.0 将 `ip-control` 模块重命名为 `connections`，并使用数字 `id` 而非 `uuid` 标识用户，旧端点已被移除。因此，limiter 4.0.0+ 不支持 2.x 面板，limiter 3.0.0 也不支持 3.x 面板。

如果面板尚未升级，请在 `docker-compose.yml` 中将镜像固定为 3.0.0 版本：

```yaml
image: ghcr.io/avsba001/remnawave-limiter:b422f43
```

`b422f43` 是 3.0.0 版本对应的提交。ghcr 中没有单独的 `3.0.0` 镜像标签：CI 仅为 `v*` 形式的引用构建语义化版本标签，而该版本使用的是不带前缀的 `3.0.0` 标签。

## 安装

```bash
mkdir -p /opt/remnawave-limiter && cd /opt/remnawave-limiter

curl -O https://raw.githubusercontent.com/avsba001/remnawave-limiter/master/docker-compose.yml
curl -O https://raw.githubusercontent.com/avsba001/remnawave-limiter/master/.env.example

cp .env.example .env && nano .env
```

`.env` 中的必填参数：

```bash
REMNAWAVE_API_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-api-token-here
TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11X
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_ADMIN_IDS=123456789
```

启动、查看日志与更新：

```bash
docker compose pull && docker compose up -d   # 启动 / 更新
docker compose logs -f limiter                # 检查
```

## 配置

所有设置均通过 `.env` 或环境变量配置。

| 参数 | 默认值 | 说明 |
|----------|:---:|----------|
| `REMNAWAVE_API_URL` | **必填** | Remnawave 面板地址 |
| `REMNAWAVE_API_TOKEN` | **必填** | API 令牌（在面板中生成） |
| `REMNAWAVE_COOKIES` | — | 额外的 Cookie 身份验证。格式：以 `;` 分隔的 `key=value`（例如 `cf_clearance=abc; session=xyz`）。适用于位于 Cloudflare/WAF 后的面板 |
| `REMNAWAVE_HEADERS` | — | 每次 API 请求附带的额外 HTTP 请求头。格式：以 `;` 分隔的 `Name: Value`（例如 `X-Api-Key: secret123; X-Custom-Header: value`）。适用于位于代理/Cloudflare/防护服务后的面板 |
| `TELEGRAM_BOT_TOKEN` | **必填** | 从 @BotFather 获取的机器人令牌 |
| `TELEGRAM_CHAT_ID` | **必填** | 接收告警的聊天/频道/群组 ID |
| `TELEGRAM_ADMIN_IDS` | **必填** | 以逗号分隔的管理员 ID（只有这些用户可以点击操作按钮） |
| `TELEGRAM_THREAD_ID` | — | 超级群组中的消息线程/话题 ID |
| `TELEGRAM_PROXY` | — | Telegram API 代理。支持协议：`http`、`https`、`socks5`、`socks5h`。格式：`scheme://[user:pass@]host:port` |
| `CHECK_INTERVAL` | `30` | 检查间隔（秒） |
| `ACTIVE_IP_WINDOW` | `300` | `lastSeen` 距今小于此值（秒）时，IP 被视为活跃 |
| `TOLERANCE` | `0` | 允许超出限额的固定数量。限额 3 + 容差 1 → 达到 5 个及以上时触发操作 |
| `TOLERANCE_MULTIPLIER` | `0` | 比例容差：`TOLERANCE + floor(limit × multiplier)`。0 表示不使用。允许范围为 `0..100` |
| `COOLDOWN` | `300` | 同一用户两次告警之间的冷却时间（秒） |
| `USER_CACHE_TTL` | `600` | 用户数据缓存有效期（秒） |
| `DEFAULT_DEVICE_LIMIT` | `0` | 未设置 `hwidDeviceLimit` 时使用的限额。0 = 不限制 |
| `ACTION_MODE` | `manual` | `manual`——发送带按钮的告警；`auto`——自动停用订阅 |
| `AUTO_DISABLE_DURATION` | `0` | 临时停用时长（分钟）。0 = 永久。在 `manual` 模式下添加相应按钮，在 `auto` 模式下设置自动恢复时间 |
| `AUTO_NOTIFY_SOFT` | `false` | 仅用于 `auto` 模式。超出数量**在容差范围内**（`limit < 设备数 <= limit+TOLERANCE`）时发送信息告警，不封禁。仅在超过 `limit+TOLERANCE` 时封禁 |
| `WEBHOOK_URL` | — | 违规时接收 Webhook 的 URL（POST JSON）。留空 = 禁用 |
| `WEBHOOK_SECRET` | — | Webhook 密钥，用于 HMAC-SHA256 签名 `X-Signature`（请求体）和 `X-Signature-V2`（`<X-Timestamp>.<请求体>`）。参见 [Webhook](#webhook)（可选） |
| `WEBHOOK_SECRET_HEADER` | `true` | 是否在 `X-Webhook-Secret` 请求头中发送原始密钥。为兼容性而保留；如果接收端验证签名，请关闭 |
| `WHITELIST_USER_IDS` | — | 从检查中排除的用户数字 ID（以逗号分隔）。4.0 之前版本使用的 UUID 在此不适用 |
| `IGNORED_NODE_UUIDS` | — | 收集 IP 时忽略的节点 UUID（不纳入报告和决策）。用于技术/测试节点 |
| `IP_WHITELIST` | — | 从计数中排除的 IP 和/或 CIDR 子网，以逗号分隔。用于过滤节点/网桥/中继 IP。支持 IPv4/IPv6。示例：`203.0.113.5,10.0.0.0/8,2001:db8::/32` |
| `IGNORE_DURATION` | `0` | “忽略”按钮操作的有效时长（分钟）。`0` = 永久。`> 0` = 带有效期的临时白名单 |
| `VIOLATION_THRESHOLD` | `1` | 执行操作所需的违规次数。1 = 立即响应 |
| `VIOLATION_THRESHOLD_WINDOW` | `3600` | 违规计数窗口（秒）。必须大于 `(VIOLATION_THRESHOLD − 1) × (COOLDOWN + CHECK_INTERVAL)`，否则阈值无法达到，配置会被拒绝 |
| `SUBNET_GROUPING` | `false` | 按 `/SUBNET_PREFIX_V4` 对 IPv4 分组——统计子网而非 IP 数量（减少 CGNAT 导致的误判）。IPv6 逐 IP 计数 |
| `SUBNET_PREFIX_V4` | `24` | IPv4 前缀长度（8..32）。24 为标准值；16 适合以移动网络用户为主的场景。在 `SUBNET_GROUPING=true` 时生效 |
| `ASN_GROUPING` | `false` | 统计不同的 ASN 网络服务商数量，而非 IP/子网数量——最准确的反共享信号。无 ASN 的 IP 分别作为独立分组。优先于 `SUBNET_GROUPING`。需要 MaxMind ASN 数据库 |
| `ASN_DATABASE_PATH` | `./geoip/GeoLite2-ASN.mmdb` | `GeoLite2-ASN.mmdb` 路径。目录自动创建。仅在使用非标准目录布局时修改 |
| `MAXMIND_LICENSE_KEY` | — | MaxMind 密钥。设置后，启动时自动下载缺失的数据库，并在后台更新。[获取](https://www.maxmind.com/en/geolite2/signup) |
| `MAXMIND_UPDATE_INTERVAL` | `168h` | 数据库自动更新间隔（最小 `1h`）。仅在设置了 `MAXMIND_LICENSE_KEY` 时生效 |
| `REDIS_URL` | `redis://redis:6379` | Redis 地址 |
| `TIMEZONE` | `UTC` | 告警时间戳使用的时区（例如 `Europe/Moscow`） |
| `LANGUAGE` | `ru` | 界面语言：`ru``en`或`zh` |
| `DAILY_REPORT` | `false` | 向聊天发送每日违规报告（违规排行 + 计数）。可通过 `/settings` 在运行时切换 |
| `DAILY_REPORT_TIME` | `09:00` | 发送报告的本地时间（`HH:MM`，使用 `TIMEZONE`）。修改后需要重启 |
| `HEALTH_ADDR` | — | HTTP 存活检查端点 `/healthz` 的监听地址（例如 `:8080`）。留空 = 禁用 |
| `LOG_LEVEL` | `info` | 日志详细程度：`trace`、`debug`、`info`、`warn`、`error`。`info` 每轮检查输出一条摘要，并记录所有操作；`debug` 额外记录各 IP 的详情及 Telegram 通信详情。可通过 `/settings` 在运行时修改 |
| `LOG_FORMAT` | `text` | `text` 便于人工阅读，`json` 用于日志采集器（Loki、ELK）。修改后需要重启 |

**通知中的 ASN。** 如果 MaxMind 数据库可用，告警和 Webhook 中每个 IP 旁会显示网络服务商（`• 91.107.96.11 - Hetzner Online GmbH (Chicago-1)`），标题中则显示不同 ASN 的数量（`检测到：4 IP（3 ASN）`）。这些展示信息不影响限制逻辑——决策仅依据 IP/子网/ASN 数量。

## 限额逻辑

| `hwidDeviceLimit` | 行为 |
|:-:|----------|
| `> 0` | 用作设备数量限制 |
| `null` | 使用配置中的 `DEFAULT_DEVICE_LIMIT` |
| `0` | 不限制——跳过该用户 |

## 违规次数阈值

当 `VIOLATION_THRESHOLD=1`（默认）时，每次超限都会触发响应。设置为更大的值时，仅在 `VIOLATION_THRESHOLD_WINDOW` 窗口内累计 N 次违规后执行操作：超限 → 检查冷却时间 → 递增计数器（TTL = 窗口）→ 达到阈值时执行操作并重置计数器。如果两次违规之间的间隔超过窗口，计数器将清零。

冷却时间在递增前设置，因此计数器最多每 `COOLDOWN` 增长一次，而下一次违规会在最近的一轮检查中被发现。窗口必须容纳 `N − 1` 个这样的步骤：`VIOLATION_THRESHOLD_WINDOW > (VIOLATION_THRESHOLD − 1) × (COOLDOWN + CHECK_INTERVAL)`。不满足此条件的配置会在启动时和 `/settings` 中被拒绝——否则阈值将永远无法达到，限制器会悄无声息地停止执行操作。

以 `VIOLATION_THRESHOLD=3`、`VIOLATION_THRESHOLD_WINDOW=3600` 为例：

| 时间 | 计数器 | 操作 |
|:---:|:---:|----------|
| 12:00 | 1/3 | 记录日志，不执行操作 |
| 12:05 | 2/3 | 记录日志，不执行操作 |
| 12:10 | 3/3 | 告警/封禁，重置计数器 |
| 12:15 | 1/3 | 记录日志，不执行操作 |

## Telegram 机器人

机器人的所有消息均为 Telegram **富消息**（Bot API 10.3，`sendRichMessage`）：支持标题、表格、折叠区块、按阅读者时区显示的时间（`tg-time`），以及消息内部的彩色按钮。如果 Telegram 拒绝富消息标记，消息仍会以带内联按钮的纯文本发送。需要支持富消息的 Telegram 客户端才能显示。

### 命令

仅对 `TELEGRAM_ADMIN_IDS` 中的管理员开放。启动时自动注册到机器人的命令菜单。

| 命令 | 功能 |
|---------|------------|
| `/settings` | 交互式运行时设置菜单。即时修改安全参数（无需重启）；数值保存在 Redis 中，重启后仍保留。详见下方“运行时设置”章节 |
| `/stats` | 违规统计：最近 24 小时及一周的次数 + 最近一周违规最多的前 5 名用户。“🔄 刷新”按钮会原地更新报告 |

### 运行时设置（`/settings`）

配置来源优先级：**Redis 覆盖值 > `.env` / 环境变量 > 默认值**。更改即时生效（例如，`CHECK_INTERVAL` 会重新创建定时器）。结构性和敏感参数（API URL/令牌、所有 `TELEGRAM_*`、`REDIS_URL`、`TIMEZONE`、`LANGUAGE`、`WEBHOOK_*`、`IP_WHITELIST`、`IGNORED_NODE_UUIDS`、`MAXMIND_*`、`DAILY_REPORT_TIME`、`HEALTH_ADDR`、`LOG_FORMAT`）**只能**通过修改 `.env` 并重启来更改。“重置为 .env”按钮可移除覆盖值（针对单个参数，或确认后针对全部参数）。

菜单在同一条消息中运行，并原地更新：

- **主界面**——按类别列出所有参数的表格（🎯 检测、🛡 处理方式、⏳ 阈值与冷却、🌐 分组、🧰 服务）；♻️ 标记在机器人中修改过的值。
- **分类界面**——显示数值、各参数的说明及按钮：开关只需点击一次即可切换（开启时显示绿色），其他参数则打开编辑界面。
- **编辑界面**——显示当前值、来源（`.env` 或机器人）、变量名、快捷预设（分钟以便于阅读的形式显示，`0` = “永久”），也可通过消息输入自定义值。校验错误显示在同一界面，可以立即重新输入。

### 每日报告（`DAILY_REPORT=true`）

每天在 `DAILY_REPORT_TIME`（`TIMEZONE` 时区的本地时间），机器人向聊天发送与 `/stats` 相同的报告（24 小时/一周违规次数 + 前 5 名）。仅统计达到 `VIOLATION_THRESHOLD` 阈值后实际触发的违规；“软”警告不计入统计。`DAILY_REPORT` 可通过 `/settings` 在运行时切换，`DAILY_REPORT_TIME` 仅能通过重启修改。

### 人工模式（`ACTION_MODE=manual`）

超限时，机器人发送带按钮的告警：

```
⚠️ 超出设备数量限制
┌──────────────────────┬──────────────────────┐
│ 👤 用户              │ username123          │
│ 🔗 订阅              │ 打开                 │
│ 📊 数量限制          │ 3                    │
│ 📡 检测到            │ 5 IP · 2 ASN         │
│ 🚦 超出数量          │ +2                   │
│ 📈 24 小时内违规次数 │ 3                    │
│ 🕐 时间              │ 周六，11月29日 04:15 │  ← 使用阅读者的时区
└──────────────────────┴──────────────────────┘
▾ 📍 IP 地址（5）
  IP          网络服务商     节点
  10.0.1.10   Hetzner        node-1
  10.0.3.30   Cloudflare     node-2

[🔄 断开连接]
[🔒 永久停用] [🔒 停用 10 分钟]  ← AUTO_DISABLE_DURATION > 0 时显示第二个按钮
[🔇 忽略 15 分钟]                ← IGNORE_DURATION > 0 时显示，否则为“🔇 忽略”
```

点击按钮后，告警内容会保留，按钮消失，并在底部添加操作结果：执行了什么、由谁执行以及执行时间。停用订阅后，会保留“🔓 启用订阅”按钮，以便撤销操作。如果无法编辑告警（已删除、时间过久或 Telegram 拒绝编辑），操作结果将作为回复告警的单独消息发送。

“停用 N 分钟”和“忽略 N 分钟”的时长在发送告警时就保存在按钮中：通过 `/settings` 修改 `AUTO_DISABLE_DURATION`/`IGNORE_DURATION` 不会改变已发送按钮的含义。“永久停用”和“启用订阅”会取消已安排的自动恢复任务，防止旧定时器再次启用用户。

| 按钮 | 操作 |
|--------|----------|
| 断开连接 | 通过 API 重置活跃连接 |
| 永久停用 | 永久停用订阅 |
| 停用 N 分钟 | 临时停用并自动恢复（当 `AUTO_DISABLE_DURATION > 0` 时） |
| 忽略 | 加入白名单：`IGNORE_DURATION > 0` 时为临时，否则为永久 |

### 自动模式（`ACTION_MODE=auto`）

订阅自动停用，机器人发送带“启用订阅”按钮的信息告警。设置 `AUTO_DISABLE_DURATION > 0` 时，订阅会通过定时任务恢复，告警中会显示恢复时间。

**容差范围内通知（`AUTO_NOTIFY_SOFT=true`）。** 当你希望仅在明显超限（`TOLERANCE`）时封禁，同时仍了解哪些用户已经超过 HWID 限额时：

| 设备数量 | 操作 |
|------------------|----------|
| `设备数 <= limit` | 不通知 |
| `limit < 设备数 <= limit+TOLERANCE` | 🔔 信息告警，**不封禁**（带“断开连接”和“忽略”按钮） |
| `设备数 > limit+TOLERANCE` | 🔒 封禁 + 告警 |

“软”警告使用独立冷却时间（`cooldown:soft:`），**不**增加 24 小时违规计数，**不**计入 `VIOLATION_THRESHOLD`，并发送事件为 `soft_violation_detected` 的 Webhook。

## Webhook

违规时，limiter 可以向 `WEBHOOK_URL` 发送 HTTP POST（两种模式均支持）。每个请求均附带 `X-Timestamp`（发送时的 Unix 时间）。设置 `WEBHOOK_SECRET` 后，还会添加以下请求头：

| 请求头 | 内容 |
|-----------|------------|
| `X-Signature-V2` | `sha256=<hex>`——对字符串 `<X-Timestamp>.<请求体>` 计算 HMAC-SHA256。**推荐的校验方式：**签名同时覆盖时间，因此使用新 `X-Timestamp` 重放的请求无法通过验证。应拒绝 `X-Timestamp` 早于数分钟前的请求 |
| `X-Signature` | `sha256=<hex>`——仅对请求体计算 HMAC-SHA256。为兼容性而保留：防篡改，但不防重放 |
| `X-Webhook-Secret` | 原始密钥，用于直接比对。任何看到一次请求的人都能得知密钥，并为任意内容签名——如果接收端验证签名，请设置 `WEBHOOK_SECRET_HEADER=false` |

接收端校验（伪代码）：`hmac_sha256(secret, ts + "." + body) == header["X-Signature-V2"]` 且 `now − ts < 300`。

发送不会阻塞监控循环。遇到网络错误、5xx 或 429 时会重试（最多 3 次尝试）；4xx 被视为接收端主动拒绝，不再重试。服务停止时事件不会丢失：收到 SIGTERM 后，limiter 会等待发送完成，而不是将其丢弃。

**请求体示例：**

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

| 字段 | 说明 |
|------|----------|
| `event` | 封禁时为 `violation_detected`；“软”警告时为 `soft_violation_detected`（`AUTO_NOTIFY_SOFT`） |
| `action_mode` | `manual` 或 `auto` |
| `user` | 用户数据（user_id、username、email、telegram_id、subscription_url） |
| `violation.ips` | 活跃 IP，以及对应节点和最近活跃时间 |
| `violation.ip_count` | 不重复的 IP 数量 |
| `violation.device_limit` / `tolerance` / `effective_limit` | 限额、容差和有效限额（`device_limit + tolerance`） |
| `violation.violation_count_24h` | 24 小时内的违规次数 |
| `violation.grouping_mode` | 设备计数依据：`ip`、`subnet` 或 `asn` |
| `violation.device_group_count` | 所选模式下最终的“设备”数量——用于与限额比较的就是此值 |
| `violation.subnet_count` / `asn_group_count` | 子网 / ASN 分组数量。仅在启用对应模式时出现 |
| `violation.ips[].asn` / `asn_org` | 网络服务商编号和名称。仅在 MaxMind 数据库已加载且成功解析 ASN 时出现 |
| `action.auto_disable_duration_min` | 封禁时长（0 = 永久） |
| `timestamp` | 检测时间（ISO 8601） |

## 常见问题

**如何查询 Telegram Chat ID？** 添加 [@userinfobot](https://t.me/userinfobot) 并发送 `/start`。群组/频道请使用 [@getidsbot](https://t.me/getidsbot)。

**如果面板 API 不可用会怎样？** 服务会记录错误，跳过本轮检查，并在 `CHECK_INTERVAL` 后再次尝试。API 请求最多重试 3 次，使用带随机抖动的指数退避；429 和 408 也会重试，并遵循面板返回的 `Retry-After` 请求头。未能轮询任何节点的检查轮次不会被视为成功——此时 `/healthz` 会反映问题。

**能否使用 Remnawave 的 Redis？** 可以，但不推荐——本项目会启动自己的 Redis。若使用已有实例，请设置 `REDIS_URL`。

**如何修改用户限额？** 在 Remnawave 面板的订阅设置中修改 `hwidDeviceLimit` 字段。

## 支持与许可证

[GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues) · GNU GPL v3.0——参见 [LICENSE](LICENSE)
