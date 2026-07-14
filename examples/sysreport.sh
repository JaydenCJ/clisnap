#!/bin/sh
# A deliberately volatile CLI: every run prints different timestamps,
# PIDs, temp paths and durations. clisnap's default redactors make its
# snapshot stable anyway.
SCRATCH="$(mktemp -d)"
trap 'rmdir "$SCRATCH" 2>/dev/null' EXIT

echo "sysreport 1.0 (build-4f9a01c2)"
echo "started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "pid: $$"
echo "scratch dir: $SCRATCH"
echo "collected 3 probes in $(( $$ % 90 + 10 ))ms"
echo "status: healthy"
