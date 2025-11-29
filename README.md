# Remnawave Limiter

**Ограничение количества одновременных IP-адресов для Remnawave VPN с использованием fail2ban**

Скрипт мониторит логи Remnawave и автоматически блокирует IP-адреса при превышении лимита одновременных подключений с одного ключа на определённой ноде. Особенно полезно, если вы замечаете, что ваши пользователи делятся VLESS ключами с другими людьми несмотря на ограничение HWID.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 📋 Содержание

- [Возможности](#-возможности)
- [Как это работает](#-как-это-работает)
- [Требования](#-требования)
- [Установка](#-установка)
- [Конфигурация](#%EF%B8%8F-конфигурация)
- [Использование CLI](#-использование-cli)
- [Webhook уведомления](#-webhook-уведомления)
- [Whitelist email](#-whitelist-email)
- [Troubleshooting](#-troubleshooting)
- [FAQ](#-faq)

## ✨ Возможности

- ✅ **Fail2ban интеграция** - проверенная система банов с автоматическим разбаном
- ✅ **Толерантность к переключению сети** - не банит при смене LTE↔Wi-Fi или переключении вышек
- ✅ **CLI управление** - удобные команды для мониторинга и управления
- ✅ **Webhook переменные и headers** - возможность настроить payload и заголовки для авторизации
- ✅ **Whitelist email** - исключение определённых пользователей из проверки лимитов

## 🔍 Как это работает

### Основная логика

1. **Мониторинг логов** - читает access лог Remnawave каждые 5 секунд
2. **Сбор уникальных IP** - для каждого email собирает список уникальных IP-адресов
3. **Проверка одновременности** - IP считается активным если был < 60 секунд назад
4. **Детекция нарушений** - если одновременно активных IP > лимита → логирует нарушение
5. **Fail2ban обработка** - После 3 нарушений в течение 5 минут → блокировка IP

### Защита от ложных срабатываний

- **Дедупликация** - одно нарушение не логируется чаще раза в минуту
- **Fail2ban tolerance** - нужно 3 нарушения за 5 минут для бана
- **Grace period** - 60 секунд на завершение переключения сети
- **Timestamp-based detection** - проверка реальной активности, а не просто наличия в логе

## 📦 Требования

- **ОС**: Ubuntu 20.04+, Debian 10+, CentOS 7+, Fedora, Arch, Alpine
- **Go**: 1.21+ (устанавливается автоматически)
- **Fail2ban**: устанавливается автоматически
- **Remnanode**: нода с включенным access логом
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
| Интервал очистки лога (сек) | `3600` | Частота truncate лога |
| Webhook URL | `none` | URL для уведомлений (опционально) |
| Webhook Template | пусто | Шаблон тела запроса (обязателен если указан URL) |
| Webhook Headers | пусто | Заголовки HTTP (опционально) |
| Whitelist emails | `none` | Email для исключения из проверки |

### Проверка установки

```bash
systemctl status remnawave-limiter
systemctl status fail2ban
limiter-cli status
```

## ⚙️ Конфигурация

### Файл конфигурации

Файл: `/opt/remnawave-limiter/.env`

```bash
# Путь к логу Remnawave
REMNAWAVE_LOG_PATH=/var/log/remnanode/access.log

# Путь к логу нарушений (для fail2ban)
VIOLATION_LOG_PATH=/var/log/remnawave-limiter/access-limiter.log

# Максимальное количество IP-адресов на один ключ
MAX_IPS_PER_KEY=1

# Интервал проверки лога в секундах
CHECK_INTERVAL=5

# Интервал очистки лога в секундах
LOG_CLEAR_INTERVAL=3600

# Webhook уведомления (template обязателен если указан URL)
WEBHOOK_URL=https://your-domain.com/api/webhook
WEBHOOK_TEMPLATE={"username":"%email","ip":"%ip","server":"%server","action":"%action","duration":%duration,"timestamp":"%timestamp"}
WEBHOOK_HEADERS=Authorization:Bearer your-token,Content-Type:application/json
BAN_DURATION_MINUTES=10

# Whitelist email (через запятую)
WHITELIST_EMAILS=root,admin,vomao039fa3
```

### Применение изменений

```bash
sudo systemctl restart remnawave-limiter
```

## 🖥️ Использование CLI

### Быстрый старт

```bash
limiter-cli status          # Статус системы
limiter-cli violations      # Последние нарушения
limiter-cli banned          # Забаненные IP
limiter-cli unban 1.2.3.4   # Разбанить IP
limiter-cli active          # Активные подключения
```

### Все команды

#### `limiter-cli status`
Показывает статус сервисов и fail2ban jail

#### `limiter-cli violations [-n 20]`
Показывает последние нарушения

#### `limiter-cli banned`
Список забаненных IP

#### `limiter-cli unban <ip>`
Разбанить конкретный IP

#### `limiter-cli unban-all`
Разбанить все IP

#### `limiter-cli active`
Показывает текущие подключения из лога

#### `limiter-cli logs [-f] [-n 50]`
Просмотр логов сервиса

#### `limiter-cli clear`
Очистить все логи (требует подтверждения)

## 📡 Webhook уведомления

### Настройка шаблона

Webhook требует обязательной настройки шаблона. Доступны следующие переменные:

| Переменная | Описание | Пример |
|-----------|----------|---------|
| `%email` | Subscription ID (идентификатор подписки) | vim6g9a50a |
| `%ip` | IP адрес | 1.2.3.4 |
| `%server` | Hostname сервера | vpn-node-01 |
| `%action` | Действие (ban/unban) | ban |
| `%duration` | Длительность бана (минуты) | 30 |
| `%timestamp` | ISO 8601 timestamp | 2025-11-29T12:00:00Z |

### Пример конфигурации

```bash
WEBHOOK_URL=https://discord.com/api/webhooks/xxx
WEBHOOK_TEMPLATE={"content":"🚫 Ban: %email from %ip on %server for %duration min"}
WEBHOOK_HEADERS=Content-Type:application/json
```

### Пример с авторизацией

```bash
WEBHOOK_URL=https://api.example.com/notifications
WEBHOOK_TEMPLATE={"user":"%email","ip":"%ip","action":"%action","timestamp":"%timestamp"}
WEBHOOK_HEADERS=Authorization:Bearer token123,Content-Type:application/json,X-Server-Name:VPN-01
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

- подписки из whitelist **полностью игнорируются** при проверке лимитов
- можно указать неограниченное количество подписок
- Разделитель - запятая
- подписки чувствительны к регистру
- изменения применяются после перезапуска сервиса

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
# Измените bantime = 30m на нужное (m=минуты, h=часы, d=дни)
sudo systemctl restart fail2ban
```

### Как добавить email в whitelist после установки?

```bash
sudo nano /opt/remnawave-limiter/.env
# Измените WHITELIST_EMAILS=email1,email2,email3
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
sudo rm /usr/local/bin/limiter-cli

sudo systemctl daemon-reload
sudo systemctl restart fail2ban

# Удалить логи (опционально)
sudo rm -rf /var/log/remnawave-limiter
```

## 💬 Поддержка

- **Issues**: [GitHub Issues](https://github.com/ResetVPN/remnawave-limiter/issues)

## 📝 Лицензия

MIT License - см. [LICENSE](LICENSE)
