#!/bin/sh
# spec.sh — generate a specification document from the scan results
echo "=== creating specification ==="
title="${CIAO_ITEM_TITLE:-feature}"
scan_out=".ciao/state/scan.issues"
if [ -f "$scan_out" ]; then
  issues=$(cat "$scan_out")
else
  issues="No scan data available"
fi
cat > SPEC.md <<END
# Specification: $title
## Issues Identified
$(echo "$issues" | sed 's/^/- /')
## Objective
Address the issues above to improve project health.
## Scope
- In: fix each identified issue
- Out: scope creep beyond listed items
## Acceptance Criteria
- Tests pass (go test ./...)
- Build clean (go build ./...)
- Formatting clean (gofmt -s -w .)
## Checklist
- [ ] Tests added or verified
- [ ] Linter configured and passing
- [ ] Dockerfile fixed
- [ ] CI pipeline added
- [ ] Dependencies verified (go mod tidy/verify)
END
echo "created SPEC.md"
