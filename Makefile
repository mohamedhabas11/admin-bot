# Define variables
PROJECT_NAME := admin-bot
PROJECT_PORT := 8080
GO := go

.PHONY: all test lint build clean

# Default target
all: test

# Run tests
test:
	@echo "Running tests..."
	@$(GO) test ./pkg/... -v

# Lint the code
lint:
	@echo "Running linter..."
	@golangci-lint run ./...

# Build the project
build:
	@echo "Building $(PROJECT_NAME)..."
	@$(GO) build -o bin/$(PROJECT_NAME)

# Clean build artifacts
clean:
	@echo "Cleaning up..."
	@rm -rf bin

docker-dev:
	@docker build -f dev.Dockerfile -t $(PROJECT_NAME)-dev \
	--build-arg APP_PORT=$(PROJECT_PORT) .
	@docker run --rm -it -p $(PROJECT_PORT):$(PROJECT_PORT) -v $(PWD):/usr/src/app $(PROJECT_NAME)-dev
