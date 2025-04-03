# TODO: Ship only binary
# TODO: ENV CONFIG=""

# # Stage 1: Build dependencies and tools
# FROM golang:1.23.0 AS builder

# WORKDIR /usr/src/app

# # Copy the application code (invalidates cache only when code changes)
# COPY . .

# # Download dependencies (caches go mod dependencies separately)
# RUN go mod download

# # Stage 2: Runtime (lightweight and uses prebuilt Air)
# FROM golang:1.23.0

# WORKDIR /usr/src/app

# # Copy application files and Air binary from builder
# COPY --from=builder /usr/src/app /usr/src/app

# # Expose the application port
# EXPOSE ${APP_PORT:-8080}

