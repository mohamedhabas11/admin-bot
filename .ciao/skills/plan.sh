#!/bin/sh
# plan.sh — break a specification into actionable tasks
echo "=== creating task plan ==="
spec="SPEC.md"
if [ ! -f "$spec" ]; then
  tasks="- Investigate scope"
  echo "$tasks" > .ciao/state/tasks.txt
fi
grep '\- \[ \]' "$spec" 2>/dev/null | while read -r line; do
  echo "$line"
done > .ciao/state/tasks.txt
cat > PLAN.md <<END
# Task Plan
$(cat .ciao/state/tasks.txt)
## Order
1. Fix dependencies first (go mod tidy)
2. Configure linter (.golangci.yml)
3. Add tests
4. Fix Dockerfile
5. Add CI workflow
6. Verify everything
END
echo "created PLAN.md"
echo "tasks=$(wc -l < .ciao/state/tasks.txt)"
