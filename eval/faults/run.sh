#!/bin/sh
# Fault-injection driver for the Wombat harness.
#
# Each scenario starts cmd/faultsrv, which impersonates an OpenAI-compatible
# model and misbehaves in one specific way, then runs the real agent loop
# through cmd/eval on the go-off-by-one fixture. The task is trivially
# solvable, so any failure isolates the transport fault rather than the
# model's coding ability.
#
# Usage:
#   sh eval/faults/run.sh              # every scenario
#   sh eval/faults/run.sh rate429      # one scenario
#   sh eval/faults/run.sh --list
#
# Exit status is non-zero when a scenario behaves unlike its expectation.

set -u

cd "$(dirname "$0")/../.." || exit 1

now_ms() {
	python3 -c 'import time; print(int(time.time() * 1000))' 2>/dev/null ||
		echo $(($(date +%s) * 1000))
}

# name | expected exit | min requests | min elapsed ms | task timeout s | expectation
SCENARIOS='clean|0|2|0|90|retries nothing, solves the task
flaky500|0|3|0|90|retries the 500 and completes
rate429|0|3|900|90|honours Retry-After before retrying
unauthorized|1|1|0|60|fails fast without retry
truncate|0|3|0|90|retries an abrupt mid-stream cut
nodone|0|2|0|60|accepts a clean stream with no [DONE]
noend|0|2|0|60|accepts a cut ending on a value boundary
cleancut|1|2|0|60|never executes a tool call cut off by a clean EOF
badjson|0|2|0|60|skips an undecodable chunk
badargs|1|2|0|60|surfaces a malformed tool call as a task failure
nousage|0|2|0|60|completes without a usage chunk
slow|0|2|3500|90|waits for slow deltas instead of giving up
fallback|0|6|0|120|falls back to the next model
hang|1|1|0|25|aborts a stalled body within the task timeout'

FILTER=""
for arg in "$@"; do
	case "$arg" in
	--all) FILTER="" ;;
	--list)
		printf '%s\n' "$SCENARIOS" | cut -d'|' -f1
		exit 0
		;;
	-h | --help)
		sed -n '2,18p' "$0"
		exit 0
		;;
	*) FILTER="$arg" ;;
	esac
done

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/wombat-faults.XXXXXX") || exit 1
cleanup() {
	if [ -n "${SRV_PID:-}" ]; then
		kill "$SRV_PID" 2>/dev/null
		wait "$SRV_PID" 2>/dev/null
	fi
	[ "${KEEP:-0}" = "1" ] || rm -rf "$ROOT"
}
trap cleanup EXIT INT TERM

echo "building…"
if ! go build -o "$ROOT/faultsrv" ./cmd/faultsrv; then
	echo "faultsrv build failed" >&2
	exit 1
fi
if ! go build -o "$ROOT/eval" ./cmd/eval; then
	echo "eval build failed" >&2
	exit 1
fi

mkdir -p "$ROOT/out"
SUMMARY="$ROOT/summary.txt"
: >"$SUMMARY"
FAILED=0
RAN=0

printf '%-14s %-8s %-4s %-6s %-6s %s\n' SCENARIO RESULT EXIT REQS MS EXPECTATION
printf '%-14s %-8s %-4s %-6s %-6s %s\n' -------------- -------- ---- ----- ------ -----------

