# Remnawave Limiter

**Централизованный контроль устройств для Remnawave**

Мониторит одновременные подключения пользователей через API панели: собирает IP со всех нод, сравнивает с лимитом устройств каждого пользователя (`hwidDeviceLimit`) и реагирует на превышения — через Telegram-бота или автоматической блокировкой подписки.

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

[English version](README_en.md)

## Возможности

- **Агрегация IP со всех нод** — полная картина подключений, сравнение с индивидуальным лимитом + погрешность (tolerance)
- **Два режима:** `manual` — алерт с inline-кнопками (сброс, бан, whitelist); `auto` — автоблокировка с автовосстановлением по таймеру
- **Порог нарушений** — действие только после N превышений за окно (защита от ложных срабатываний)
- **Группировка** IP по подсетям (`/24`) или по ASN-провайдерам — против CGNAT и шаринга
- **Webhook** (JSON POST) о нарушениях с HMAC-подписью
- **Статистика и отчёты** — команда `/stats` (за 24ч/неделю + топ-5 нарушителей) и ежедневный отчёт в чат
- **Рантайм-настройки** через `/settings` — изменение параметров на лету без перезапуска (хранятся в Redis)
- **Whitelist** пользователей и IP/CIDR, кулдаун, кэш, выбор часового пояса, ru/en
- **Liveness `/healthz`** (опционально, по `HEALTH_ADDR`) для healthcheck в Docker/оркестраторе

## Архитектура

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

Один инстанс рядом с панелью или на любом сервере. Установка на ноды не нужна — всё через API. Docker Compose поднимает сервис + Redis.

**Цикл проверки:** список нод (`GET /api/nodes`) → параллельный сбор IP по нодам → агрегация по пользователю → фильтр по `lastSeen` → сравнение с `лимит + погрешность` → инкремент порогового счётчика → при достижении порога реакция (алерт или автоблокировка).

## Требования

