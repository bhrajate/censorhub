.PHONY: build run test bench lint clean proto wire \
       docker-build docker-up docker-down \
       run-dev run-test run-staging run-prod

APP_NAME := censorhub
BUILD_DIR := bin
MAIN_PATH := ./cmd/server
ENV ?= dev

# Build
build:
	@echo "Building $(APP_NAME)..."
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_PATH)

# Run with environment (default: dev)
# Usage: make run ENV=dev | make run ENV=production
run:
	go run $(MAIN_PATH) --config configs/config.yaml --env $(ENV)

# Environment shortcuts
run-dev:
	@$(MAKE) run ENV=dev

run-staging:
	@$(MAKE) run ENV=staging

run-prod:
	@$(MAKE) run ENV=production

# Test
test:
	APP_ENV=test go test ./... -v -race -count=1

bench:
	go test ./internal/infrastructure/algorithm/... -bench=. -benchmem -count=3

coverage:
	APP_ENV=test go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html

# Code quality
lint:
	@which golangci-lint > /dev/null 2>&1 || { \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin; \
	}
	golangci-lint run ./...

fmt:
	gofmt -s -w .
	goimports -w .

# Generate
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/censor/v1/censor.proto

wire:
	cd cmd/server && wire

# Docker (ENV can be: dev, staging, production)
docker-build:
	docker build -f deployments/docker/Dockerfile -t $(APP_NAME):latest .

docker-up:
	docker-compose -f deployments/docker/docker-compose.yaml up -d

docker-down:
	docker-compose -f deployments/docker/docker-compose.yaml down

# Clean
clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html
