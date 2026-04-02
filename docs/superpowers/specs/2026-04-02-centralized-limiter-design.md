# Centralized Remnawave Limiter — Design Spec

## Overview

Переписать remnawave-limiter из per-node системы (парсинг логов + fail2ban) в централизованный сервис, который через Remnawave API мониторит IP-подключения пользователей со всех нод и реагирует на превышение лимита устройств.

Один инстанс, деплой через Docker Compose (Go-бинарник + Redis).

## Architecture

```
docker-compose.yml
├── remnawave-limiter  (Go binary)
└── redis              (valkey/valkey:8.1-alpine, свой инстанс на порту 6379 внутри сети)
```

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

Fail2ban, парсинг логов, per-node установка — убираются полностью.
CLI (`limiter-cli`) убирается.

## Configuration

Всё через `.env` файл (или переменные окружения в docker-compose).

### API подключение
- `REMNAWAVE_API_URL` — адрес панели (например `https://panel.example.com`)
- `REMNAWAVE_API_TOKEN` — токен авторизации

### Мониторинг
- `CHECK_INTERVAL` — интервал проверки (секунды, default: 30)
- `ACTIVE_IP_WINDOW` — окно активности IP по `lastSeen` (секунды, default: 300)
- `TOLERANCE` — погрешность сверх лимита (default: 0). Если лимит 3 и tolerance 1, реакция при 5+ IP
- `COOLDOWN` — кулдаун между алертами на одного пользователя (секунды, default: 300)
- `USER_CACHE_TTL` — время жизни кэша данных пользователя в Redis (секунды, default: 600)
- `DEFAULT_DEVICE_LIMIT` — fallback лимит для пользователей с `hwidDeviceLimit = null` (default: 0, 0 = не ограничивать)

### Режим реакции
- `ACTION_MODE` — `manual` или `auto` (default: manual)
- `AUTO_DISABLE_DURATION` — длительность отключения подписки в минутах (0 = перманентно). Только для `auto`.

### Telegram
- `TELEGRAM_BOT_TOKEN` — токен бота
- `TELEGRAM_CHAT_ID` — ID чата/канала/группы
- `TELEGRAM_THREAD_ID` — ID треда (опционально, для топиков в группе)
- `TELEGRAM_ADMIN_IDS` — ID админов, которым разрешено нажимать кнопки (через запятую)

### Whitelist
- `WHITELIST_USER_IDS` — список userId, исключённых из проверки (через запятую)

### Redis
- `REDIS_URL` — адрес Redis (default: `redis://redis:6379`)

### Timezone
- `TIMEZONE` — часовой пояс для отображения в алертах (default: `UTC`)

## Project Structure

```
remnawave-limiter/
├── cmd/limiter/main.go              → Точка входа
├── internal/
│   ├── config/config.go             → Загрузка .env, структура Config
│   ├── api/client.go                → HTTP-клиент к Remnawave API
│   ├── api/types.go                 → Типы ответов API
│   ├── monitor/monitor.go           → Основной цикл мониторинга
│   ├── cache/cache.go               → Redis-обёртка
│   ├── telegram/bot.go              → Telegram-бот
│   ├── telegram/messages.go         → Форматирование сообщений
│   └── version/version.go           → Версия
├── docker-compose.yml
├── Dockerfile
├── .env.example
├── Makefile
└── go.mod
```

## Remnawave API Endpoints Used

| Endpoint | Назначение |
|----------|-----------|
| `GET /api/nodes` | Список всех нод (фильтр: isConnected=true, isDisabled=false) |
| `POST /api/ip-control/fetch-users-ips/{nodeUuid}` | Запрос IP пользователей на ноде → jobId |
| `GET /api/ip-control/fetch-users-ips/result/{jobId}` | Результат: [{userId, [{ip, lastSeen}]}] |
| `GET /api/users/by-id/{id}` | Данные пользователя: uuid, hwidDeviceLimit, telegramId, email, username |
| `POST /api/ip-control/drop-connections` | Сброс подключений пользователя |
| `POST /api/users/{uuid}/actions/disable` | Отключение подписки |
| `POST /api/users/{uuid}/actions/enable` | Включение подписки |

## Data Flow

```
Каждые CHECK_INTERVAL секунд:

1. GET /api/nodes
   └→ фильтр: isConnected=true, isDisabled=false
   └→ список nodeUUIDs

2. Для каждой ноды (параллельно):
   ├→ POST /api/ip-control/fetch-users-ips/{nodeUuid} → jobId
   └→ Poll GET /api/ip-control/fetch-users-ips/result/{jobId}
      └→ {userId, [{ip, lastSeen}]}

3. Агрегация по userId со всех нод:
   userId=1234 → [
     {ip: "1.2.3.4", lastSeen: ..., node: "yandex-1"},
     {ip: "5.6.7.8", lastSeen: ..., node: "germany-2"},
   ]

4. Фильтр: оставить только IP с lastSeen < ACTIVE_IP_WINDOW

5. Для каждого userId с activeIPs > 0:
   ├→ Проверить whitelist в Redis → пропустить если есть
   ├→ Получить userData из Redis-кэша (или GET /api/users/by-id/{id})
   ├→ limit = userData.hwidDeviceLimit (0 = пропустить, null = DEFAULT_DEVICE_LIMIT)
   └→ Если len(activeIPs) > limit + TOLERANCE:
      ├→ Проверить кулдаун в Redis → пропустить если не истёк
      ├→ ACTION_MODE = manual:
      │    └→ Telegram алерт с кнопками
      └→ ACTION_MODE = auto:
           ├→ POST /api/users/{uuid}/actions/disable
           ├→ Сохранить таймер восстановления в Redis (если не перманент)
           └→ Telegram информационный алерт
```

