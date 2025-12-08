#!/bin/bash

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

clear
echo -e "${BLUE}"
echo "╔════════════════════════════════════════════════════════╗"
echo "║                                                        ║"
echo "║                  Remnawave IP Limiter                  ║"
echo "║      https://github.com/syvlech/remnawave-limiter      ║"
echo "║                                                        ║"
echo "╚════════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo ""

if [ "$EUID" -ne 0 ]; then
    print_error "Запустите скрипт с правами root (sudo)"
    exit 1
fi

print_success "Права root подтверждены"
echo ""

if [ -d "/usr/local/go/bin" ] && [[ ":$PATH:" != *":/usr/local/go/bin:"* ]]; then
    export PATH=$PATH:/usr/local/go/bin
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="/opt/remnawave-limiter"
ENV_FILE="$INSTALL_DIR/.env"
VIOLATION_LOG="/var/log/remnawave-limiter/access-limiter.log"
BANNED_LOG="/var/log/remnawave-limiter/banned.log"

detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
        VER=$VERSION_ID
    elif [ -f /etc/lsb-release ]; then
        . /etc/lsb-release
        OS=$DISTRIB_ID
        VER=$DISTRIB_RELEASE
    else
        OS=$(uname -s)
        VER=$(uname -r)
    fi
    echo "$OS"
}

RELEASE=$(detect_os)

print_info "Обнаружена ОС: $RELEASE"
echo ""

ask_with_default() {
    local prompt="$1"
    local default="$2"
    local value

    read -p "$prompt [$default]: " value
    echo "${value:-$default}"
}

ask_yes_no() {
    local prompt="$1"
    local default="$2"
    local answer

    while true; do
        read -p "$prompt (y/n) [$default]: " answer
        answer="${answer:-$default}"
        case "${answer,,}" in
            y|yes) return 0 ;;
            n|no) return 1 ;;
            *) echo "Пожалуйста, введите y или n" ;;
        esac
    done
}

install_go() {
    if command -v go &>/dev/null || [ -x "/usr/local/go/bin/go" ]; then
        if command -v go &>/dev/null; then
            GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
        else
            GO_VERSION=$(/usr/local/go/bin/go version | awk '{print $3}' | sed 's/go//')
            export PATH=$PATH:/usr/local/go/bin
        fi
        print_success "Go уже установлен: $GO_VERSION"
        return 0
    fi

    print_info "Go не обнаружен. Установка Go..."

    ARCH=$(uname -m)
    case $ARCH in
        x86_64)
            GO_ARCH="amd64"
            ;;
        aarch64|arm64)
            GO_ARCH="arm64"
            ;;
        armv7l|armv6l)
            GO_ARCH="armv6l"
            ;;
        *)
            print_error "Неподдерживаемая архитектура: $ARCH"
            return 1
            ;;
    esac

    GO_VERSION="1.21.6"
    GO_TAR="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TAR}"

    print_info "Скачивание Go ${GO_VERSION} для ${GO_ARCH}..."

    cd /tmp
    if ! wget -q --show-progress "$GO_URL"; then
        print_error "Не удалось скачать Go"
        return 1
    fi

    print_info "Установка Go..."
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$GO_TAR"
    rm "$GO_TAR"

    if ! grep -q "/usr/local/go/bin" /etc/profile; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    fi

    export PATH=$PATH:/usr/local/go/bin

    if command -v go &>/dev/null; then
        print_success "Go установлен успешно: $(go version)"
        return 0
    else
        print_error "Не удалось установить Go"
        return 1
    fi
}

install_fail2ban() {
    if ! command -v fail2ban-client &>/dev/null; then
        print_info "Fail2ban не установлен. Установка..."
        echo ""

        case "${RELEASE}" in
        ubuntu|debian)
            apt-get update
            apt-get install fail2ban -y
            ;;
        centos|rhel|almalinux|rocky)
            yum update -y && yum install epel-release -y
            yum -y install fail2ban
            ;;
        fedora)
            dnf -y update && dnf -y install fail2ban
            ;;
        arch|manjaro)
            pacman -Syu --noconfirm fail2ban
            ;;
        alpine)
            apk add fail2ban
            ;;
        *)
            print_error "Неподдерживаемая ОС. Установите fail2ban вручную."
            exit 1
            ;;
        esac

        if ! command -v fail2ban-client &>/dev/null; then
            print_error "Не удалось установить fail2ban"
            exit 1
        fi

        print_success "Fail2ban установлен успешно!"
        echo ""
    else
        print_success "Fail2ban уже установлен"
        echo ""
    fi
}

