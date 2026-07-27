#!/usr/bin/env bash
# Fault injection: kill an edge relay mid-session and measure how the region
# behind it recovers, with an unaffected region as the control.
#
# Everything the load generators expose is reachable with `docker exec ... curl`,
# so this needs no published ports.
#
#   bash tools/faultinj/relay-failure.sh [results-dir]
set -u

OUT="${1:-results/faultinj}"
RUN="faultinj-$(date +%s)"
VICTIM=edgecast-relay-eu-central-1
TREATMENT=edgecast-moq-sub-eu-central-1
CONTROL=edgecast-moq-sub-us-east-1

say() { printf '\n[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

post() { docker exec "$1" curl -s -X POST -H 'Content-Type: application/json' -d "$3" "http://localhost:8080$2" >/dev/null; }

mkdir -p "$OUT"

say "clearing impairment and starting run $RUN on both regions"
post "$TREATMENT" /netem/clear '{}'
post "$CONTROL"   /netem/clear '{}'
post "$TREATMENT" /sessions/restart "{\"run_id\":\"$RUN\"}"
post "$CONTROL"   /sessions/restart "{\"run_id\":\"$RUN\"}"

say "steady state for 25s"
sleep 25

say "KILLING $VICTIM (SIGKILL, no graceful shutdown)"
docker kill "$VICTIM" >/dev/null
KILL_AT=$(date +%s)

say "relay down; holding 15s"
sleep 15

say "restarting $VICTIM"
docker start "$VICTIM" >/dev/null
UP_AT=$(date +%s)

say "observing recovery for 45s"
sleep 45

say "collecting results"
docker exec "$TREATMENT" curl -s "http://localhost:8080/sessions/results?run_id=$RUN" > "$OUT/treatment-eu-central.json"
docker exec "$CONTROL"   curl -s "http://localhost:8080/sessions/results?run_id=$RUN" > "$OUT/control-us-east.json"

cat > "$OUT/meta.json" <<EOF
{
  "run_id": "$RUN",
  "victim_container": "$VICTIM",
  "killed_unix": $KILL_AT,
  "restarted_unix": $UP_AT,
  "treatment_region": "eu-central",
  "control_region": "us-east",
  "note": "eu-central subscribers depend on the killed edge relay; us-east is unaffected and serves as the control."
}
EOF

say "wrote $OUT/{treatment-eu-central,control-us-east,meta}.json"
