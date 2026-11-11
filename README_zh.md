# Remnawave Limiter

**Remnawave 的集中式设备控制**

通过面板 API 监控用户的同时连接：收集所有节点的 IP，与每个用户的设备限额（`hwidDeviceLimit`）比较，并对超限作出响应——通过 Telegram 机器人通知，或自动停用订阅。

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

[Русская версия](README.md) · [English version](README_en.md)

## 功能

- **汇总所有节点的 IP**——全面掌握连接情况，与用户各自的限额 + 容差（tolerance）比较
- **两种模式**：`manual`——发送带内联按钮的告警（断开连接、停用订阅、忽略）；`auto`——自动停用订阅，并通过定时任务自动恢复
- **违规次数阈值**——仅在时间窗口内超限 N 次后执行操作（防止误判）
- 按子网（`/24`）或 ASN 网络服务商对 IP **分组**——应对 CGNAT 和账号共享
- 违规时发送带 HMAC 签名的 **Webhook**（JSON POST）
- **统计与报告**——`/stats` 命令（24 小时/一周统计 + 违规最多的前 5 名用户），以及发送到聊天的每日报告
- **检查更新**——每天将当前版本与 GitHub 上的最新发布版本比较一次，有新版本时在聊天中通知
- **流量监控**——将用户每日流量与其自身常态比较：发现突增和持续高流量，不会按用户逐个请求
- 通过 `/settings` 修改**运行时设置**——无需重启即可即时更改参数（保存在 Redis 中）
- 用户和 IP/CIDR **白名单**、冷却时间、缓存、时区选择、俄语/英语/简体中文
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

**检查流程**：获取节点列表（`GET /api/nodes`）→ 并行收集各节点 IP → 按用户汇总 → 根据 `lastSeen` 过滤 → 与 `限额 + 容差` 比较 → 递增阈值计数器 → 达到阈值时执行操作（告警或自动停用）。

单个节点故障不会中断检查：继续检查其他节点，并在日志中警告数据不完整。如果所有节点均未响应，则跳过整轮检查——否则，连接数据缺失会被误认为“所有用户均未超限”。

## 要求

- Docker 和 Docker Compose
- **Remnawave 3.0.0+**，以及 API 令牌（权限范围为 `connections:*`、`users:*` 和 `nodes:read`；告警中的 HWID 部分还需要 `hwid-user-devices:read`，流量监控需要 `bandwidth-stats:node-usage`）
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
| `connections:drop` | `POST /api/connections/drop` | 仅用于“断开连接”按钮：`manual` 模式告警、容差范围内告警（`AUTO_NOTIFY_SOFT`）和流量告警 |
| `users:subscription-request-history` | `GET /api/users/{id}/subscription-request-history` | 告警中的“订阅请求”部分；可选 |
| `hwid-user-devices:list-by-user` | `GET /api/hwid/devices/{userId}` | 告警中的“HWID 设备”部分；可选 |
| `bandwidth-stats:node-usage` | `POST /api/bandwidth-stats/nodes/usage` | 流量监控（`TRAFFIC_MONITOR`）；可选 |

节点仅需读取权限：`nodes:*` 还会授予写入权限（重启和停用节点、修改配置档案（Config Profiles）和入站（Inbounds）），limiter 并不需要这些权限。

</details>

### 应选择哪个 limiter 版本

