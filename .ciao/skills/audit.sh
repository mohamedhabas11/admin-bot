#!/bin/sh
# audit.sh — check module health and report findings
echo "=== auditing module ==="
go mod tidy 2>&1
if ! go mod verify 2>&1; then
  echo "audit: deps need fixing"
  echo "status=dirty" > .ciao/state/audit.out
  exit 1
fi
echo "audit: clean"
echo "status=clean" > .ciao/state/audit.out
