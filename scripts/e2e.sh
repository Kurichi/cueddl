#!/usr/bin/env bash
# End-to-end test against the Cloud Spanner emulator.
#
# Exercises the full declarative lifecycle:
#   1. apply examples/simple onto an empty emulator (creates instance + db)
#   2. plan again            -> must be a no-op (idempotency)
#   3. plan examples/evolved -> must contain one destructive statement
#   4. apply without permission -> must be refused
#   5. apply with CUEDDL_ALLOW_DESTRUCTIVE=1 -> must succeed
#   6. plan again            -> must be a no-op again
set -euo pipefail
cd "$(dirname "$0")/.."

CONTAINER=cueddl-e2e-emulator
PORT="${PORT:-9010}"

docker run -d --rm --name "$CONTAINER" -p "$PORT:9010" \
	gcr.io/cloud-spanner-emulator/emulator >/dev/null
trap 'docker stop "$CONTAINER" >/dev/null 2>&1 || true' EXIT

export SPANNER_EMULATOR_HOST="localhost:$PORT"

for _ in $(seq 1 30); do
	if docker logs "$CONTAINER" 2>&1 | grep -q "gRPC server listening"; then
		break
	fi
	sleep 1
done

fail() {
	echo "e2e: FAIL: $1" >&2
	exit 1
}

go tool cue cmd apply ./examples/simple | grep -q "Applied 6 statement" ||
	fail "initial apply did not run 6 statements"
go tool cue cmd plan ./examples/simple | grep -q "No changes" ||
	fail "simple is not idempotent"
go tool cue cmd plan ./examples/evolved | grep -q "1 destructive" ||
	fail "evolved plan did not flag the destructive statement"
if go tool cue cmd apply ./examples/evolved >/dev/null 2>&1; then
	fail "destructive apply was not refused"
fi
CUEDDL_ALLOW_DESTRUCTIVE=1 go tool cue cmd apply ./examples/evolved | grep -q "Applied 9 statement" ||
	fail "evolved apply did not run 9 statements"
go tool cue cmd plan ./examples/evolved | grep -q "No changes" ||
	fail "evolved is not idempotent"

echo "e2e: PASS"
