#!/usr/bin/env python3

import re
import time
import json
from collections import defaultdict
from datetime import datetime
from pathlib import Path
from typing import Dict, Set, List, Optional
import logging
from dataclasses import dataclass
import signal
import sys
import os
from dotenv import load_dotenv
import requests
from threading import Thread

@dataclass
class Config:
    """Конфигурация скрипта"""
    remnawave_log_path: str
    violation_log_path: str = "/var/log/remnawave-limiter/access-limiter.log"
    max_ips_per_key: int = 1
    check_interval: int = 5
    log_clear_interval: int = 3600
    webhook_url: Optional[str] = None
    server_name: str = "VPN Server"
    ban_duration_minutes: int = 30

    @classmethod
    def from_env(cls, env_path: str = None):
        """Загружает конфигурацию из .env файла"""
        if env_path:
            load_dotenv(env_path)
        else:
            script_dir = Path(__file__).parent.absolute()
            env_file = script_dir / '.env'
            if env_file.exists():
                load_dotenv(env_file)

        webhook_url = os.getenv('WEBHOOK_URL', '').strip()
        if not webhook_url or webhook_url.lower() == 'none':
            webhook_url = None

        return cls(
            remnawave_log_path=os.getenv('REMNAWAVE_LOG_PATH', '/var/log/remnawave/access.log'),
            violation_log_path=os.getenv('VIOLATION_LOG_PATH', '/var/log/remnawave-limiter/access-limiter.log'),
            max_ips_per_key=int(os.getenv('MAX_IPS_PER_KEY', '1')),
            check_interval=int(os.getenv('CHECK_INTERVAL', '5')),
            log_clear_interval=int(os.getenv('LOG_CLEAR_INTERVAL', '3600')),
            webhook_url=webhook_url,
            server_name=os.getenv('SERVER_NAME', 'VPN Server'),
            ban_duration_minutes=int(os.getenv('BAN_DURATION_MINUTES', '30'))
        )

