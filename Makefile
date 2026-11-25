.PHONY: all build clean test test-race cover lint fmt vet vuln tidy docker-build docker-up docker-down

BINARY  := remnawave-limiter
VERSION := $(shell sed -n 's/.*Version = "\(.*\)"/\1/p' internal/version/version.go)
LDFLAGS := -s -w -X github.com/remnawave/limiter/internal/version.Version=$(VERSION)

all: build

build:
	@echo "🔨 Сборка v$(VERSION)..."
	go mod download
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/limiter/
	@echo "✅ Готово!"

clean:
	rm -rf bin/ coverage.out coverage.html

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "📊 coverage.html"

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: vet
	@out="$$(gofmt -l . )"; \
	if [ -n "$$out" ]; then echo "❌ gofmt требуется для:"; echo "$$out"; exit 1; fi
	@echo "✅ Формат в порядке"

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

tidy:
	go mod tidy

docker-build:
	docker build --build-arg VERSION=$(VERSION) \
		-t $(BINARY):$(VERSION) -t $(BINARY):latest .

docker-up:
	docker compose up -d

docker-down:
	docker compose down
