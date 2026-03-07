# Remnawave Limiter

**Ограничение количества одновременных IP-адресов для Remnawave**

Скрипт мониторит логи Remnawave и автоматически блокирует IP-адреса при превышении лимита одновременных подключений с одного ключа на определённой ноде. Особенно полезно, если вы замечаете, что ваши пользователи делятся VLESS ключами с другими людьми несмотря на ограничение HWID.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 📋 Содержание

- [Возможности](#-возможности)
- [Как это работает](#-как-это-работает)
- [Требования](#-требования)
- [Установка](#-установка)
- [Конфигурация](#%EF%B8%8F-конфигурация)
- [Использование CLI](#-использование-cli)
- [Архивирование логов](#-архивирование-логов)
- [Webhook уведомления](#-webhook-уведомления)
- [Whitelist](#%EF%B8%8F-whitelist)
- [Troubleshooting](#-troubleshooting)
- [FAQ](#-faq)

## ✨ Возможности

- ✅ **Fail2ban интеграция** — проверенная система банов с автоматическим разбаном
- ✅ **Толерантность к переключению сети** — не банит при смене LTE↔Wi-Fi или переключении вышек
- ✅ **CLI управление** — удобные команды для мониторинга и управления
- ✅ **Webhook уведомления** — настраиваемый payload и заголовки для авторизации
- ✅ **Whitelist** — исключение определённых подписок из проверки лимитов
- ✅ **Архивирование логов** — опциональное сохранение access логов перед очисткой + logrotate (хранение до 1 года)

## 🔍 Как это работает

### Основная логика

1. **Мониторинг логов** — читает access лог Remnawave каждые N секунд (по умолчанию 5)
2. **Сбор уникальных IP** — для каждой подписки собирает список уникальных IP-адресов
3. **Проверка одновременности** — IP считается активным, если был замечен < 60 секунд назад
4. **Детекция нарушений** — если одновременно активных IP > лимита → логирует нарушение
5. **Fail2ban обработка** — после 3 нарушений в течение 5 минут → блокировка IP
6. **Webhook** — при бане/разбане отправляет уведомление (если настроен)

### Защита от ложных срабатываний

- **Дедупликация** — одно нарушение IP+email не логируется чаще раза в 60 секунд
- **Fail2ban tolerance** — нужно 3 нарушения за 5 минут для бана
- **Grace period** — 60 секунд на завершение переключения сети
- **Порядок бана** — банятся самые новые IP, самые ранние сохраняются
- **Фильтрация localhost** — `127.0.0.1` и `::1` игнорируются

## 📦 Требования

- **ОС**: Ubuntu 20.04+, Debian 10+, CentOS 7+, Fedora, Arch, Alpine
- **Go**: 1.26+ (устанавливается автоматически)
- **Fail2ban**: устанавливается автоматически
- **Remnanode**: нода с включённым access логом
- **Root доступ**: для установки systemd сервисов

## 🚀 Установка

### Автоматическая установка (рекомендуется)

```bash
git clone https://github.com/syvlech/remnawave-limiter.git && cd remnawave-limiter && sudo bash install.sh
```

### Параметры при установке

Установщик запросит следующие параметры:

| Параметр | По умолчанию | Описание |
|----------|--------------|----------|
| Путь к логу Remnanode | `/var/log/remnanode/access.log` | Access лог Remnanode |
| Максимум IP на ключ | `1` | Лимит одновременных IP |
| Время бана (минуты) | `10` | Длительность блокировки |
| Интервал проверки (сек) | `5` | Частота мониторинга логов |
| Интервал очистки лога (сек) | `3600` | Частота truncate рабочего лога |
| Архивирование access лога | `нет` | Сохранение копии лога перед очисткой + logrotate |
| Webhook URL | `none` | URL для уведомлений (опционально) |
| Webhook Template | пусто | Шаблон тела запроса (обязателен если указан URL) |
| Webhook Headers | пусто | Заголовки HTTP (опционально) |
| Whitelist emails | `none` | Подписки для исключения из проверки |

### Проверка установки

```bash
systemctl status remnawave-limiter
systemctl status fail2ban
limiter-cli status
limiter-cli version
```

## ⚙️ Конфигурация

### Файл конфигурации

Файл: `/opt/remnawave-limiter/.env`

```bash
# Путь к логу Remnawave
REMNAWAVE_LOG_PATH=/var/log/remnanode/access.log

# Путь к логу нарушений (для fail2ban)
VIOLATION_LOG_PATH=/var/log/remnawave-limiter/access-limiter.log

# Архивирование access лога (true/false)
ENABLE_LOG_ARCHIVE=false

# Путь к архиву access лога (используется при ENABLE_LOG_ARCHIVE=true)
ACCESS_LOG_ARCHIVE_PATH=/var/log/remnawave-limiter/access-archive.log

# Максимальное количество IP-адресов на один ключ
MAX_IPS_PER_KEY=1

# Интервал проверки лога в секундах
CHECK_INTERVAL=5

# Интервал очистки рабочего лога в секундах
LOG_CLEAR_INTERVAL=3600

# Webhook уведомления (template обязателен если указан URL)
WEBHOOK_URL=https://your-domain.com/api/webhook
WEBHOOK_TEMPLATE={"username":"%email","ip":"%ip","server":"%server","action":"%action","duration":%duration,"timestamp":"%timestamp"}
WEBHOOK_HEADERS=Authorization:Bearer your-token,Content-Type:application/json

# Время бана в минутах
BAN_DURATION_MINUTES=10

# Whitelist подписок (через запятую)
WHITELIST_EMAILS=root,admin,vomao039fa3
```

### Применение изменений

```bash
sudo systemctl restart remnawave-limiter
```

## 🖥️ Использование CLI

```bash
limiter-cli status                    # Показать статус системы
limiter-cli violations                # Последние 20 нарушений
limiter-cli violations -n 50          # Последние 50 нарушений
limiter-cli banned                    # Список забаненных IP
limiter-cli unban 1.2.3.4             # Разбанить IP
limiter-cli unban-all                 # Разбанить все IP
limiter-cli active                    # Активные подключения
limiter-cli logs                      # Последние 50 строк логов
limiter-cli logs -f                   # Следить за логами (Ctrl+C для выхода)
limiter-cli clear                     # Очистить лог нарушений
limiter-cli version                   # Показать версию
```

## 📦 Архивирование логов

По умолчанию демон периодически очищает рабочий access лог (truncate). Если включено архивирование, перед каждой очисткой содержимое лога дописывается в архивный файл.

### Включение

В `.env`:

```bash
ENABLE_LOG_ARCHIVE=true
ACCESS_LOG_ARCHIVE_PATH=/var/log/remnawave-limiter/access-archive.log
```

### Logrotate

При установке с включённым архивированием автоматически создаётся конфигурация logrotate (`/etc/logrotate.d/remnawave-limiter`):

- **Ротация**: еженедельно
- **Хранение**: 52 недели (1 год)
- **Сжатие**: gzip (с отложенным сжатием)
- **Метод**: copytruncate

Ротированные файлы: `access-archive.log.1`, `access-archive.log.2.gz`, `access-archive.log.3.gz` и т.д.

### Без архивирования

Если `ENABLE_LOG_ARCHIVE=false` (по умолчанию), access лог просто очищается без сохранения — поведение как в предыдущих версиях.

## 📡 Webhook уведомления

### Настройка шаблона

Webhook отправляется при бане и разбане IP. Доступные переменные:

| Переменная | Описание | Пример |
|-----------|----------|---------|
| `%email` | ID подписки | user123 |
| `%ip` | IP адрес | 1.2.3.4 |
| `%server` | Hostname сервера | vpn-node-01 |
| `%action` | Действие | ban / unban |
| `%duration` | Длительность бана (минуты) | 10 |
| `%timestamp` | ISO 8601 timestamp | 2025-11-29T12:00:00Z |

Значения автоматически экранируются для JSON. Webhook отправляется асинхронно с 2 повторами при ошибке. События старше 60 секунд пропускаются.

### Пример: Discord

```bash
WEBHOOK_URL=https://discord.com/api/webhooks/xxx
WEBHOOK_TEMPLATE={"content":"Ban: %email from %ip on %server for %duration min"}
WEBHOOK_HEADERS=Content-Type:application/json
```

### Пример: API с авторизацией

```bash
WEBHOOK_URL=https://api.example.com/notifications
WEBHOOK_TEMPLATE={"user":"%email","ip":"%ip","action":"%action","timestamp":"%timestamp"}
WEBHOOK_HEADERS=Authorization:Bearer token123,Content-Type:application/json
```

## 🛡️ Whitelist

Whitelist позволяет исключить определённые подписки из проверки лимитов IP.

### Настройка

```bash
# В .env файле
WHITELIST_EMAILS=root,admin,vomao039fa3

# Перезапустите сервис
sudo systemctl restart remnawave-limiter
```

### Особенности

- Подписки из whitelist полностью игнорируются при проверке лимитов
- Разделитель — запятая
- Чувствительны к регистру
- Изменения применяются после перезапуска сервиса

## 🔧 Troubleshooting

### Сервис не запускается

```bash
systemctl status remnawave-limiter
journalctl -u remnawave-limiter -n 50 --no-pager
cat /opt/remnawave-limiter/.env
```

### Fail2ban не банит

```bash
systemctl status fail2ban
fail2ban-client status remnawave-limiter
fail2ban-regex /var/log/remnawave-limiter/access-limiter.log \
               /etc/fail2ban/filter.d/remnawave-limiter.conf
tail -100 /var/log/fail2ban.log | grep remnawave
```

### Нарушения не логируются

```bash
ls -la /var/log/remnanode/access.log
tail -5 /var/log/remnanode/access.log
journalctl -u remnawave-limiter -f
tail -20 /var/log/remnawave-limiter/access-limiter.log
```

### Webhook не работает

```bash
journalctl -u remnawave-limiter | grep -i webhook
cat /opt/remnawave-limiter/.env | grep WEBHOOK

# Тестовая отправка
curl -X POST https://your-domain.com/api/webhook \
  -H 'Content-Type: application/json' \
  -d '{"test":"message"}'
```

## ❓ FAQ

### Как изменить лимит IP на ключ?

```bash
sudo nano /opt/remnawave-limiter/.env
# Измените MAX_IPS_PER_KEY=1 на нужное значение
sudo systemctl restart remnawave-limiter
```

### Как изменить время бана?

```bash
sudo nano /etc/fail2ban/jail.d/remnawave-limiter.conf
# Измените bantime = 10m на нужное (m=минуты, h=часы, d=дни)
sudo systemctl restart fail2ban
```

### Как добавить подписку в whitelist после установки?

```bash
sudo nano /opt/remnawave-limiter/.env
# Добавьте в WHITELIST_EMAILS через запятую
sudo systemctl restart remnawave-limiter
```

### Как включить архивирование логов после установки?

```bash
sudo nano /opt/remnawave-limiter/.env
# Установите ENABLE_LOG_ARCHIVE=true

# Создайте файл архива
sudo touch /var/log/remnawave-limiter/access-archive.log

# Добавьте logrotate конфигурацию
sudo tee /etc/logrotate.d/remnawave-limiter << 'EOF'
/var/log/remnawave-limiter/access-archive.log {
    weekly
    rotate 52
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
}
EOF

sudo systemctl restart remnawave-limiter
```

### Как полностью удалить?

```bash
sudo systemctl stop remnawave-limiter
sudo systemctl disable remnawave-limiter

sudo rm -rf /opt/remnawave-limiter
sudo rm /etc/systemd/system/remnawave-limiter.service
sudo rm /etc/fail2ban/jail.d/remnawave-limiter.conf
sudo rm /etc/fail2ban/filter.d/remnawave-limiter.conf
sudo rm /etc/fail2ban/action.d/remnawave-limiter.conf
sudo rm /etc/logrotate.d/remnawave-limiter
sudo rm /usr/local/bin/limiter-cli

sudo systemctl daemon-reload
sudo systemctl restart fail2ban

# Удалить логи (опционально)
sudo rm -rf /var/log/remnawave-limiter
```

## 💬 Поддержка

- **Issues**: [GitHub Issues](https://github.com/syvlech/remnawave-limiter/issues)

## 📝 Лицензия

MIT License — см. [LICENSE](LICENSE)
