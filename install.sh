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
findtime = 300
bantime = ${bantime}m
EOF

    cat > /etc/fail2ban/filter.d/remnawave-limiter.conf << EOF
[Definition]
datepattern = ^%%Y/%%m/%%d %%H:%%M:%%S
failregex = \[LIMIT_IP\]\s+Email\s+=\s+\S+\s+\|\|\s+SRC\s+=\s+<ADDR>
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
            echo "\$(date +\"%%Y/%%m/%%d %%H:%%M:%%S\") BAN [Email] = <F-USER> [IP] = <ip> banned for <bantime> seconds." >> ${BANNED_LOG}

actionunban = <iptables> -D f2b-<name> -s <ip> -j <blocktype>
              echo "\$(date +\"%%Y/%%m/%%d %%H:%%M:%%S\") UNBAN [Email] = <F-USER> [IP] = <ip> unbanned." >> ${BANNED_LOG}

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

print_info "Время бана в минутах (30 = 30 минут, 1440 = 1 день)"
BAN_TIME=$(ask_with_default "Время бана (минуты)" "30")

CHECK_INTERVAL=$(ask_with_default "Интервал проверки лога (секунды)" "5")

print_info "Интервал очистки лога (truncate)"
print_info "После очистки лог начинается заново (рекомендуется 3600 секунд = 1 час)"
LOG_CLEAR_INTERVAL=$(ask_with_default "Интервал очистки лога (секунды)" "3600")

echo ""
print_info "Дополнительные параметры (webhook уведомления):"
echo ""

print_info "Название сервера (будет отображаться в уведомлениях)"
SERVER_NAME=$(ask_with_default "Название сервера" "VPN Server")

print_info "URL webhook для уведомлений о блокировках (оставьте пустым для отключения)"
print_info "Формат JSON: {server, ban_duration_minutes, ip_masked, email, reason, timestamp}"
read -p "Webhook URL [none]: " WEBHOOK_URL
WEBHOOK_URL="${WEBHOOK_URL:-none}"

echo ""

print_info "Создание директории $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"

print_info "Создание конфигурационного файла..."

cat > "$ENV_FILE" << EOF
# Remnanode IP Limiter Configuration
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
SERVER_NAME=$SERVER_NAME
BAN_DURATION_MINUTES=$BAN_TIME
EOF

print_success "Конфигурация сохранена в $ENV_FILE"
echo ""

print_info "Проверка зависимостей..."
echo ""

if ! command -v python3 &> /dev/null; then
    print_warning "Python 3 не найден. Установка..."
    case "${RELEASE}" in
    ubuntu|debian)
        apt-get update && apt-get install -y python3
        ;;
    centos|rhel|almalinux|rocky)
        yum -y install python3
        ;;
    fedora)
        dnf -y install python3
        ;;
    arch|manjaro)
        pacman -Syu --noconfirm python
        ;;
    alpine)
        apk add python3
        ;;
    esac
fi

print_success "Python 3: $(python3 --version)"

print_info "Установка Python зависимостей (python-dotenv, requests)..."
echo ""

DEPS_INSTALLED=false

if command -v pip3 &> /dev/null; then
    print_info "Попытка установки через pip3 --break-system-packages..."
    if pip3 install --break-system-packages python-dotenv requests; then
        DEPS_INSTALLED=true
        print_success "Зависимости установлены через pip3 (--break-system-packages)"
    fi
fi

if [ "$DEPS_INSTALLED" = false ] && command -v pip3 &> /dev/null; then
    print_info "Попытка установки через pip3..."
    if pip3 install python-dotenv requests; then
        DEPS_INSTALLED=true
        print_success "Зависимости установлены через pip3"
    fi
fi

if [ "$DEPS_INSTALLED" = false ]; then
    print_warning "pip3 не смог установить зависимости, пробуем системный пакетный менеджер..."

    case "${RELEASE}" in
    ubuntu|debian)
        if apt-get install -y python3-dotenv python3-requests; then
            DEPS_INSTALLED=true
            print_success "Зависимости установлены через apt"
        fi
        ;;
    centos|rhel|almalinux|rocky)
        yum install -y epel-release 2>/dev/null || true
        if yum install -y python3-dotenv python3-requests; then
            DEPS_INSTALLED=true
            print_success "Зависимости установлены через yum"
        fi
        ;;
    fedora)
        if dnf install -y python3-dotenv python3-requests; then
            DEPS_INSTALLED=true
            print_success "Зависимости установлены через dnf"
        fi
        ;;
    arch|manjaro)
        if pacman -S --noconfirm python-dotenv python-requests; then
            DEPS_INSTALLED=true
            print_success "Зависимости установлены через pacman"
        fi
        ;;
    alpine)
        if apk add py3-dotenv py3-requests; then
            DEPS_INSTALLED=true
            print_success "Зависимости установлены через apk"
        fi
        ;;
    *)
        print_warning "Неизвестный дистрибутив, пытаемся установить через pip3..."
        ;;
    esac
fi

print_info "Проверка установленных зависимостей..."
if python3 -c "import dotenv, requests" 2>/dev/null; then
    print_success "✅ Все зависимости корректно установлены и работают"
else
    print_error "❌ Не удалось импортировать python-dotenv или requests"
    print_error "Попробуйте установить вручную:"
    echo ""
    echo "    pip3 install python-dotenv requests"
    echo ""
    echo "Или через системный пакетный менеджер:"
    case "${RELEASE}" in
    ubuntu|debian)
        echo "    apt-get install python3-dotenv python3-requests"
        ;;
    centos|rhel|almalinux|rocky)
        echo "    yum install python3-dotenv python3-requests"
        ;;
    fedora)
        echo "    dnf install python3-dotenv python3-requests"
        ;;
    esac
    echo ""
    exit 1
fi

echo ""

print_info "Копирование файлов..."

if [ ! -f "$SCRIPT_DIR/remnawave-limiter.py" ]; then
    print_error "Файл remnawave-limiter.py не найден!"
    exit 1
fi

cp "$SCRIPT_DIR/remnawave-limiter.py" "$INSTALL_DIR/"
chmod +x "$INSTALL_DIR/remnawave-limiter.py"

if [ -f "$SCRIPT_DIR/limiter-cli.py" ]; then
    print_info "Установка CLI..."
    cp "$SCRIPT_DIR/limiter-cli.py" "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/limiter-cli.py"

    ln -sf "$INSTALL_DIR/limiter-cli.py" /usr/local/bin/limiter

    print_success "CLI установлен (команда: limiter)"
else
    print_warning "CLI скрипт не найден, пропускаем"
fi

print_success "Файлы скопированы"
echo ""

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
Description=Remnawave IP Limiter (fail2ban integration)
After=network.target fail2ban.service
Wants=fail2ban.service

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=/usr/bin/python3 $INSTALL_DIR/remnawave-limiter.py
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

if ask_yes_no "Запустить сервис сейчас?" "y"; then
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
echo "    limiter status                # Статус системы"
echo "    limiter violations            # Последние нарушения"
echo "    limiter banned                # Забаненные IP"
echo "    limiter unban 1.2.3.4         # Разбанить IP"
echo "    limiter unban-all             # Разбанить все"
echo "    limiter active                # Активные подключения"
echo "    limiter logs -f               # Следить за логами"
echo "    limiter clear                 # Очистить логи"
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
