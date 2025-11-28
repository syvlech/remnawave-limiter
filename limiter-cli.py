#!/usr/bin/env python3

import argparse
import subprocess
import sys
from pathlib import Path
from datetime import datetime
from typing import List, Dict
from collections import defaultdict
import re

class Colors:
    """ANSI цвета для вывода"""
    RED = '\033[0;31m'
    GREEN = '\033[0;32m'
    YELLOW = '\033[1;33m'
    BLUE = '\033[0;34m'
    MAGENTA = '\033[0;35m'
    CYAN = '\033[0;36m'
    NC = '\033[0m'

class LimiterCLI:
    def __init__(self):
        self.jail_name = 'remnawave-limiter'
        self.violation_log = '/var/log/remnawave-limiter/access-limiter.log'
        self.remnawave_log = '/var/log/remnanode/access.log'
        self.log_pattern = re.compile(r'from\s+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}):\d+\s+accepted.*?email:\s*(\S+)')

    def _run_command(self, cmd: List[str], check=True) -> tuple:
        """Выполняет команду и возвращает (success, output)"""
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, check=check)
            return True, result.stdout
        except subprocess.CalledProcessError as e:
            return False, e.stderr
        except Exception as e:
            return False, str(e)

    def status(self):
        """Показывает статус fail2ban jail и сервиса"""
        print(f"{Colors.BLUE}╔════════════════════════════════════════════════════════╗{Colors.NC}")
        print(f"{Colors.BLUE}║         Remnawave IP Limiter - Статус системы          ║{Colors.NC}")
        print(f"{Colors.BLUE}╚════════════════════════════════════════════════════════╝{Colors.NC}\n")

        success, output = self._run_command(['systemctl', 'is-active', 'remnawave-limiter'], check=False)
        service_status = output.strip()

        status_color = Colors.GREEN if service_status == 'active' else Colors.RED
        print(f"📊 Сервис remnawave-limiter: {status_color}{service_status}{Colors.NC}")

        success, output = self._run_command(['systemctl', 'is-active', 'fail2ban'], check=False)
        f2b_status = output.strip()

        f2b_color = Colors.GREEN if f2b_status == 'active' else Colors.RED
        print(f"🔒 Fail2ban: {f2b_color}{f2b_status}{Colors.NC}\n")

        success, output = self._run_command(['fail2ban-client', 'status', self.jail_name], check=False)
        if success:
            print(f"{Colors.CYAN}Статус jail '{self.jail_name}':{Colors.NC}")
            print(output)
        else:
            print(f"{Colors.RED}❌ Не удалось получить статус jail{Colors.NC}")

    def list_banned(self):
        """Показывает список забаненных IP"""
        success, output = self._run_command(['fail2ban-client', 'status', self.jail_name], check=False)

        if not success:
            print(f"{Colors.RED}❌ Ошибка получения списка банов{Colors.NC}")
            return

        for line in output.split('\n'):
            if 'Banned IP list' in line:
                ips = line.split(':')[1].strip()
                if not ips:
                    print(f"{Colors.GREEN}✅ Нет забаненных IP{Colors.NC}")
                else:
                    print(f"{Colors.YELLOW}🚫 Забаненные IP:{Colors.NC}")
                    for ip in ips.split():
                        print(f"   • {Colors.RED}{ip}{Colors.NC}")
                return

        print(f"{Colors.GREEN}✅ Нет забаненных IP{Colors.NC}")

    def unban(self, ip: str):
        """Разбанивает IP адрес"""
        print(f"🔓 Разбан {Colors.CYAN}{ip}{Colors.NC}...")

        success, output = self._run_command(['fail2ban-client', 'set', self.jail_name, 'unbanip', ip], check=False)

        if success:
            print(f"{Colors.GREEN}✅ IP {ip} разбанен{Colors.NC}")
        else:
            print(f"{Colors.RED}❌ Ошибка разбана: {output}{Colors.NC}")

    def unban_all(self):
        """Разбанивает все IP"""
        success, output = self._run_command(['fail2ban-client', 'status', self.jail_name], check=False)
        if not success:
            print(f"{Colors.RED}❌ Ошибка получения списка банов{Colors.NC}")
            return

        banned_ips = []
        for line in output.split('\n'):
            if 'Banned IP list' in line:
                ips = line.split(':')[1].strip()
                if ips:
                    banned_ips = ips.split()
                break

        if not banned_ips:
            print(f"{Colors.GREEN}✅ Нет забаненных IP{Colors.NC}")
            return

        print(f"🔓 Разбан {len(banned_ips)} IP адресов...")

        for ip in banned_ips:
            self._run_command(['fail2ban-client', 'set', self.jail_name, 'unbanip', ip], check=False)
            print(f"  ✓ {ip}")

        print(f"{Colors.GREEN}✅ Все IP разбанены{Colors.NC}")

    def violations(self, tail: int = 20):
        """Показывает последние нарушения"""
        log_path = Path(self.violation_log)

        if not log_path.exists():
            print(f"{Colors.YELLOW}⚠️  Лог нарушений пуст{Colors.NC}")
            return

        print(f"{Colors.BLUE}📋 Последние {tail} нарушений:{Colors.NC}\n")

        success, output = self._run_command(['tail', f'-{tail}', str(log_path)], check=False)

        if success and output:
            for line in output.strip().split('\n'):
                match = re.search(r'(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}).*Email = (\S+).*SRC = (\S+)', line)
                if match:
                    timestamp, email, ip = match.groups()
                    print(f"{Colors.YELLOW}{timestamp}{Colors.NC} │ {Colors.CYAN}{email}{Colors.NC} │ {Colors.RED}{ip}{Colors.NC}")
                else:
                    print(line)
        else:
            print(f"{Colors.YELLOW}⚠️  Нет нарушений{Colors.NC}")

    def active_connections(self):
        """Показывает активные подключения по email"""
        log_path = Path(self.remnawave_log)

        if not log_path.exists():
            print(f"{Colors.RED}❌ Лог Remnawave не найден: {log_path}{Colors.NC}")
            return

        email_ips: Dict[str, set] = defaultdict(set)

        try:
            with open(log_path, 'r', encoding='utf-8', errors='ignore') as f:
                for line in f:
                    match = self.log_pattern.search(line)
                    if match:
                        ip, email = match.groups()
                        email_ips[email].add(ip)
        except Exception as e:
            print(f"{Colors.RED}❌ Ошибка чтения лога: {e}{Colors.NC}")
            return

        if not email_ips:
            print(f"{Colors.YELLOW}⚠️  Нет активных подключений в логе{Colors.NC}")
            return

        print(f"{Colors.BLUE}╔════════════════════════════════════════════════════════╗{Colors.NC}")
        print(f"{Colors.BLUE}║            Активные подключения (текущий лог)          ║{Colors.NC}")
        print(f"{Colors.BLUE}╚════════════════════════════════════════════════════════╝{Colors.NC}\n")

        for email, ips in sorted(email_ips.items(), key=lambda x: len(x[1]), reverse=True):
            ip_count = len(ips)
            color = Colors.RED if ip_count > 1 else Colors.GREEN

            print(f"{color}📧 {email} ({ip_count} IP){Colors.NC}")
            for ip in sorted(ips):
                print(f"   └─ {Colors.CYAN}{ip}{Colors.NC}")
            print()

    def clear_logs(self):
        """Очищает логи (требует подтверждения)"""
        print(f"{Colors.YELLOW}⚠️  Это удалит все логи нарушений и Remnawave access log!{Colors.NC}")
        response = input("Продолжить? (yes/no): ")

        if response.lower() != 'yes':
            print(f"{Colors.BLUE}Отменено{Colors.NC}")
            return

        try:
            Path(self.violation_log).write_text('')
            print(f"{Colors.GREEN}✅ Лог нарушений очищен{Colors.NC}")
        except Exception as e:
            print(f"{Colors.RED}❌ Ошибка очистки лога нарушений: {e}{Colors.NC}")

        try:
            Path(self.remnawave_log).write_text('')
            print(f"{Colors.GREEN}✅ Access лог очищен{Colors.NC}")
        except Exception as e:
            print(f"{Colors.RED}❌ Ошибка очистки access лога: {e}{Colors.NC}")

        print(f"\n{Colors.BLUE}Перезапуск сервиса...{Colors.NC}")
        self._run_command(['systemctl', 'restart', 'remnawave-limiter'], check=False)
        print(f"{Colors.GREEN}✅ Сервис перезапущен{Colors.NC}")

    def logs(self, follow: bool = False, lines: int = 50):
        """Показывает логи сервиса"""
        cmd = ['journalctl', '-u', 'remnawave-limiter', '--no-pager']

        if follow:
            cmd.append('-f')
        else:
            cmd.extend(['-n', str(lines)])

        try:
            if follow:
                subprocess.run(cmd)
            else:
                success, output = self._run_command(cmd, check=False)
                if success:
                    print(output)
        except KeyboardInterrupt:
            pass