| Remnawave 面板版本 | limiter 版本 |
|---|---|
| **3.0.0 及更新版本** | **4.0.0+**——当前版本，标签为 `latest` |
| 低于 3.0.0（2.x 分支） | [3.0.0](https://github.com/syvlech/remnawave-limiter/releases/tag/3.0.0)——最后一个支持 2.x 面板的版本 |

这些版本不能互换使用：Remnawave 3.0.0 将 `ip-control` 模块重命名为 `connections`，并使用数字 `id` 而非 `uuid` 标识用户，旧端点已被移除。因此，limiter 4.0.0+ 不支持 2.x 面板，limiter 3.0.0 也不支持 3.x 面板。

如果面板尚未升级，请在 `docker-compose.yml` 中将镜像固定为 3.0.0 版本：

```yaml
image: ghcr.io/syvlech/remnawave-limiter:b422f43
```

`b422f43` 是 3.0.0 版本对应的提交。ghcr 中没有单独的 `3.0.0` 镜像标签：CI 仅为 `v*` 形式的引用构建语义化版本标签，而该版本使用的是不带前缀的 `3.0.0` 标签。

## 安装

```bash
mkdir -p /opt/remnawave-limiter && cd /opt/remnawave-limiter

curl -O https://raw.githubusercontent.com/syvlech/remnawave-limiter/master/docker-compose.yml
curl -O https://raw.githubusercontent.com/syvlech/remnawave-limiter/master/.env.example

cp .env.example .env && nano .env
```

`.env` 中的必填参数：

```bash
REMNAWAVE_API_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-api-token-here
TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11X
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_ADMIN_IDS=123456789
LANGUAGE=zh
```

启动、查看日志与更新：

```bash
docker compose pull && docker compose up -d   # 启动 / 更新
docker compose logs -f limiter                # 查看日志
```

## 配置

所有设置均通过 `.env` 或环境变量配置。

| 参数 | 默认值 | 说明 |
|----------|:---:|----------|
| `REMNAWAVE_API_URL` | **必填** | Remnawave 面板地址 |
| `REMNAWAVE_API_TOKEN` | **必填** | API 令牌（在面板中生成） |
| `REMNAWAVE_COOKIES` | — | 额外的 Cookie 身份验证。格式：以 `;` 分隔的 `key=value`（例如 `cf_clearance=abc; session=xyz`）。适用于位于 Cloudflare/WAF 后的面板 |
| `REMNAWAVE_HEADERS` | — | 每次 API 请求附带的额外 HTTP 请求头。格式：以 `;` 分隔的 `Name: Value`（例如 `X-Api-Key: secret123; X-Custom-Header: value`）。圆括号内的 `;` 不会分隔请求头（User-Agent 可原样填写），其他位置的 `;` 请写成 `\;`。适用于位于代理/Cloudflare/防护服务后的面板 |
| `TELEGRAM_BOT_TOKEN` | **必填** | 从 @BotFather 获取的机器人令牌 |
| `TELEGRAM_CHAT_ID` | **必填** | 接收告警的聊天/频道/群组 ID |
| `TELEGRAM_ADMIN_IDS` | **必填** | 以逗号分隔的管理员 ID（只有这些用户可以点击操作按钮） |
| `TELEGRAM_THREAD_ID` | — | 超级群组中的消息线程/话题 ID |
| `TELEGRAM_PROXY` | — | Telegram API 代理。支持协议：`http`、`https`、`socks5`、`socks5h`。格式：`scheme://[user:pass@]host:port` |
| `CHECK_INTERVAL` | `60` | 检查间隔（秒） |
| `ACTIVE_IP_WINDOW` | `120` | `lastSeen` 距今小于此值（秒）时，IP 被视为活跃 |
| `TOLERANCE` | `0` | 允许超出限额的固定数量。限额 3 + 容差 1 → 达到 5 个及以上时触发操作 |
| `TOLERANCE_MULTIPLIER` | `0` | 按限额比例的额外容差：`TOLERANCE + floor(limit × multiplier)`。0 表示不使用。允许范围为 `0..100` |
| `COOLDOWN` | `300` | 同一用户两次告警之间的冷却时间（秒） |
| `USER_CACHE_TTL` | `600` | 用户数据缓存有效期（秒） |
| `DEFAULT_DEVICE_LIMIT` | `0` | 未设置 `hwidDeviceLimit` 时使用的限额。0 = 不限制 |
| `ACTION_MODE` | `manual` | `manual`——发送带按钮的告警；`auto`——自动停用订阅 |
| `AUTO_DISABLE_DURATION` | `15` | 临时停用时长（分钟）。0 = 永久。在 `manual` 模式下添加相应按钮，在 `auto` 模式下设置自动恢复时间 |
| `AUTO_NOTIFY_SOFT` | `false` | 仅用于 `auto` 模式。超出数量**在容差范围内**（`limit < 设备数 <= limit+TOLERANCE`）时发送信息告警，不封禁。仅在超过 `limit+TOLERANCE` 时封禁。此处的容差为完整容差：`TOLERANCE + floor(limit × TOLERANCE_MULTIPLIER)` |
| `WEBHOOK_URL` | — | 违规时接收 Webhook 的 URL（POST JSON）。留空 = 禁用 |
| `WEBHOOK_SECRET` | — | Webhook 密钥，用于 HMAC-SHA256 签名 `X-Signature`（请求体）和 `X-Signature-V2`（`<X-Timestamp>.<请求体>`）。参见 [Webhook](#webhook)（可选） |
| `WEBHOOK_SECRET_HEADER` | `true` | 是否在 `X-Webhook-Secret` 请求头中发送原始密钥。为兼容性而保留；如果接收端验证签名，请关闭 |
| `WHITELIST_USER_IDS` | — | 从检查中排除的用户数字 ID（以逗号分隔）。4.0 之前版本使用的 UUID 在此不适用 |
| `IGNORED_NODE_UUIDS` | — | 收集 IP 时忽略的节点 UUID（不纳入报告和决策）。用于技术/测试节点 |
| `IP_WHITELIST` | — | 从计数中排除的 IP 和/或 CIDR 子网，以逗号分隔。用于过滤节点/网桥/中继 IP。支持 IPv4/IPv6。示例：`203.0.113.5,10.0.0.0/8,2001:db8::/32` |
| `IGNORE_DURATION` | `0` | “忽略”按钮操作的有效时长（分钟）。`0` = 永久。`> 0` = 带有效期的临时白名单 |
| `VIOLATION_THRESHOLD` | `3` | 执行操作所需的违规次数。1 = 立即响应 |
| `VIOLATION_THRESHOLD_WINDOW` | `3600` | 违规计数窗口（秒）。必须大于 `(VIOLATION_THRESHOLD − 1) × (COOLDOWN + CHECK_INTERVAL)`，否则阈值无法达到，配置会被拒绝 |
| `SUBNET_GROUPING` | `false` | 按 `/SUBNET_PREFIX_V4` 对 IPv4 分组——统计子网而非 IP 数量（减少 CGNAT 导致的误判）。IPv6 逐 IP 计数 |
| `SUBNET_PREFIX_V4` | `24` | IPv4 前缀长度（8..32）。24 为标准值；16 适合以移动网络用户为主的场景。在 `SUBNET_GROUPING=true` 时生效 |
| `ASN_GROUPING` | `false` | 统计不同的 ASN 网络服务商数量，而非 IP/子网数量——最准确的反共享信号。每个无 ASN 的 IP 各自计为一个独立分组。优先于 `SUBNET_GROUPING`。需要 MaxMind ASN 数据库 |
| `ASN_DATABASE_PATH` | `./geoip/GeoLite2-ASN.mmdb` | `GeoLite2-ASN.mmdb` 路径。目录自动创建。仅在使用非标准目录布局时修改 |
| `MAXMIND_LICENSE_KEY` | — | MaxMind 密钥。设置后，启动时自动下载缺失的数据库，并在后台更新。[获取](https://www.maxmind.com/en/geolite2/signup) |
| `MAXMIND_UPDATE_INTERVAL` | `168h` | 数据库自动更新间隔（最小 `1h`）。仅在设置了 `MAXMIND_LICENSE_KEY` 时生效 |
| `REDIS_URL` | `redis://redis:6379` | Redis 地址 |
| `TIMEZONE` | `UTC` | 告警时间戳使用的时区（例如 `Europe/Moscow`） |
| `LANGUAGE` | `ru` | 界面语言：`ru`、`en` 或 `zh` |
| `DAILY_REPORT` | `true` | 向聊天发送每日违规报告（违规排行 + 计数）。可通过 `/settings` 在运行时切换 |
| `DAILY_REPORT_TIME` | `21:00` | 发送报告的本地时间（`HH:MM`，使用 `TIMEZONE`）。修改后需要重启 |
| `UPDATE_CHECK` | `true` | 每天在 GitHub 上检查一次新版本并在聊天中通知，每个版本只通知一次。见[检查更新](#检查更新update_checktrue)。可通过 `/settings` 在运行时切换 |
| `TRAFFIC_MONITOR` | `true` | 流量监控：相对用户常态的突增，以及持续高流量。见[流量监控](#流量监控traffic_monitortrue)。可通过 `/settings` 在运行时切换 |
| `TRAFFIC_SPIKE_FACTOR` | `10` | 当日流量需超过常态（14 天内有流量日的中位数）多少倍才算突增。`1.5..100`。可在 `/settings` 中修改 |
| `TRAFFIC_SPIKE_MIN_GB` | `150` | 判定突增的当日最低流量，单位 GB。可在 `/settings` 中修改 |
| `TRAFFIC_HEAVY_GB` | `200` | 当日流量达到此值即为高流量日，单位 GB。`0` 表示关闭该规则。可在 `/settings` 中修改 |
| `TRAFFIC_HEAVY_DAYS` | `5` | 最近 7 天中需有多少个高流量日才发送持续高流量告警（`1..7`）。可在 `/settings` 中修改 |
| `TRAFFIC_IGNORED_NODE_UUIDS` | — | 不计入流量的节点 UUID，逗号分隔（在 `IGNORED_NODE_UUIDS` 之外），例如本就以大流量为常态的节点 |
| `HEALTH_ADDR` | — | HTTP 存活检查端点 `/healthz` 的监听地址（例如 `:8080`）。留空 = 禁用 |
| `LOG_LEVEL` | `info` | 日志详细程度：`trace`、`debug`、`info`、`warn`、`error`。`info` 每轮检查输出一条摘要，并记录所有操作；`debug` 额外记录各 IP 的详情及 Telegram 通信详情。可通过 `/settings` 在运行时修改 |
| `LOG_FORMAT` | `text` | `text` 便于人工阅读，`json` 用于日志采集器（Loki、ELK）。修改后需要重启 |

**通知中的 ASN**。如果 MaxMind 数据库可用，告警的 IP 列表中每个 IP 都会显示网络服务商（“网络服务商”列），Webhook 中带有 `asn` 和 `asn_org` 字段，“📡 检测到”一行则显示不同 ASN 的数量（`4 IP · 3 ASN`）。这些展示信息不影响限制逻辑——决策仅依据 IP/子网/ASN 数量。

## 限额逻辑

| `hwidDeviceLimit` | 行为 |
|:-:|----------|
| `> 0` | 用作设备限额 |
| `null` | 使用配置中的 `DEFAULT_DEVICE_LIMIT` |
| `0` | 不限制——跳过该用户 |

## 违规次数阈值

当 `VIOLATION_THRESHOLD=1` 时，每次超限都会触发响应。设置为更大的值时（默认为 3），仅在 `VIOLATION_THRESHOLD_WINDOW` 窗口内累计 N 次违规后执行操作：超限 → 检查冷却时间 → 递增计数器（TTL = 窗口）→ 达到阈值时执行操作并重置计数器。如果两次违规之间的间隔超过窗口，计数器将清零。

冷却时间在递增前设置，因此计数器最多每 `COOLDOWN` 增长一次，而冷却结束后，下一次违规要到随后最近的一轮检查才会被发现。窗口必须容纳 `N − 1` 个这样的步骤：`VIOLATION_THRESHOLD_WINDOW > (VIOLATION_THRESHOLD − 1) × (COOLDOWN + CHECK_INTERVAL)`。不满足此条件的配置会在启动时和 `/settings` 中被拒绝——否则阈值将永远无法达到，limiter 会悄无声息地停止执行操作。

以 `VIOLATION_THRESHOLD=3`、`VIOLATION_THRESHOLD_WINDOW=3600` 为例：

| 时间 | 计数器 | 操作 |
|:---:|:---:|----------|
| 12:00 | 1/3 | 记录日志，不执行操作 |
| 12:05 | 2/3 | 记录日志，不执行操作 |
| 12:10 | 3/3 | 告警/封禁，重置计数器 |
| 12:15 | 1/3 | 记录日志，不执行操作 |

## Telegram 机器人

机器人的所有消息均为 Telegram **富消息**（Bot API 10.3，`sendRichMessage`）：支持标题、表格、折叠区块、按阅读者时区显示的时间（`tg-time`），以及消息内部的彩色按钮。如果 Telegram 拒绝富消息格式，消息仍会以带内联按钮的纯文本发送。需要支持富消息的 Telegram 客户端才能显示。

### 命令

仅对 `TELEGRAM_ADMIN_IDS` 中的管理员开放。启动时自动注册到机器人的命令菜单。

| 命令 | 功能 |
|---------|------------|
| `/settings` | 交互式运行时设置菜单。即时修改安全参数（无需重启）；数值保存在 Redis 中，重启后仍保留。详见下方“运行时设置”章节 |
| `/stats` | 违规统计：最近 24 小时及一周的次数 + 最近一周违规最多的前 5 名用户。“🔄 刷新”按钮会原地更新报告 |

### 运行时设置（`/settings`）

配置来源优先级：**Redis 覆盖值 > `.env` / 环境变量 > 默认值**。更改即时生效（例如，`CHECK_INTERVAL` 会重新创建定时器）。结构性和敏感参数（API URL/令牌、所有 `TELEGRAM_*`、`REDIS_URL`、`TIMEZONE`、`LANGUAGE`、`WEBHOOK_*`、`IP_WHITELIST`、`IGNORED_NODE_UUIDS`、`MAXMIND_*`、`DAILY_REPORT_TIME`、`HEALTH_ADDR`、`LOG_FORMAT`、`REMNAWAVE_COOKIES`、`REMNAWAVE_HEADERS`、`WHITELIST_USER_IDS`、`ASN_DATABASE_PATH`、`TRAFFIC_IGNORED_NODE_UUIDS`）**只能**通过修改 `.env` 并重启来更改。“♻️ 重置为 .env 配置”按钮（针对单个参数）和“♻️ 全部重置为 .env 配置”按钮（需确认）可移除覆盖值。

菜单在同一条消息中运行，并原地更新：

- **主界面**——按类别列出所有参数的表格（🎯 检测、🛡 处理方式、⏳ 阈值与冷却、🌐 分组、📈 流量、🧰 服务）；♻️ 标记在机器人中修改过的值。
- **分类界面**——显示数值、各参数的说明及按钮：开关只需点击一次即可切换（开启时显示绿色），其他参数则打开编辑界面。
- **编辑界面**——显示当前值、来源（`.env` 或机器人）、变量名、快捷预设（分钟以便于阅读的形式显示，`0` = “永久”），也可通过消息输入自定义值。校验错误显示在同一界面，可以立即重新输入。

### 每日报告（`DAILY_REPORT=true`）

每天在 `DAILY_REPORT_TIME`（`TIMEZONE` 时区的本地时间），机器人向聊天发送与 `/stats` 相同的报告（24 小时/一周违规次数 + 前 5 名）。仅统计达到 `VIOLATION_THRESHOLD` 阈值后实际触发的违规；容差范围内告警不计入统计。`DAILY_REPORT` 可通过 `/settings` 在运行时切换，`DAILY_REPORT_TIME` 仅能通过重启修改。

启用流量监控（`TRAFFIC_MONITOR=true`）时，报告中会有“📈 持续高流量”部分。其中列出最近 7 个已结算日中至少有 `TRAFFIC_HEAVY_DAYS` 天流量达到 `TRAFFIC_HEAVY_GB` 的用户，按一周流量从高到低排列，并显示这样的天数和 7 天总流量。最多显示 10 名用户，白名单中的用户不显示。针对这类用户的单独告警至少间隔 7 天，但报告中每天都能看到他们。

### 检查更新（`UPDATE_CHECK=true`）

启动一分钟后，以及此后每天一次，limiter 会获取 GitHub 仓库的最新发布版本（`api.github.com`，无需令牌）。如果它比当前运行的版本新，机器人会向聊天发送当前版本、新版本、更新命令，以及打开发布页面的“📝 更新内容”按钮。每个版本只通知一次：最近通知过的版本保存在 Redis（`update:notified`）中，因此重启不会重复通知。草稿和预发布版本不计入。网络错误和 GitHub 请求频率限制只会记录到日志（`warn`）。请求直接发出；如果设置了 `HTTPS_PROXY`，则经由该代理。如需关闭，请设置 `UPDATE_CHECK=false`，或在 `/settings` →“🧰 服务”中关闭。

### 流量监控（`TRAFFIC_MONITOR=true`）

limiter 会把每个用户的每日流量与该用户自己的常态进行比较，并在两种情况下发出通知：

- **突增**。当日流量达到常态的 `TRAFFIC_SPIKE_FACTOR` 倍，且不少于 `TRAFFIC_SPIKE_MIN_GB`。常态是前 14 天中用户实际使用订阅的那些日子的流量中位数，单独一天的异常几乎不会改变它。没有流量的日子表示没有数据，而不是流量为零：因此在这样的日子少于 5 天时（新订阅、暂停后重新使用），不会对该用户检测突增。这样可以发现账号被盗，以及开始共享或转售。
- **持续高流量**。最近 7 天中至少有 `TRAFFIC_HEAVY_DAYS` 天的流量达到 `TRAFFIC_HEAVY_GB`，并且最近一天同样是高流量日。这样可以发现做种机和转售：它们的常态本身就高，不会出现突增。

告警发送到同一个聊天，按钮与设备超限告警相同。告警中包含当日流量、常态及超出倍数、流量最大的三个节点、本周设备超限次数以及 14 天的柱状图。监控只发送通知，不会自行停用任何用户。白名单中的用户会被跳过。同一用户的突增告警至少间隔 3 天才会再次发送，持续高流量告警至少间隔 7 天。每日报告（`DAILY_REPORT=true`）的“📈 持续高流量”部分每天都会列出这类用户。

**示例**。`ivan` 的常态为每天 6 GB（14 天中位数）。limiter 每 30 分钟用一个请求获取所有节点今日的流量，并按用户汇总：

- 09:00 时 `ivan` 为 60 GB（DE-1 40 + NL-1 20）：这是常态的 10 倍，但少于 150 GB，不发告警；
- 11:30 时 `ivan` 为 180 GB：超过 max(10 × 6, 150) = 150 GB，发出“📈 流量异常”，超出 30 倍。

`petr` 的常态为 15 GB，今天下载了 130 GB。这是 8.7 倍，低于 10 倍，不发告警：对重度用户来说这只是一次大文件下载。`seedbox` 最近 7 天为 240、190、280、260、220、300、250 GB。它的常态本身就约为 250 GB，因此没有突增；但 7 天中有 6 天超过 200 GB，日期切换后会收到“📈 持续高流量”。

**日期与历史**。面板按 UTC 计算日期。日期切换后，limiter 会一次性请求刚结束那一天的完整数据，将汇总写入自己在 Redis 中的历史（保留 16 天，记录在某个节点上达到 100 MB 的所有用户），并用两条规则检查。首次启用时会立即从面板加载最近 14 天的数据（14 个请求），因此从第一次轮询起就能检测突增。如果面板不保存旧历史，则运行 5 天后才开始检测突增。

**节点**。请求覆盖面板的所有节点，`IGNORED_NODE_UUIDS` 和 `TRAFFIC_IGNORED_NODE_UUIDS` 中的节点除外（例如本就以大流量为常态的节点）。已停用的节点同样计入，因为它们今天可能产生过流量。节点数量不影响开销：面板只在用户实际使用过的节点上保存记录。如果用户只能使用 100 个节点中的 20 个，他每天最多只有 20 条记录。

面板对“节点 + 用户”这一组合应用阈值，而不是对用户在所有节点上的总和。因此白天 limiter 以 `TRAFFIC_SPIKE_MIN_GB` 的十分之一作为每个节点的阈值（150 GB 时为 15 GB），即使有人把流量平均分到 10 个节点也能被发现。结算当天数据时，阈值为每个节点 100 MB：把 200 GB 分散到 16 个节点（每个 12.5 GB）的人白天看不到，但日期切换后会被发现。在真实面板上，用户平均每天会经过约 5 个节点。

**对面板的负载**。无论订阅数量多少，每天约 49 个统计请求（白天 48 个，夜间 1 个），每次轮询前另有一个轻量的节点列表请求，不会按用户逐个请求。每个请求只通过 `nodes_user_usage_history` 表的主键读取一天的数据。在一个有 25 个节点、每天约 2,500 名活跃用户的面板上实测：

- 白天的请求耗时 0.4 秒，响应 34 KB；
- 夜间的请求耗时 1.4 秒，响应 420 KB；
- 首次启动并补全 14 天历史约需 5 秒；
- Redis 中的历史约占 3 MB。

对于 5 万订阅（活跃用户约为上述面板的 10 倍），预计白天请求需 1–4 秒，夜间请求需 5–15 秒、约 4 MB，历史约占 30 MB。

**默认值的依据**。这些值依据同一个面板上的实测数据选定。该面板每日流量的中位数为 3.5 GB，99% 的用户不超过 34 GB。使用 5 倍和 30 GB 时，突增每天约触发 9 次；使用 10 倍和 100 GB 时约为 1.3 次，而且这些确实是异常：常态 6 GB 却用了 185 GB，常态 2.6 GB 却用了 105 GB。“7 天中有 5 天达到 100 GB”这条规则一周内只命中一名用户，他每天用掉 1.1 TB。告警数量会随用户数量增长，因此默认值留有余量：突增为 150 GB，持续高流量为 200 GB。在小型面板上可以降到 100 GB。套餐的流量限额由面板自己跟踪（`BANDWIDTH_USAGE_NOTIFICATIONS_THRESHOLD`）。

需要 `bandwidth-stats:node-usage` 权限；没有该权限时，limiter 只会记录一次警告并跳过轮询。如果面板关闭了用户流量记录（`SERVICE_DISABLE_USER_USAGE_RECORDS=true`），将没有数据，limiter 会对此发出警告。

### 手动模式（`ACTION_MODE=manual`）

超限时，机器人发送带按钮的告警：

```
⚠️ 超出设备限额
┌──────────────────────┬──────────────────────┐
│ 👤 用户              │ username123          │
│ 🔗 订阅              │ 打开                 │
│ 📊 设备限额          │ 3                    │
│ 📡 检测到            │ 5 IP · 2 ASN         │
│ 🚦 超出数量          │ +2                   │
│ 📈 24 小时内违规次数 │ 3                    │
│ 🕐 时间              │ 周六，11月29日 04:15 │  ← 使用阅读者的时区
└──────────────────────┴──────────────────────┘
▾ 📍 IP 地址 (5)
  IP          网络服务商     节点
  10.0.1.10   Hetzner        node-1
  10.0.3.30   Cloudflare     node-2
▸ 📥 订阅请求 (24 次，3 小时内)
▸ 📱 HWID 设备 (2)

[🔄 断开连接]
[🔒 永久停用] [🔒 停用 10 分钟]  ← AUTO_DISABLE_DURATION > 0 时显示第二个按钮
[🔇 忽略 15 分钟]                ← IGNORE_DURATION > 0 时显示，否则为“🔇 忽略”
```

**共享迹象**。告警主表中有两行摘要，IP 列表下方还有两个折叠的详情部分：

- “📥 订阅请求”——面板中最近 24 次订阅请求记录，按 IP 分组：网络服务商、应用（Happ、v2rayNG、浏览器……）以及次数。🟢 表示当前有来自该 IP 的连接。主表摘要：`9 IP · 4 ASN · 应用 4 个`。
- “📱 HWID 设备”——面板为该用户登记的设备：型号、系统、应用和最近活动时间。主表摘要：`2 / 3`（设备数 / 限额）。

一部在 Wi-Fi 和 LTE 之间切换的手机，会用同一个应用从两三个网络请求订阅；分享给朋友的链接则会出现许多网络和不同的应用。如果 IP 数超过限额而 HWID 设备数没有超过，很可能是导出了密钥。这些数据只在发送告警时请求（两个并行请求，最长 8 秒），需要 `users:subscription-request-history` 和 `hwid-user-devices:list-by-user` 权限。没有这些权限、面板版本较旧或关闭了请求历史（`SERVICE_DISABLE_SRH_RECORDS`）时，告警不含这两部分，日志中只会提示一次。如果面板未启用 HWID 限额，HWID 部分为空。

点击按钮后，告警内容会保留，按钮消失，并在底部添加操作结果：执行了什么、由谁执行以及执行时间。停用订阅后，会保留“🔓 启用订阅”按钮，以便撤销操作。如果无法编辑告警（已删除、时间过久或 Telegram 拒绝编辑），操作结果将作为回复告警的单独消息发送。

“停用 N 分钟”和“忽略 N 分钟”的时长在发送告警时就保存在按钮中：通过 `/settings` 修改 `AUTO_DISABLE_DURATION`/`IGNORE_DURATION` 不会改变已发送按钮的含义。“永久停用”和“启用订阅”会取消已安排的自动恢复任务，防止旧定时器再次启用用户。

| 按钮 | 操作 |
|--------|----------|
| 断开连接 | 通过 API 断开该用户的活跃连接 |
| 永久停用 | 永久停用订阅 |
| 停用 N 分钟 | 临时停用并自动恢复（当 `AUTO_DISABLE_DURATION > 0` 时） |
| 忽略 | 加入白名单：`IGNORE_DURATION > 0` 时为临时，否则为永久 |

### 自动模式（`ACTION_MODE=auto`）

订阅自动停用，机器人发送带“启用订阅”按钮的信息告警。设置 `AUTO_DISABLE_DURATION > 0` 时，订阅会通过定时任务恢复，告警中会显示恢复时间。

**容差范围内告警（`AUTO_NOTIFY_SOFT=true`）**。如果希望仅在明显超限（`TOLERANCE`）时封禁，同时仍想知道哪些用户已超过 HWID 限额：

| 设备数量 | 操作 |
|------------------|----------|
| `设备数 <= limit` | 不通知 |
| `limit < 设备数 <= limit+TOLERANCE` | 🔔 信息告警，**不封禁**（带“断开连接”和“忽略”按钮） |
| `设备数 > limit+TOLERANCE` | 🔒 封禁 + 告警 |

容差范围内告警使用独立冷却时间（`cooldown:soft:`），**不**增加 24 小时违规计数，**不**计入 `VIOLATION_THRESHOLD`，并发送事件为 `soft_violation_detected` 的 Webhook。

## Webhook

违规时，limiter 可以向 `WEBHOOK_URL` 发送 HTTP POST（两种模式均支持）。每个请求均附带 `X-Timestamp`（发送时的 Unix 时间）。设置 `WEBHOOK_SECRET` 后，还会添加以下请求头：

| 请求头 | 内容 |
|-----------|------------|
| `X-Signature-V2` | `sha256=<hex>`——对字符串 `<X-Timestamp>.<请求体>` 计算 HMAC-SHA256。**推荐的校验方式**：签名同时覆盖时间，因此使用新 `X-Timestamp` 重放的请求无法通过验证。应拒绝 `X-Timestamp` 距今超过几分钟的请求 |
| `X-Signature` | `sha256=<hex>`——仅对请求体计算 HMAC-SHA256。为兼容性而保留：防篡改，但不防重放 |
| `X-Webhook-Secret` | 原始密钥，用于直接比对。任何看到一次请求的人都能得知密钥，并为任意内容签名——如果接收端验证签名，请设置 `WEBHOOK_SECRET_HEADER=false` |

接收端校验（伪代码）：`hmac_sha256(secret, ts + "." + body) == header["X-Signature-V2"]` 且 `now − ts < 300`。

发送不会阻塞监控循环。遇到网络错误、5xx 或 429 时会重试（最多 3 次尝试）；4xx 被视为接收端主动拒绝，不再重试。服务停止时事件不会丢失：收到 SIGTERM 后，limiter 会等待发送完成，而不是将其丢弃。

**请求体示例**：

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
| `event` | 封禁时为 `violation_detected`；容差范围内告警时为 `soft_violation_detected`（`AUTO_NOTIFY_SOFT`） |
| `action_mode` | `manual` 或 `auto` |
| `user` | 用户数据（user_id、username、email、telegram_id、subscription_url） |
| `violation.ips` | 活跃 IP，以及对应节点和最近活跃时间 |
| `violation.ip_count` | 不重复的 IP 数量 |
| `violation.device_limit` / `tolerance` / `effective_limit` | 限额、容差和有效限额（`device_limit + tolerance`） |
| `violation.violation_count_24h` | 24 小时内的违规次数 |
| `violation.grouping_mode` | 设备计数依据：`ip`、`subnet` 或 `asn` |
| `violation.device_group_count` | 所选模式下最终的“设备”数量——用于与限额比较的就是此值 |
| `violation.subnet_count` / `asn_group_count` | 子网 / ASN 分组数量。仅在启用对应模式时出现 |
| `violation.ips[].asn` / `asn_org` | 网络服务商的 ASN 编号和名称。仅在 MaxMind 数据库已加载且成功解析 ASN 时出现 |
| `action.auto_disable_duration_min` | 停用时长，单位为分钟（0 = 永久） |
| `timestamp` | 检测时间（ISO 8601） |

## 常见问题

**如何查询 Telegram Chat ID**？添加 [@userinfobot](https://t.me/userinfobot) 并发送 `/start`。群组/频道请使用 [@getidsbot](https://t.me/getidsbot)。

**如果面板 API 不可用会怎样**？服务会记录错误，跳过本轮检查，并在 `CHECK_INTERVAL` 后再次尝试。API 请求最多重试 3 次，使用带随机抖动的指数退避；429 和 408 也会重试，并遵循面板返回的 `Retry-After` 请求头。以下情况的检查轮次不会被视为成功：未能轮询任何节点、面板没有任何活跃节点，或未能检查任何用户（Redis 或用户请求不可用）——此时 `/healthz` 会反映问题。

**能否使用 Remnawave 的 Redis**？可以，但不推荐——本项目会启动自己的 Redis。若使用已有实例，请设置 `REDIS_URL`。

**如何修改用户限额**？在 Remnawave 面板的订阅设置中修改 `hwidDeviceLimit` 字段。

## 支持与许可证

[GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues) · GNU GPL v3.0——参见 [LICENSE](LICENSE)
