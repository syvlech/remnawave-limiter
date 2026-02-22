package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

type Config struct {
	RemnawaveLogPath   string
	ViolationLogPath   string
	MaxIPsPerKey       int
	CheckInterval      int
	LogClearInterval   int
	WebhookURL         string
	WebhookTemplate    string
	WebhookHeaders     map[string]string
	BanDurationMinutes int
	WhitelistEmails    []string
}

func LoadConfig(envPath string) (*Config, error) {
	if envPath == "" {
		execPath, err := os.Executable()
		if err == nil {
			envPath = filepath.Join(filepath.Dir(execPath), ".env")
		} else {
			envPath = ".env"
		}
	}

	if err := godotenv.Load(envPath); err != nil {
		logrus.WithError(err).Warn("Не удалось загрузить .env файл, используются переменные окружения")
	}

	cfg := &Config{
		RemnawaveLogPath:   getEnv("REMNAWAVE_LOG_PATH", "/var/log/remnanode/access.log"),
		ViolationLogPath:   getEnv("VIOLATION_LOG_PATH", "/var/log/remnawave-limiter/access-limiter.log"),
		MaxIPsPerKey:       getEnvInt("MAX_IPS_PER_KEY", 1),
		CheckInterval:      getEnvInt("CHECK_INTERVAL", 5),
		LogClearInterval:   getEnvInt("LOG_CLEAR_INTERVAL", 3600),
		WebhookURL:         getEnv("WEBHOOK_URL", ""),
		WebhookTemplate:    getEnv("WEBHOOK_TEMPLATE", ""),
		WebhookHeaders:     parseHeaders(getEnv("WEBHOOK_HEADERS", "")),
		BanDurationMinutes: getEnvInt("BAN_DURATION_MINUTES", 10),
		WhitelistEmails:    parseList(getEnv("WHITELIST_EMAILS", "")),
	}

	if cfg.WebhookURL == "none" || strings.TrimSpace(cfg.WebhookURL) == "" {
		cfg.WebhookURL = ""
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.MaxIPsPerKey <= 0 {
		return fmt.Errorf("MAX_IPS_PER_KEY должен быть > 0, получено %d", cfg.MaxIPsPerKey)
	}
	if cfg.CheckInterval <= 0 {
		return fmt.Errorf("CHECK_INTERVAL должен быть > 0, получено %d", cfg.CheckInterval)
	}
	if cfg.LogClearInterval <= 0 {
		return fmt.Errorf("LOG_CLEAR_INTERVAL должен быть > 0, получено %d", cfg.LogClearInterval)
	}
	if cfg.BanDurationMinutes <= 0 {
		return fmt.Errorf("BAN_DURATION_MINUTES должен быть > 0, получено %d", cfg.BanDurationMinutes)
	}

	logPaths := map[string]string{
		"REMNAWAVE_LOG_PATH": cfg.RemnawaveLogPath,
		"VIOLATION_LOG_PATH": cfg.ViolationLogPath,
	}
	for key, logPath := range logPaths {
		dir := filepath.Dir(logPath)
		if info, err := os.Stat(dir); err != nil {
			return fmt.Errorf("директория для %s (%s) недоступна: %v", key, dir, err)
		} else if !info.IsDir() {
			return fmt.Errorf("путь %s для %s не является директорией", dir, key)
		}
	}

	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		intVal, err := strconv.Atoi(value)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"key":     key,
				"value":   value,
				"default": defaultValue,
			}).Warnf("Не удалось преобразовать %s в число, используется значение по умолчанию %d", key, defaultValue)
			return defaultValue
		}
		return intVal
	}
	return defaultValue
}

func parseHeaders(headersStr string) map[string]string {
	headers := make(map[string]string)
	if headersStr == "" {
		return headers
	}

	pairs := strings.Split(headersStr, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			headers[key] = value
		}
	}

	return headers
}

func parseList(listStr string) []string {
	if listStr == "" {
		return []string{}
	}

	items := strings.Split(listStr, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}