def main():
    parser = argparse.ArgumentParser(
        description='Remnawave IP Limiter CLI',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Примеры:
  limiter status                    # Показать статус системы
  limiter violations                # Последние 20 нарушений
  limiter violations -n 50          # Последние 50 нарушений
  limiter banned                    # Список забаненных IP
  limiter unban 1.2.3.4             # Разбанить IP
  limiter unban-all                 # Разбанить все IP
  limiter active                    # Активные подключения
  limiter logs                      # Последние 50 строк логов
  limiter logs -f                   # Следить за логами (Ctrl+C для выхода)
  limiter clear                     # Очистить все логи
        """
    )

    subparsers = parser.add_subparsers(dest='command', help='Команды')

    subparsers.add_parser('status', help='Статус системы')

    violations_parser = subparsers.add_parser('violations', help='Показать нарушения')
    violations_parser.add_argument('-n', '--lines', type=int, default=20, help='Количество строк (default: 20)')

    subparsers.add_parser('banned', help='Список забаненных IP')

    unban_parser = subparsers.add_parser('unban', help='Разбанить IP')
    unban_parser.add_argument('ip', help='IP адрес')

    subparsers.add_parser('unban-all', help='Разбанить все IP')

    subparsers.add_parser('active', help='Активные подключения')

    subparsers.add_parser('clear', help='Очистить логи')

    logs_parser = subparsers.add_parser('logs', help='Показать логи сервиса')
    logs_parser.add_argument('-f', '--follow', action='store_true', help='Следить за логами')
    logs_parser.add_argument('-n', '--lines', type=int, default=50, help='Количество строк (default: 50)')

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    cli = LimiterCLI()

    try:
        if args.command == 'status':
            cli.status()
        elif args.command == 'violations':
            cli.violations(tail=args.lines)
        elif args.command == 'banned':
            cli.list_banned()
        elif args.command == 'unban':
            cli.unban(args.ip)
        elif args.command == 'unban-all':
            cli.unban_all()
        elif args.command == 'active':
            cli.active_connections()
        elif args.command == 'clear':
            cli.clear_logs()
        elif args.command == 'logs':
            cli.logs(follow=args.follow, lines=args.lines)
        else:
            parser.print_help()
    except KeyboardInterrupt:
        print(f"\n{Colors.BLUE}Прервано{Colors.NC}")
        sys.exit(0)
    except Exception as e:
        print(f"{Colors.RED}❌ Ошибка: {e}{Colors.NC}")
        sys.exit(1)

if __name__ == '__main__':
    main()