- Docker и Docker Compose
- Remnawave 2.7.0+ с API-токеном
- Telegram-бот ([@BotFather](https://t.me/BotFather))

## Установка

```bash
mkdir -p /opt/remnawave-limiter && cd /opt/remnawave-limiter

curl -O https://raw.githubusercontent.com/syvlech/remnawave-limiter/master/docker-compose.yml
curl -O https://raw.githubusercontent.com/syvlech/remnawave-limiter/master/.env.example

cp .env.example .env && nano .env
```

Обязательные параметры в `.env`:

```bash
REMNAWAVE_API_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-api-token-here
TELEGRAM_BOT_TOKEN=123456:ABC-DEF
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_ADMIN_IDS=123456789
```

Запуск, логи и обновление:

```bash
docker compose pull && docker compose up -d   # запуск / обновление
docker compose logs -f limiter                # проверка
```

## Конфигурация

Все настройки — через `.env` или переменные окружения.

| Параметр | По умолчанию | Описание |
|----------|:---:|----------|
| `REMNAWAVE_API_URL` | **обязательный** | Адрес панели Remnawave |
| `REMNAWAVE_API_TOKEN` | **обязательный** | API-токен (генерируется в панели) |
| `REMNAWAVE_COOKIES` | — | Доп. авторизация через cookie. Формат: `key=value` через `;` (напр. `cf_clearance=abc; session=xyz`). Для панелей за Cloudflare/WAF |
| `REMNAWAVE_HEADERS` | — | Доп. HTTP-заголовки для каждого запроса к API. Формат: `Name: Value` через `;` (напр. `X-Api-Key: secret123; X-Custom-Header: value`). Для панелей за прокси/Cloudflare/защитой |
| `TELEGRAM_BOT_TOKEN` | **обязательный** | Токен бота от @BotFather |
| `TELEGRAM_CHAT_ID` | **обязательный** | ID чата/канала/группы для алертов |
| `TELEGRAM_ADMIN_IDS` | **обязательный** | ID админов через запятую (только они нажимают кнопки) |
| `TELEGRAM_THREAD_ID` | — | ID треда/топика в супергруппе |
| `TELEGRAM_PROXY` | — | Прокси для Telegram API. Схемы: `http`, `https`, `socks5`, `socks5h`. Формат: `scheme://[user:pass@]host:port` |
| `CHECK_INTERVAL` | `30` | Интервал проверки (сек) |
| `ACTIVE_IP_WINDOW` | `300` | IP активен, если `lastSeen` < этого значения (сек) |
| `TOLERANCE` | `0` | Фиксированное допустимое превышение лимита. Лимит 3 + tolerance 1 → реакция при 5+ |
| `TOLERANCE_MULTIPLIER` | `0` | Пропорциональный допуск: `TOLERANCE + floor(limit × множитель)`. 0 — не используется |
| `COOLDOWN` | `300` | Кулдаун между алертами на пользователя (сек) |
| `USER_CACHE_TTL` | `600` | TTL кэша данных пользователя (сек) |
| `DEFAULT_DEVICE_LIMIT` | `0` | Лимит, если `hwidDeviceLimit` не задан. 0 = без ограничения |
| `ACTION_MODE` | `manual` | `manual` — алерт с кнопками; `auto` — автоотключение подписки |
| `AUTO_DISABLE_DURATION` | `0` | Длительность временного отключения (мин). 0 = перманентно. В `manual` добавляет кнопку, в `auto` — время автовосстановления |
| `AUTO_NOTIFY_SOFT` | `false` | Только `auto`. Превышение **в пределах допуска** (`limit < устройств <= limit+TOLERANCE`) даёт информационный алерт без бана. Бан — только выше `limit+TOLERANCE` |
| `WEBHOOK_URL` | — | URL для webhook при нарушениях (POST JSON). Пусто = выключен |
| `WEBHOOK_SECRET` | — | Секрет webhook. Передаётся в заголовке `X-Webhook-Secret` и используется для HMAC-SHA256 подписи тела в `X-Signature: sha256=<hex>` (опционально) |
| `WHITELIST_USER_IDS` | — | UUID для исключения из проверки (через запятую) |
| `IGNORED_NODE_UUIDS` | — | UUID нод, игнорируемых при сборе IP (не в отчётах, не в решениях). Для технических/тестовых нод |
| `IP_WHITELIST` | — | IP и/или CIDR-подсети через запятую, исключаемые из подсчёта. Отсеивает IP нод/мостов/релеев. IPv4/IPv6. Пример: `203.0.113.5,10.0.0.0/8,2001:db8::/32` |
| `IGNORE_DURATION` | `0` | Время действия кнопки «Игнорировать» (мин). `0` = навсегда. `> 0` = временный whitelist с TTL |
| `VIOLATION_THRESHOLD` | `1` | Сколько нарушений нужно для действия. 1 = мгновенная реакция |
| `VIOLATION_THRESHOLD_WINDOW` | `3600` | Окно подсчёта нарушений (сек). Счётчик сбрасывается при отсутствии новых нарушений в окне |
| `SUBNET_GROUPING` | `false` | Группировка IPv4 по `/SUBNET_PREFIX_V4` — считаются подсети вместо IP (снижает ложные срабатывания от CGNAT). IPv6 — по-штучно |
| `SUBNET_PREFIX_V4` | `24` | Длина IPv4-префикса (8..32). 24 — стандарт; 16 — для мобильной аудитории. При `SUBNET_GROUPING=true` |
| `ASN_GROUPING` | `false` | Считать уникальные ASN-провайдеры вместо IP/подсетей — самый точный сигнал против шаринга. IP без ASN — отдельная группа. Приоритетнее `SUBNET_GROUPING`. Требует базу MaxMind ASN |
| `ASN_DATABASE_PATH` | `./geoip/GeoLite2-ASN.mmdb` | Путь к `GeoLite2-ASN.mmdb`. Директория создаётся автоматически. Менять только при нестандартной раскладке |
| `MAXMIND_LICENSE_KEY` | — | Ключ MaxMind. Если задан — недостающая база скачивается при старте + фоновое обновление. [Получить](https://www.maxmind.com/en/geolite2/signup) |
| `MAXMIND_UPDATE_INTERVAL` | `168h` | Интервал автообновления базы (мин. `1h`). Только при заданном `MAXMIND_LICENSE_KEY` |
| `REDIS_URL` | `redis://redis:6379` | Адрес Redis |
| `TIMEZONE` | `UTC` | Часовой пояс для timestamps в алертах (напр. `Europe/Moscow`) |
| `LANGUAGE` | `ru` | Язык интерфейса: `ru` или `en` |
| `DAILY_REPORT` | `false` | Ежедневный отчёт о нарушениях в чат (топ нарушителей + счётчики). Переключается на лету через `/settings` |
| `DAILY_REPORT_TIME` | `09:00` | Локальное время отправки отчёта (`HH:MM`, в `TIMEZONE`). Меняется только перезапуском |
| `HEALTH_ADDR` | — | Адрес HTTP liveness-эндпоинта `/healthz` (напр. `:8080`). Пусто = выключен |

**ASN в уведомлениях.** Если база MaxMind доступна, рядом с каждым IP в алерте и webhook показывается провайдер (`• 91.107.96.11 - Hetzner Online GmbH (Chicago-1)`), а в заголовке — счётчик уникальных ASN (`Обнаружено: 4 IP (3 ASN)`). На логику ограничения не влияет — решения только по количеству IP/подсетей/ASN.

## Логика лимитов

| `hwidDeviceLimit` | Поведение |
|:-:|----------|
| `> 0` | Используется как лимит устройств |
| `null` | Берётся `DEFAULT_DEVICE_LIMIT` из конфига |
| `0` | Без ограничений — пользователь пропускается |

## Порог нарушений

При `VIOLATION_THRESHOLD=1` (по умолчанию) реакция на каждое превышение. При большем значении действие выполняется только после накопления N нарушений в окне `VIOLATION_THRESHOLD_WINDOW`: превышение → проверка cooldown → инкремент счётчика (TTL = окно) → при достижении порога действие и сброс счётчика. Если между нарушениями проходит больше окна — счётчик обнуляется.

Пример при `VIOLATION_THRESHOLD=3`, `VIOLATION_THRESHOLD_WINDOW=3600`:

| Время | Счётчик | Действие |
|:---:|:---:|----------|
| 12:00 | 1/3 | Лог, без действия |
| 12:05 | 2/3 | Лог, без действия |
| 12:10 | 3/3 | Алерт/блокировка, сброс |
| 12:15 | 1/3 | Лог, без действия |

## Telegram-бот

### Команды

Доступны только администраторам из `TELEGRAM_ADMIN_IDS`. Регистрируются в меню бота автоматически при старте.

| Команда | Что делает |
|---------|------------|
| `/settings` | Интерактивное меню рантайм-настроек. Меняет безопасные параметры на лету (без перезапуска); значения сохраняются в Redis и переживают рестарт. Подробнее — раздел «Рантайм-настройки» ниже |
| `/stats` | Статистика нарушений: количество за последние 24 часа и за неделю + топ-5 нарушителей за неделю по числу нарушений |

### Рантайм-настройки (`/settings`)

Приоритет источников: **Redis-override > `.env` / переменные окружения > значения по умолчанию**. Изменения применяются на лету (например, `CHECK_INTERVAL` пересоздаёт тикер). Структурные и секретные ключи (API URL/токен, все `TELEGRAM_*`, `REDIS_URL`, `TIMEZONE`, `LANGUAGE`, `WEBHOOK_*`, `IP_WHITELIST`, `IGNORED_NODE_UUIDS`, `MAXMIND_*`, `DAILY_REPORT_TIME`, `HEALTH_ADDR`) меняются **только** через `.env` + перезапуск. Кнопка «Сбросить к .env» (для ключа или для всех) убирает override.

### Ежедневный отчёт (`DAILY_REPORT=true`)

Раз в сутки в `DAILY_REPORT_TIME` (локальное время в `TIMEZONE`) бот присылает в чат тот же отчёт, что и `/stats` (нарушений за 24ч/неделю + топ-5). Учитываются только реальные срабатывания после порога `VIOLATION_THRESHOLD`; «мягкие» предупреждения в статистику не попадают. `DAILY_REPORT` переключается на лету через `/settings`, время `DAILY_REPORT_TIME` — только перезапуском.

### Ручной режим (`ACTION_MODE=manual`)

При превышении бот отправляет алерт с кнопками:

```
⚠️ Превышение лимита устройств

👤 Пользователь: username123
📊 Лимит: 3 | Обнаружено: 5 IP
📈 Нарушений за 24ч: 3
🕐 2025-11-29 04:15:30 (Europe/Moscow)

📍 IP-адреса:
  • 10.0.1.10 (нода: node-1)
  • 10.0.3.30 (нода: node-2)

🔗 Профиль

[🔄 Сбросить подключения] [🔒 Отключить навсегда]
[🔒 Отключить на 10 мин]        ← если AUTO_DISABLE_DURATION > 0
[🔇 Игнорировать (15 мин)]      ← если IGNORE_DURATION > 0, иначе "🔇 Игнорировать"
```

| Кнопка | Действие |
|--------|----------|
| Сбросить подключения | Сброс активных подключений через API |
| Отключить навсегда | Перманентная деактивация подписки |
| Отключить на N мин | Временная деактивация с автовосстановлением (при `AUTO_DISABLE_DURATION > 0`) |
| Игнорировать | В whitelist: временно при `IGNORE_DURATION > 0`, иначе навсегда |

### Автоматический режим (`ACTION_MODE=auto`)

Подписка отключается автоматически, бот шлёт информационный алерт с кнопкой «Включить подписку». При `AUTO_DISABLE_DURATION > 0` подписка восстанавливается по таймеру.

**Уведомления в пределах допуска (`AUTO_NOTIFY_SOFT=true`).** Когда нужно банить только при заметном превышении (`TOLERANCE`), но знать о тех, кто уже вышел за HWID-лимит:

| Кол-во устройств | Действие |
|------------------|----------|
| `устройств <= limit` | тишина |
| `limit < устройств <= limit+TOLERANCE` | 🔔 информационный алерт, **без бана** |
| `устройств > limit+TOLERANCE` | 🔒 бан + алерт |

«Мягкие» предупреждения используют отдельный кулдаун (`cooldown:soft:`), **не** увеличивают счётчик нарушений за 24ч, **не** учитываются в `VIOLATION_THRESHOLD` и шлют webhook с событием `soft_violation_detected`.

## Webhook

При нарушении limiter может отправить HTTP POST на `WEBHOOK_URL` (работает в обоих режимах). Если задан `WEBHOOK_SECRET`, к запросу добавляются два заголовка: `X-Webhook-Secret` (сам секрет, для простой сверки) и `X-Signature: sha256=<hex>` — HMAC-SHA256 от тела запроса с этим секретом (для проверки целостности и защиты от подмены). Отправка асинхронная (fire-and-forget) и не блокирует цикл мониторинга.

**Пример payload:**

```json
{
  "event": "violation_detected",
  "action_mode": "auto",
  "user": {
    "uuid": "550e8400-e29b-41d4-a716-446655440000",
    "user_id": "123",
    "username": "john",
    "email": "john@example.com",
    "telegram_id": 123456789,
    "subscription_url": "https://panel.example.com/sub/abc"
  },
  "violation": {
    "ips": [
      { "ip": "1.2.3.4", "node_name": "DE-1", "node_uuid": "node-uuid-1", "last_seen": "2025-11-29T12:00:00Z" },
      { "ip": "5.6.7.8", "node_name": "US-1", "node_uuid": "node-uuid-2", "last_seen": "2025-11-29T12:01:00Z" }
    ],
    "ip_count": 5,
    "device_limit": 3,
    "tolerance": 1,
    "effective_limit": 4,
    "violation_count_24h": 3
  },
  "action": { "auto_disable_duration_min": 10 },
  "timestamp": "2025-11-29T12:05:00Z"
}
```

| Поле | Описание |
|------|----------|
| `event` | `violation_detected` при бане; `soft_violation_detected` для «мягкого» предупреждения (`AUTO_NOTIFY_SOFT`) |
| `action_mode` | `manual` или `auto` |
| `user` | Данные пользователя (UUID, username, email, telegram_id, subscription_url) |
| `violation.ips` | Активные IP с нодой и временем последней активности |
| `violation.ip_count` | Количество уникальных IP |
| `violation.device_limit` / `tolerance` / `effective_limit` | Лимит, допуск и эффективный лимит (`device_limit + tolerance`) |
| `violation.violation_count_24h` | Нарушений за 24 часа |
| `action.auto_disable_duration_min` | Длительность блокировки (0 = перманентная) |
| `timestamp` | Время обнаружения (ISO 8601) |

## FAQ

**Как узнать Telegram Chat ID?** Добавьте [@userinfobot](https://t.me/userinfobot) и отправьте `/start`. Для группы/канала — [@getidsbot](https://t.me/getidsbot).

**Что если API панели недоступен?** Сервис логирует ошибку, пропускает цикл и пробует снова через `CHECK_INTERVAL`. Запросы к API повторяются до 3 раз с exponential backoff.

**Можно ли использовать Redis от Remnawave?** Можно, но не рекомендуется — проект поднимает свой Redis. Для существующего укажите `REDIS_URL`.

**Как изменить лимит пользователя?** Через поле `hwidDeviceLimit` в настройках подписки в панели Remnawave.

## Поддержка и лицензия

[GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues) · GNU GPL v3.0 — см. [LICENSE](LICENSE)