create_jail_config() {
    local bantime="${1:-30}"

    print_info "Создание конфигурации fail2ban jail..."

    sed -i 's/#allowipv6 = auto/allowipv6 = auto/g' /etc/fail2ban/fail2ban.conf 2>/dev/null || true

    cat > /etc/fail2ban/jail.d/remnawave-limiter.conf << EOF
[remnawave-limiter]
enabled = true
backend = auto
filter = remnawave-limiter
action = remnawave-limiter
logpath = ${VIOLATION_LOG}
maxretry = 3
findtime = 60
bantime = ${bantime}m
EOF

    cat > /etc/fail2ban/filter.d/remnawave-limiter.conf << EOF
[Definition]
datepattern = ^%%Y/%%m/%%d %%H:%%M:%%S
failregex = \[LIMIT_IP\]\s+Email\s+=\s+(?P<mlfid>\S+)\s+\|\|\s+SRC\s+=\s+<ADDR>
ignoreregex =
EOF

    cat > /etc/fail2ban/action.d/remnawave-limiter.conf << EOF
[INCLUDES]
before = iptables-allports.conf

[Definition]
actionstart = <iptables> -N f2b-<name>
              <iptables> -A f2b-<name> -j <returntype>
              <iptables> -I <chain> -p <protocol> -j f2b-<name>

actionstop = <iptables> -D <chain> -p <protocol> -j f2b-<name>
             <actionflush>
             <iptables> -X f2b-<name>

actioncheck = <iptables> -n -L <chain> | grep -q 'f2b-<name>[ \t]'

actionban = <iptables> -I f2b-<name> 1 -s <ip> -j <blocktype>
            echo "\$(date +\"%%Y/%%m/%%d %%H:%%M:%%S\") BAN [Email] = <F-MLFID> [IP] = <ip> banned for <bantime> seconds." >> ${BANNED_LOG}

actionunban = <iptables> -D f2b-<name> -s <ip> -j <blocktype>
              echo "\$(date +\"%%Y/%%m/%%d %%H:%%M:%%S\") UNBAN [Email] = <F-MLFID> [IP] = <ip> unbanned." >> ${BANNED_LOG}

[Init]
name = default
protocol = tcp
chain = INPUT
EOF

    print_success "Конфигурация fail2ban создана (bantime: ${bantime} минут)"
    echo ""
}

echo -e "${YELLOW}═══════════════════════════════════════════════════════${NC}"
echo -e "${YELLOW}          Настройка параметров${NC}"
echo -e "${YELLOW}═══════════════════════════════════════════════════════${NC}"
echo ""

print_info "Основные параметры:"
echo ""

REMNAWAVE_LOG=$(ask_with_default "Путь к логу Remnanode" "/var/log/remnanode/access.log")

MAX_IPS=$(ask_with_default "Максимум IP-адресов на один ключ" "1")

print_info "Время бана в минутах (10 = 10 минут, 1440 = 1 день)"
BAN_TIME=$(ask_with_default "Время бана (минуты)" "10")

CHECK_INTERVAL=$(ask_with_default "Интервал проверки лога (секунды)" "5")

print_info "Интервал очистки лога (truncate)"
print_info "После очистки лог начинается заново (рекомендуется 3600 секунд = 1 час)"
LOG_CLEAR_INTERVAL=$(ask_with_default "Интервал очистки лога (секунды)" "3600")

echo ""
print_info "Дополнительные параметры (webhook уведомления):"
echo ""

print_info "URL webhook для уведомлений о блокировках (оставьте пустым для отключения)"
read -p "Webhook URL [none]: " WEBHOOK_URL
WEBHOOK_URL="${WEBHOOK_URL:-none}"

