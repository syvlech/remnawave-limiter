package main

import (
	"log"

	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/limiter"
	"github.com/remnawave/limiter/pkg/logger"
)

func main() {
	cfg, err := config.LoadConfig("")
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	mainLogger, mainLogFile, err := logger.SetupLogger("/var/log/remnawave-limiter/limiter.log")
	if err != nil {
		log.Fatalf("Ошибка настройки логирования: %v", err)
	}
	defer mainLogFile.Close()

	violationLogger, violationLogFile, err := logger.SetupViolationLogger(cfg.ViolationLogPath)
	if err != nil {
		log.Fatalf("Ошибка настройки логирования нарушений: %v", err)
	}
	defer violationLogFile.Close()

	l := limiter.NewLimiter(cfg, mainLogger, violationLogger)
	l.Run()
}
