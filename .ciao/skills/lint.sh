#!/bin/sh
# lint.sh — configure and run Go linting
echo "=== linting ==="
if [ ! -f .golangci.yml ] && [ ! -f .golangci.yaml ]; then
  echo "  [lint] no config — creating .golangci.yml"
  cat > .golangci.yml <<'CONFIG'
run:
  timeout: 5m
linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
issues:
  exclude-use-default: false
CONFIG
  echo "  created .golangci.yml"
fi
if go vet ./... 2>&1; then
  echo "vet: clean"
else
  echo "vet: issues found (non-fatal)"
fi
if command -v golangci-lint >/dev/null 2>&1; then
  if golangci-lint run ./... 2>&1; then
    echo "golangci-lint: clean"
  else
    echo "golangci-lint: issues found (non-fatal)"
  fi
else
  echo "golangci-lint: not installed — skipping"
fi
echo "status=done" > .ciao/state/lint.out
