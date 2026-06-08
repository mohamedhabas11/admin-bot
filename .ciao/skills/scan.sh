#!/bin/sh
# scan.sh — audit the repo for improvement opportunities
echo "=== scanning repo ==="
issues=""
repo_dir=$(pwd)

# --- tests ---
if ! find . -name '*_test.go' -not -path './.ciao/*' 2>/dev/null | grep -q .; then
  echo "  [testing] no test files found"
  issues="$issues
testing: No test files exist"
fi

# --- lint config ---
if [ ! -f .golangci.yml ] && [ ! -f .golangci.yaml ]; then
  echo "  [linting] no .golangci.yml found"
  issues="$issues
linting: No golangci-lint config present"
fi

# --- Dockerfile ---
if [ -f Dockerfile ] && grep -q "^#" Dockerfile 2>/dev/null; then
  echo "  [docker] Dockerfile is a stub (all comments)"
  issues="$issues
docker: Dockerfile is incomplete (all comments)"
fi

# --- CI ---
if [ ! -d .github ]; then
  echo "  [ci] no .github/ directory"
  issues="$issues
ci: No GitHub Actions workflows"
fi

# --- go mod ---
go mod tidy 2>/dev/null
if ! go mod verify 2>/dev/null; then
  echo "  [deps] go module verification failed"
  issues="$issues
deps: Module dependencies need attention (go mod tidy/verify)"
fi

# --- go.sum ---
if [ ! -f go.sum ]; then
  echo "  [deps] no go.sum"
  issues="$issues
deps: Missing go.sum"
fi

mkdir -p .ciao/state
if [ -z "$issues" ]; then
  echo "=============================="
  echo "result: clean"
  echo "Nothing to improve — repo is in good shape"
  echo "clean" > .ciao/state/scan.status
else
  echo "=============================="
  echo "result: issues_found"
  echo "Found improvement opportunities:"
  echo "$issues"
  echo "issues_found" > .ciao/state/scan.status
  echo "$issues" > .ciao/state/scan.issues
fi
