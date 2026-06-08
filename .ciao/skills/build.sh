#!/bin/sh
# build.sh — verify the project compiles
echo "=== building ==="
if go build ./... 2>&1; then
  echo "build: success"
  echo "status=success" > .ciao/state/build.out
else
  echo "build: failed"
  echo "status=fail" > .ciao/state/build.out
  exit 1
fi