if [ "$WEBHOOK_URL" != "none" ]; then
    echo ""
    print_info "Webhook Template (оставьте пустым, если webhook не отправляется)"
    print_info "Переменные: %email=subscription_id, %ip=IP, %server=hostname, %action=ban/unban, %duration=минуты, %timestamp=время"
    print_info "Пример: {\"username\":\"%email\",\"ip\":\"%ip\",\"server\":\"%server\",\"action\":\"%action\",\"duration\":%duration,\"timestamp\":\"%timestamp\"}"
    read -p "Template: " WEBHOOK_TEMPLATE

    echo ""
    print_info "Webhook Headers (через запятую, формат: Header1:Value1,Header2:Value2)"
    print_info "Пример: Authorization:Bearer token123,Content-Type:application/json"
    read -p "Headers: " WEBHOOK_HEADERS
fi

echo ""
print_info "Whitelist email (исключения из проверки лимитов)"
print_info "Название подписки пользователя(-ей) через запятую"
print_info "Пример: admin,root,via50m51"
read -p "Whitelist emails [none]: " WHITELIST_EMAILS
WHITELIST_EMAILS="${WHITELIST_EMAILS:-none}"

echo ""

print_info "Создание директории $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"

print_info "Создание конфигурационного файла..."

cat > "$ENV_FILE" << EOF
# Сгенерировано: $(date)

# Путь к access логу Remnanode
REMNAWAVE_LOG_PATH=$REMNAWAVE_LOG

# Путь к логу нарушений (для fail2ban)
VIOLATION_LOG_PATH=$VIOLATION_LOG

# Максимальное количество IP-адресов на один ключ
MAX_IPS_PER_KEY=$MAX_IPS

# Интервал проверки лога в секундах
CHECK_INTERVAL=$CHECK_INTERVAL

# Интервал очистки лога в секундах
LOG_CLEAR_INTERVAL=$LOG_CLEAR_INTERVAL

# Webhook уведомления
WEBHOOK_URL=$WEBHOOK_URL
WEBHOOK_TEMPLATE=$WEBHOOK_TEMPLATE
WEBHOOK_HEADERS=$WEBHOOK_HEADERS
BAN_DURATION_MINUTES=$BAN_TIME

# Whitelist email (исключения из проверки лимитов)
WHITELIST_EMAILS=$WHITELIST_EMAILS
EOF

print_success "Конфигурация сохранена в $ENV_FILE"
echo ""

print_info "Установка Go и зависимостей..."
echo ""

if ! install_go; then
    print_error "Не удалось установить Go"
    exit 1
fi

if ! command -v wget &>/dev/null; then
    print_info "Установка wget..."
    case "${RELEASE}" in
    ubuntu|debian)
        apt-get install -y wget
        ;;
    centos|rhel|almalinux|rocky)
        yum install -y wget
        ;;
    fedora)
        dnf install -y wget
        ;;
    arch|manjaro)
        pacman -S --noconfirm wget
        ;;
    alpine)
        apk add wget
        ;;
    esac
fi

print_info "Сборка приложения..."
cd "$SCRIPT_DIR"

if systemctl is-active --quiet remnawave-limiter 2>/dev/null; then
    print_info "Остановка сервиса для обновления бинарников..."
    systemctl stop remnawave-limiter
    sleep 2
fi

go mod download

print_info "Сборка remnawave-limiter..."
go build -ldflags="-s -w" -o "$INSTALL_DIR/remnawave-limiter" ./cmd/limiter

print_info "Сборка limiter-cli..."
go build -ldflags="-s -w" -o "$INSTALL_DIR/limiter-cli" ./cmd/limiter-cli

chmod +x "$INSTALL_DIR/remnawave-limiter"
chmod +x "$INSTALL_DIR/limiter-cli"

ln -sf "$INSTALL_DIR/limiter-cli" /usr/local/bin/limiter-cli

print_success "Приложение собрано и установлено"
echo ""

cp "$ENV_FILE" "$INSTALL_DIR/"

install_fail2ban

create_jail_config "$BAN_TIME"

print_info "Создание лог файлов..."
mkdir -p "$(dirname "$VIOLATION_LOG")"
touch "$VIOLATION_LOG"
touch "$BANNED_LOG"
print_success "Лог файлы созданы"
echo ""

print_info "Создание systemd service..."

