#!/usr/bin/env bash
# Records the bundled sample: starts all five scenarios (chaos on), waits until
# every run finished, and writes the run's events to web/public/samples/demo.jsonl.
set -euo pipefail
VIZ="http://localhost:${VISUALIZER_HOST_PORT:-8095}"
SPEED="${SPEED:-1}"
OUT="web/public/samples/demo.jsonl"

after=$(curl -sf "$VIZ/healthz" | jq -r .events)
runs=$(curl -sf -X POST "$VIZ/api/runs" -H 'content-type: application/json' \
  -d "{\"scenario\":\"all\",\"speed\":$SPEED,\"chaos\":true}" | jq -r '.[].run_id')
echo "started: $(echo $runs | tr '\n' ' ')"
for i in $(seq 1 300); do
  done_count=0
  for r in $runs; do
    if curl -sf "$VIZ/api/runs" | jq -e --arg r "$r" '.[] | select(.run_id == $r and .finished != null)' >/dev/null; then
      done_count=$((done_count + 1))
    fi
  done
  [ "$done_count" -eq "$(echo "$runs" | wc -w)" ] && break
  sleep 1
done
sleep 3 # let late gateway access logs arrive
curl -sf "$VIZ/api/events?after=$after" | jq -c '.[]' > "$OUT"
echo "wrote $OUT ($(wc -l < "$OUT") events)"
