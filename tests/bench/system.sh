#!/usr/bin/env bash
# System serving bench against the live tests/ stack: drives a load profile at fixed concurrency for a
# fixed window, scrapes /metrics throughput+latency, and captures pprof (CPU top, goroutines, trace) for
# hot-spot analysis. Pure measurement — uses already-running nodes; reconfiguration is done by the caller
# (see verify-sweep at the bottom). Results → tmp/tests/bench-results.
#
# Usage: system.sh <profile> <web_port> <pprof_port> [label]
#   profile: hotzip | metadata | composer
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/tmp/tests/bench-results"; mkdir -p "$OUT"
DUR="${DUR:-20}"; CONC="${CONC:-24}"; TRACE_SEC="${TRACE_SEC:-3}"
GOFLAGS=; export GOFLAGS

profile="${1:?profile}"; web="127.0.0.1:${2:?web_port}"; pp="127.0.0.1:${3:?pprof_port}"; label="${4:-$profile}"
res="$OUT/system-${label}.txt"; : > "$res"
log(){ echo "$*" | tee -a "$res"; }

# endpoints per profile (one worker repeatedly curls these)
case "$profile" in
  hotzip)   ver=$(curl -fsS -m5 "http://$web/errors/@v/list" | head -1); urls=("/errors/@v/$ver.zip");;
  metadata) urls=("/catalog.json" "/errors/list" "/errors/releases.json");;
  composer) urls=("/p2/monolog/monolog.json" "/monolog/3.10.0.zip");;
  *) echo "unknown profile $profile"; exit 2;;
esac

metric_count(){ curl -fsS -m5 "http://$web/metrics/internal" 2>/dev/null \
  | awk -F'[ ]' '/^ogen_server_request_count_total/{s+=$NF} END{printf "%d", s}'; }
goroutines(){ curl -fsS -m5 "http://$pp/debug/pprof/goroutine?debug=1" 2>/dev/null | sed -n '1s/.*total //p'; }

log "== profile=$profile label=$label web=$web pprof=$pp dur=${DUR}s conc=$CONC =="
log "urls: ${urls[*]}"
log "goroutines idle: $(goroutines)"

worker(){ local end=$(( $(date +%s) + DUR )); while [ "$(date +%s)" -lt "$end" ]; do
  for u in "${urls[@]}"; do curl -s -o /dev/null "http://$web$u"; done; done; }

c0=$(metric_count)
# start pprof CPU capture for the whole window (background), then the load, then trace mid-window
curl -s -m$((DUR+10)) "http://$pp/debug/pprof/profile?seconds=$DUR" -o "$OUT/cpu-${label}.pprof" &
ppid=$!
for _ in $(seq 1 "$CONC"); do worker & done
sleep 2; gmid=$(goroutines)
curl -s -m$((TRACE_SEC+8)) "http://$pp/debug/pprof/trace?seconds=$TRACE_SEC" -o "$OUT/trace-${label}.out" &
wait %2 2>/dev/null; wait   # workers + trace
wait "$ppid" 2>/dev/null    # cpu profile
c1=$(metric_count)
curl -s -m5 "http://$pp/debug/pprof/heap" -o "$OUT/heap-${label}.pprof"

rps=$(awk -v a="$c0" -v b="$c1" -v d="$DUR" 'BEGIN{printf "%.0f", (b-a)/d}')
log "served 2xx delta: $((c1-c0)) over ${DUR}s  => ~${rps} req/s"
log "goroutines under load: ${gmid}  (idle above)"
log "heap inuse top:"; go tool pprof -inuse_space -top -nodecount=6 "$OUT/heap-${label}.pprof" 2>/dev/null | sed -n '5,11p' | tee -a "$res"
log "CPU top (cum):";  go tool pprof -cum -top -nodecount=16 "$OUT/cpu-${label}.pprof" 2>/dev/null | sed -n '7,22p' | tee -a "$res"
log "trace bytes: $(wc -c < "$OUT/trace-${label}.out" 2>/dev/null)"
log ""
