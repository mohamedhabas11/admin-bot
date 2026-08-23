# Define variables
PROJECT_NAME := admin-bot
PROJECT_PORT := 8080
GO := go

.PHONY: all test vet fmt lint build clean docker-dev

# Default target
all: test

# Run tests
test:
	@echo "Running tests..."
	@$(GO) test ./...

# Static analysis
vet:
	@echo "Running go vet..."
	@$(GO) vet ./...

# Format all Go files
fmt:
	@echo "Formatting..."
	@$(GO) fmt ./...

# Lint the code (requires golangci-lint)
lint:
	@echo "Running linter..."
	@golangci-lint run ./...

# Build the project
build:
	@echo "Building $(PROJECT_NAME)..."
	@$(GO) build -o bin/$(PROJECT_NAME) ./cmd

# Clean build artifacts
clean:
	@echo "Cleaning up..."
	@rm -rf bin

# Hot-reload dev container via air
docker-dev:
	@docker build -f dev.Dockerfile -t $(PROJECT_NAME)-dev \
	--build-arg APP_PORT=$(PROJECT_PORT) .
	@docker run --rm -it -p $(PROJECT_PORT):$(PROJECT_PORT) -v $(PWD):/usr/src/app $(PROJECT_NAME)-dev
