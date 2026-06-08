#!/bin/sh
# format.sh — format Go source and tidy imports
echo "=== formatting ==="
gofmt -s -w . 2>&1
echo "format: gofmt done"
if command -v goimports >/dev/null 2>&1; then
  goimports -w . 2>&1
  echo "format: goimports done"
fi
echo "status=done" > .ciao/state/format.out