## Telegram Bot

### Алерт (ручной режим)

```
⚠️ Превышение лимита устройств

👤 Пользователь: username123
🔑 Подписка: abc123@example.vpn
📊 Лимит: 3 | Обнаружено: 5 IP
🕐 2026-04-02 04:15:30 (Europe/Moscow)

📍 IP-адреса:
  • 178.66.157.246 (нода: yandex-1)
  • 90.188.59.122 (нода: yandex-1)
  • 46.250.75.216 (нода: germany-2)
  • 109.225.4.195 (нода: germany-2)
  • 80.253.12.114 (нода: finland-1)

🔗 Профиль: ссылка на панель

[Сбросить подключения] [Отключить подписку] [Игнорировать]
```

### Алерт (автоматический режим)

```
🔒 Подписка автоматически отключена

👤 Пользователь: username123
🔑 Подписка: abc123@example.vpn
📊 Лимит: 3 | Обнаружено: 5 IP
⏱ Отключена на: 30 мин (или "Перманентно")
🕐 2026-04-02 04:15:30 (Europe/Moscow)

📍 IP-адреса:
  • 178.66.157.246 (нода: yandex-1)
  ...

[Включить подписку]
```

### Callback-кнопки

| Кнопка | Действие |
|--------|----------|
| Сбросить подключения | `POST /api/ip-control/drop-connections {userUuids: [uuid]}` |
| Отключить подписку | `POST /api/users/{uuid}/actions/disable` |
| Игнорировать | `SADD whitelist userId` в Redis |
| Включить подписку | `POST /api/users/{uuid}/actions/enable` |

- Нажимать могут только админы из `TELEGRAM_ADMIN_IDS`
- После нажатия — сообщение редактируется, кнопки заменяются на статус действия

## Redis Keys

| Key | Type | TTL | Назначение |
|-----|------|-----|-----------|
| `user:{userId}` | Hash | USER_CACHE_TTL | Кэш: uuid, hwidDeviceLimit, telegramId, email, username |
| `cooldown:{userId}` | String | COOLDOWN | Timestamp последнего алерта |
| `whitelist` | Set | — | userId исключённых из проверки |
| `restore:{uuid}` | String | AUTO_DISABLE_DURATION | Таймер восстановления подписки (auto режим) |

## hwidDeviceLimit Logic

- `hwidDeviceLimit = 0` → пользователь без ограничений, пропускаем
- `hwidDeviceLimit = null` → используем `DEFAULT_DEVICE_LIMIT` из конфига
- `hwidDeviceLimit > 0` → используем как лимит
- Если `DEFAULT_DEVICE_LIMIT = 0` и `hwidDeviceLimit = null` → пропускаем (не ограничиваем)

## Error Handling

### API недоступен
- Retry с exponential backoff (3 попытки)
- Если все попытки провалились — логируем, пропускаем цикл
- Опционально: алерт в Telegram если API недоступен N циклов подряд

### Job polling
- Poll с интервалом 1с, максимум 30 попыток
- Если `isFailed=true` или таймаут — пропускаем ноду в этом цикле

### Redis недоступен
- Критическая зависимость — сервис не стартует без Redis
- При потере соединения в runtime — retry, логирование

### Гонка при нажатии кнопок
- Кнопка отрабатывает один раз, после нажатия сообщение редактируется (кнопки убираются)

## Restoration (auto mode)

При `AUTO_DISABLE_DURATION > 0`:
- В Redis ставится ключ `restore:{uuid}` с TTL = AUTO_DISABLE_DURATION * 60
- Отдельная горутина проверяет истекшие ключи через Redis keyspace notifications или периодический scan
- По истечении: `POST /api/users/{uuid}/actions/enable`

## What's Removed

- `cmd/limiter-cli/` — CLI убирается (управление через Telegram)
- `internal/parser/` — парсинг логов не нужен
- `internal/limiter/ban_watcher.go` — fail2ban не нужен
- `internal/limiter/webhook.go` — заменяется на Telegram
- `internal/limiter/limiter.go` — заменяется на monitor
- `pkg/logger/` — остаётся, но упрощается (только stdout для Docker)
- Все зависимости от fail2ban, systemd, локальных лог-файлов
