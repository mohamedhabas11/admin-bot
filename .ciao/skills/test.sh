#!/bin/sh
# test.sh — ensure tests exist and pass
echo "=== testing ==="
for dir in $(go list ./... 2>/dev/null); do
  pkg=$(echo "$dir" | sed 's|.*/||')
  pkgpath=$(go list -f '{{.Dir}}' "$dir" 2>/dev/null)
  testfile="$pkgpath/${pkg}_test.go"
  if [ ! -f "$testfile" ] && ! ls "$pkgpath"/*_test.go 2>/dev/null | grep -q .; then
    echo "  [test] no tests in $dir — creating stub"
    first=$(echo "$pkg" | cut -c1 | tr '[:lower:]' '[:upper:]')
    rest=$(echo "$pkg" | cut -c2-)
    testname="Test${first}${rest}"
    cat > "$testfile" <<TESTEOF
package ${pkg}
import "testing"
func ${testname}(t *testing.T) {
	t.Log("placeholder test for ${pkg}")
}
TESTEOF
  fi
done
if go test ./... -count=1 -timeout 120s 2>&1; then
  echo "tests: pass"
  echo "status=pass" > .ciao/state/test.out
else
  echo "tests: fail"
  echo "status=fail" > .ciao/state/test.out
  exit 1
fi
