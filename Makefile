.PHONY: build clean install test

# Версия Go
GO_VERSION := 1.21

# Имя бинарников
LIMITER_BIN := remnawave-limiter
CLI_BIN := limiter-cli

# Пути установки
INSTALL_PATH := /usr/local/bin

all: build

# Сборка обоих бинарников
build:
	@echo "🔨 Сборка Remnawave IP Limiter..."
	go mod download
	go build -ldflags="-s -w" -o bin/$(LIMITER_BIN) ./cmd/limiter
	go build -ldflags="-s -w" -o bin/$(CLI_BIN) ./cmd/limiter-cli
	@echo "✅ Сборка завершена!"

# Установка в систему
install: build
	@echo "📦 Установка бинарников..."
	sudo cp bin/$(LIMITER_BIN) $(INSTALL_PATH)/
	sudo cp bin/$(CLI_BIN) $(INSTALL_PATH)/
	sudo chmod +x $(INSTALL_PATH)/$(LIMITER_BIN)
	sudo chmod +x $(INSTALL_PATH)/$(CLI_BIN)
	@echo "✅ Установка завершена!"

# Очистка собранных файлов
clean:
	@echo "🗑️  Очистка..."
	rm -rf bin/
	@echo "✅ Очистка завершена!"

# Тестирование
test:
	@echo "🧪 Запуск тестов..."
	go test -v ./...

# Проверка кода
lint:
	@echo "🔍 Проверка кода..."
	go vet ./...
	go fmt ./...