while IFS='|' read -r name expect minreq minms timeout note; do
	[ -n "$name" ] || continue
	if [ -n "$FILTER" ] && [ "$name" != "$FILTER" ]; then
		continue
	fi
	RAN=$((RAN + 1))
	dir="$ROOT/out/$name"
	mkdir -p "$dir"
	log="$dir/requests.jsonl"
	: >"$log"

	"$ROOT/faultsrv" -addr 127.0.0.1:0 -scenario "$name" -log "$log" \
		>"$dir/server.out" 2>"$dir/server.err" &
	SRV_PID=$!

	addr=""
	i=0
	while [ "$i" -lt 100 ]; do
		addr=$(sed -n 's/^LISTEN=//p' "$dir/server.out" 2>/dev/null)
		[ -n "$addr" ] && break
		i=$((i + 1))
		sleep 0.05
	done
	if [ -z "$addr" ]; then
		printf '%-14s %-8s %-4s %-6s %-6s %s\n' "$name" ERROR - - - "faultsrv never listened"
		FAILED=$((FAILED + 1))
		kill "$SRV_PID" 2>/dev/null
		wait "$SRV_PID" 2>/dev/null
		SRV_PID=""
		continue
	fi

	cfg="$dir/config.json"
	cat >"$cfg" <<EOF
{
  "version": 2,
  "provider": "fault",
  "model": "fault-model",
  "fallback_models": $(if [ "$name" = "fallback" ]; then echo '["good-model"]'; else echo '[]'; fi),
  "mode": "create",
  "orchestrator": false,
  "providers": {
    "fault": {
      "kind": "openai",
      "base_url": "http://$addr/v1",
      "api_key": "fault-key"
    }
  }
}
EOF

	start=$(now_ms)
	WOMBAT_CONFIG="$cfg" \
		WOMBAT_HOME="$dir/home" \
		WOMBAT_NO_UPDATE_CHECK=1 \
		"$ROOT/eval" -tasks eval/tasks -task go-off-by-one -out "$dir/report" \
		-orchestrator=false -timeout "${timeout}s" -command-timeout 60s \
		>"$dir/eval.out" 2>"$dir/eval.err"
	code=$?
	end=$(now_ms)
	elapsed=$((end - start))

	kill "$SRV_PID" 2>/dev/null
	wait "$SRV_PID" 2>/dev/null
	SRV_PID=""

	reqs=$(wc -l <"$log" | tr -d ' ')

	mismatch=""
	if [ "$expect" = "0" ] && [ "$code" -ne 0 ]; then
		mismatch="expected success, got exit $code"
	fi
	if [ "$expect" = "1" ] && [ "$code" -eq 0 ]; then
		mismatch="expected failure, got success"
	fi
	if [ "$reqs" -lt "$minreq" ]; then
		mismatch="${mismatch:+$mismatch; }expected >= $minreq requests, saw $reqs"
	fi
	if [ "$elapsed" -lt "$minms" ]; then
		mismatch="${mismatch:+$mismatch; }expected >= ${minms}ms, took ${elapsed}ms"
	fi

	class=""
	if [ -f "$dir/report/report.json" ] && command -v python3 >/dev/null 2>&1; then
		class=$(python3 - "$dir/report/report.json" <<'PY' 2>/dev/null
import json, sys
try:
    r = json.load(open(sys.argv[1]))
    res = r.get("results") or []
    if res:
        first = res[0]
        print(first.get("error") or ("pass" if first.get("passed") else ",".join(sorted((r.get("failures") or {}).keys()))))
except Exception:
    pass
PY
		)
	fi

	if [ -z "$mismatch" ]; then
		verdict="ok"
		printf '%-14s %-8s %-4s %-6s %-6s %s\n' "$name" ok "$code" "$reqs" "$elapsed" "$note"
	else
		verdict="fail"
		FAILED=$((FAILED + 1))
		printf '%-14s %-8s %-4s %-6s %-6s %s (%s)\n' "$name" fail "$code" "$reqs" "$elapsed" "$note" "$mismatch"
	fi

	printf '%s|%s|%s|%s|%s|%s|%s\n' \
		"$name" "$verdict" "$code" "$reqs" "$elapsed" "$class" "$mismatch" >>"$SUMMARY"
done <<EOF
$SCENARIOS
EOF

echo
if [ "$RAN" -eq 0 ]; then
	echo "no scenario matched '${FILTER}'"
	exit 2
fi
echo "$RAN scenario(s), $FAILED unexpected"
echo "artifacts: $ROOT"
if [ "${KEEP:-0}" = "1" ]; then
	echo "kept (KEEP=1); summary at $SUMMARY"
else
	echo "rerun with KEEP=1 to keep artifacts"
fi
[ "$FAILED" -eq 0 ] || exit 1
