#!/usr/bin/env bash
# End-to-end smoke test for clisnap. No network, idempotent, runs from a
# clean tree. This script plus 'go test ./...' is the whole verification
# story — the repository intentionally ships no CI.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/clisnap"
SNAPDIR="$WORKDIR/.clisnap"

echo "[1/9] build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/clisnap) || fail "build failed"

echo "[2/9] --version matches the manifest version"
VERSION_OUT="$("$BIN" --version)"
[ "$VERSION_OUT" = "clisnap 0.1.0" ] || fail "unexpected version output: $VERSION_OUT"

echo "[3/9] record a volatile command"
cat > "$WORKDIR/tool.sh" <<'EOF'
#!/bin/sh
echo "tool 1.0 starting"
echo "run pid=$$ at $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "scratch: $(mktemp -d)"
echo "state: ready"
EOF
chmod +x "$WORKDIR/tool.sh"
"$BIN" --dir "$SNAPDIR" record boot -- "$WORKDIR/tool.sh" \
  | grep -q "recorded boot" || fail "record did not confirm"
grep -q "pid=<PID> at <TIMESTAMP>" "$SNAPDIR/boot.snap" || fail "pid/timestamp not redacted"
grep -q "scratch: <TMP>" "$SNAPDIR/boot.snap" || fail "tmp path not redacted"

echo "[4/9] check passes although pid/timestamp/tmpdir changed"
"$BIN" --dir "$SNAPDIR" check | grep -q "ok      boot" || fail "volatile re-run did not pass"

echo "[5/9] check fails with a diff when behavior changes"
sed -i.bak 's/state: ready/state: degraded/' "$WORKDIR/tool.sh"
set +e
CHECK_OUT="$("$BIN" --dir "$SNAPDIR" check 2>&1)"
CHECK_CODE=$?
set -e
[ "$CHECK_CODE" -eq 1 ] || fail "expected exit 1 on mismatch, got $CHECK_CODE"
echo "$CHECK_OUT" | grep -q -- "-state: ready" || fail "diff missing old line"
echo "$CHECK_OUT" | grep -q -- "+state: degraded" || fail "diff missing new line"

echo "[6/9] check --update accepts the new behavior"
"$BIN" --dir "$SNAPDIR" check --update | grep -q "updated boot" || fail "update did not rewrite"
"$BIN" --dir "$SNAPDIR" check | grep -q "ok      boot" || fail "post-update check failed"

echo "[7/9] list and show"
"$BIN" --dir "$SNAPDIR" list | grep -q "boot" || fail "list missing snapshot"
"$BIN" --dir "$SNAPDIR" show boot | head -1 | grep -q "clisnap snapshot v1" || fail "show header wrong"

echo "[8/9] redact filter mode"
REDACTED="$(printf 'worker pid 4821 up 12ms at 0xdeadbeef\n' | "$BIN" redact)"
[ "$REDACTED" = "worker pid <PID> up <DURATION> at <ADDR>" ] \
  || fail "filter output unexpected: $REDACTED"

echo "[9/9] rm"
"$BIN" --dir "$SNAPDIR" rm boot | grep -q "removed boot" || fail "rm did not confirm"
[ ! -e "$SNAPDIR/boot.snap" ] || fail "snapshot file still present"

echo "SMOKE OK"