cat > /etc/systemd/system/remnawave-limiter.service << EOF
[Unit]
Description=Remnawave IP Limiter
After=network.target fail2ban.service
Wants=fail2ban.service

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/remnawave-limiter
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

print_success "Systemd service создан"
echo ""

print_info "Перезагрузка systemd..."
systemctl daemon-reload
print_success "Systemd перезагружен"
echo ""

print_info "Запуск fail2ban..."
if [[ "$RELEASE" == "alpine" ]]; then
    rc-service fail2ban restart
    rc-update add fail2ban
else
    systemctl restart fail2ban
    systemctl enable fail2ban
fi

sleep 2

if systemctl is-active --quiet fail2ban 2>/dev/null || rc-service fail2ban status 2>/dev/null | grep -q "started"; then
    print_success "Fail2ban запущен"
else
    print_warning "Fail2ban может быть не запущен, проверьте вручную"
fi
echo ""

echo -e "${YELLOW}═══════════════════════════════════════════════════════${NC}"
echo -e "${YELLOW}          Запуск сервиса${NC}"
echo -e "${YELLOW}═══════════════════════════════════════════════════════${NC}"
echo ""

if systemctl is-active --quiet remnawave-limiter 2>/dev/null; then
    print_info "Сервис уже запущен, перезапускаем с новой версией..."
    systemctl restart remnawave-limiter

    sleep 2

    if systemctl is-active --quiet remnawave-limiter; then
        print_success "Сервис успешно перезапущен с новой версией!"
        echo ""
        print_info "Последние логи сервиса:"
        journalctl -u remnawave-limiter -n 10 --no-pager
    else
        print_error "Ошибка перезапуска сервиса."
        echo ""
        print_info "Последние 20 строк лога:"
        journalctl -u remnawave-limiter -n 20 --no-pager
    fi
elif ask_yes_no "Запустить сервис сейчас?" "y"; then
    print_info "Включение автозапуска..."
    systemctl enable remnawave-limiter

    print_info "Запуск сервиса..."
    systemctl start remnawave-limiter

    sleep 3

    if systemctl is-active --quiet remnawave-limiter; then
        print_success "Сервис успешно запущен!"
        echo ""
        print_info "Логи сервиса:"
        journalctl -u remnawave-limiter -n 10 --no-pager
    else
        print_error "Ошибка запуска сервиса."
        echo ""
        print_info "Последние 20 строк лога:"
        journalctl -u remnawave-limiter -n 20 --no-pager
    fi
else
    print_info "Для запуска позже: systemctl start remnawave-limiter"
    print_info "Для перезапуска: systemctl restart remnawave-limiter"
fi

echo ""
echo -e "${GREEN}╔════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║                                                        ║${NC}"
echo -e "${GREEN}║            ✅ Установка завершена успешно!             ║${NC}"
echo -e "${GREEN}║                                                        ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${BLUE}Управление через CLI (рекомендуется):${NC}"
echo ""
echo "    limiter-cli status                # Статус системы"
echo "    limiter-cli violations            # Последние нарушения"
echo "    limiter-cli banned                # Забаненные IP"
echo "    limiter-cli unban 1.2.3.4         # Разбанить IP"
echo "    limiter-cli unban-all             # Разбанить все"
echo "    limiter-cli active                # Активные подключения"
echo "    limiter-cli logs -f               # Следить за логами"
echo "    limiter-cli clear                 # Очистить логи"
echo ""
echo -e "${BLUE}Прямое управление fail2ban:${NC}"
echo ""
echo "    fail2ban-client status remnawave-limiter"
echo "    fail2ban-client set remnawave-limiter unbanip 1.2.3.4"
echo ""
echo -e "${BLUE}Просмотр логов:${NC}"
echo ""
echo "    tail -f /var/log/remnawave-limiter/limiter.log         # Лог скрипта"
echo "    tail -f /var/log/remnawave-limiter/access-limiter.log  # Лог нарушений"
echo "    tail -f /var/log/fail2ban.log | grep remnawave         # Лог fail2ban"
echo ""

print_info "Статус fail2ban jail:"
fail2ban-client status remnawave-limiter 2>/dev/null || print_warning "Jail еще не активирован (будет активирован при первом нарушении)"
echo ""

print_success "Установка завершена!"
echo ""