class IPLimiter:
    def __init__(self, config: Config):
        self.config = config
        self.running = True
        self.last_clear = int(time.time())

        self.log_pattern = re.compile(
            r'from\s+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}):\d+\s+accepted.*?email:\s*(\S+)'
        )

        self.violation_cache: Dict[str, Dict[str, float]] = defaultdict(dict)

        self._setup_logging()

        signal.signal(signal.SIGINT, self._signal_handler)
        signal.signal(signal.SIGTERM, self._signal_handler)

    def _setup_logging(self):
        """Настройка логирования"""
        log_dir = Path('/var/log/remnawave-limiter')
        log_dir.mkdir(parents=True, exist_ok=True)

        logging.basicConfig(
            level=logging.INFO,
            format='%(asctime)s - %(levelname)s - %(message)s',
            handlers=[
                logging.FileHandler('/var/log/remnawave-limiter/limiter.log'),
                logging.StreamHandler()
            ]
        )
        self.logger = logging.getLogger(__name__)

        violation_log_dir = Path(self.config.violation_log_path).parent
        violation_log_dir.mkdir(parents=True, exist_ok=True)

        self.violation_logger = logging.getLogger('violations')
        self.violation_logger.setLevel(logging.INFO)
        violation_handler = logging.FileHandler(self.config.violation_log_path)
        violation_handler.setFormatter(logging.Formatter('%(asctime)s %(message)s', datefmt='%Y/%m/%d %H:%M:%S'))
        self.violation_logger.addHandler(violation_handler)
        self.violation_logger.propagate = False

    def _signal_handler(self, signum, frame):
        """Обработчик сигналов для корректного завершения"""
        self.logger.info(f"Получен сигнал {signum}, завершение работы...")
        self.running = False
        sys.exit(0)

    def _mask_ip(self, ip: str) -> str:
        """Маскирует IP для приватности (например: 123.45.**.** или **.**.121.50)"""
        parts = ip.split('.')
        if len(parts) == 4:
            return f"{parts[0]}.{parts[1]}.**.**"
        return ip

    def _send_webhook(self, email: str, ip: str, active_ip_count: int):
        """Отправляет webhook уведомление о блокировке (асинхронно)"""
        if not self.config.webhook_url:
            return

        def send():
            try:
                payload = {
                    "server": self.config.server_name,
                    "ban_duration_minutes": self.config.ban_duration_minutes,
                    "ip_masked": self._mask_ip(ip),
                    "ip_full": ip,
                    "email": email,
                    "reason": f"подключение к локации с {active_ip_count} IP (лимит: {self.config.max_ips_per_key})",
                    "timestamp": datetime.now().isoformat(),
                    "active_ip_count": active_ip_count,
                    "limit": self.config.max_ips_per_key
                }

                response = requests.post(
                    self.config.webhook_url,
                    json=payload,
                    timeout=5,
                    headers={'Content-Type': 'application/json'}
                )

                if response.status_code == 200:
                    self.logger.debug(f"Webhook отправлен для {email} -> {ip}")
                else:
                    self.logger.warning(f"Webhook вернул код {response.status_code}")

            except requests.exceptions.Timeout:
                self.logger.warning(f"Webhook timeout для {email}")
            except Exception as e:
                self.logger.warning(f"Ошибка отправки webhook: {e}")

        thread = Thread(target=send, daemon=True)
        thread.start()

    def _parse_log_line(self, line: str) -> tuple:
        """Парсит строку лога и извлекает timestamp, email и IP"""
        timestamp_match = re.match(r'(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})', line)
        timestamp = None
        if timestamp_match:
            try:
                timestamp = datetime.strptime(timestamp_match.group(1), '%Y/%m/%d %H:%M:%S')
            except:
                pass

        match = self.log_pattern.search(line)
        if match:
            ip = match.group(1)
            email = match.group(2)

            if ip in ('127.0.0.1', '::1'):
                return None

            return email, ip, timestamp
        return None

    def _process_log_file(self) -> bool:
        """
        Обрабатывает лог-файл и возвращает True если нужно очистить лог.
        Улучшенная логика: считает только ОДНОВРЕМЕННО активные IP.
        1. Читает весь лог
        2. Для каждого email собирает последнее время активности каждого IP
        3. Определяет "активные" IP (последняя активность < 60 сек от самой новой записи)
        4. Банит лишние АКТИВНЫЕ IP (если одновременно активных > лимит)
        """
        log_path = Path(self.config.remnawave_log_path)

        if not log_path.exists():
            return False

        email_ip_times: Dict[str, Dict[str, datetime]] = defaultdict(dict)
        latest_timestamp = None

        try:
            with open(log_path, 'r', encoding='utf-8', errors='ignore') as f:
                for line in f:
                    result = self._parse_log_line(line)
                    if result:
                        email, ip, timestamp = result

                        if timestamp:
                            email_ip_times[email][ip] = timestamp
                            if latest_timestamp is None or timestamp > latest_timestamp:
                                latest_timestamp = timestamp
        except Exception as e:
            self.logger.error(f"Ошибка при чтении лога: {e}")
            return False

        if latest_timestamp is None:
            return False

        should_clear_log = False

        for email, ip_times in email_ip_times.items():
            active_ips = []
            for ip, last_seen in ip_times.items():
                time_diff = (latest_timestamp - last_seen).total_seconds()
                if time_diff <= 60:
                    active_ips.append(ip)

            active_ips.sort()

            if len(active_ips) > self.config.max_ips_per_key:
                should_clear_log = True

                disallowed_ips = active_ips[self.config.max_ips_per_key:]

                for banned_ip in disallowed_ips:
                    now = time.time()
                    last_logged = self.violation_cache[email].get(banned_ip, 0)

                    if now - last_logged > 60:
                        self.violation_logger.info(f"[LIMIT_IP] Email = {email} || SRC = {banned_ip}")
                        self.logger.warning(f"🚫 Нарушение: {email} одновременно использует {len(active_ips)} IP (лимит: {self.config.max_ips_per_key}), банится {banned_ip}")
                        self.logger.debug(f"Активные IP для {email}: {active_ips}")

                        self._send_webhook(email, banned_ip, len(active_ips))

                        self.violation_cache[email][banned_ip] = now

        return should_clear_log

    def _clear_access_log(self):
        """Очищает access лог (truncate)"""
        log_path = Path(self.config.remnawave_log_path)

        try:
            if log_path.exists():
                with open(log_path, 'w') as f:
                    pass

                self.violation_cache.clear()

                self.last_clear = int(time.time())
                self.logger.info("🗑️ Лог Remnawave очищен (truncated)")
        except Exception as e:
            self.logger.error(f"Ошибка при очистке лога: {e}")

    def run(self):
        """Основной цикл мониторинга"""
        self.logger.info("🚀 Remnawave IP Limiter запущен")
        self.logger.info(f"📁 Файл лога Remnawave: {self.config.remnawave_log_path}")
        self.logger.info(f"📁 Файл лога нарушений: {self.config.violation_log_path}")
        self.logger.info(f"🔢 Максимум IP на ключ: {self.config.max_ips_per_key}")
        self.logger.info(f"🔄 Интервал проверки: {self.config.check_interval}с")
        self.logger.info(f"🗑️ Очистка лога каждые: {self.config.log_clear_interval}с")

        while self.running:
            try:
                should_clear_log = self._process_log_file()

                current_time = int(time.time())
                if should_clear_log or (current_time - self.last_clear > self.config.log_clear_interval):
                    self._clear_access_log()

                time.sleep(self.config.check_interval)

            except Exception as e:
                self.logger.error(f"Ошибка в основном цикле: {e}", exc_info=True)
                time.sleep(self.config.check_interval)

        self.logger.info("👋 IP Limiter остановлен")

if __name__ == '__main__':
    config = Config.from_env()
    limiter = IPLimiter(config)
    limiter.run()
