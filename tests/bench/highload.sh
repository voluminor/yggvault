#!/usr/bin/env bash
# Focused high-load run: keeps read-heavy serving saturated with keepalive load, not curl-per-request.
# Uses a large artifact (monolog universal zip ~192 KiB, node-b), compares hot.verify_on_read modes,
# and adds a metadata contrast. Captures RPS/p50/p99, CPU saturation, goroutines, and RAM.
# Intentionally short: minutes, not hours. Results go to tmp/tests/bench-results/highload.txt.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$ROOT"
TDIR="$ROOT/tmp/tests"; OUT="$TDIR/bench-results"; mkdir -p "$OUT"
RES="$OUT/highload.txt"; : > "$RES"
COMPOSE=(docker compose -f tests/docker-compose.yml)
export HOST_UID="$(id -u)" HOST_GID="$(id -g)"
CONC="${CONC:-200}"; DUR="${DUR:-20}"
log(){ echo "$*" | tee -a "$RES"; }

writeNodeB(){ # $1=hot.verify_on_read
  mkdir -p "$TDIR/node-b/.state" "$TDIR/node-b/.logs"
  cat > "$TDIR/node-b/config.yml" <<EOF
logging: {console: {enabled: true, level: "warn"}}
web: {server: {domain: "vault.test", mode: "single", single: {proto: "http", listen: "0.0.0.0:8080"}}}
metrics: {web: {internal: true}}
ygg:
  pem_key: "/keys/node-b.pem"
  peers: {initial: ["tcp://ygg-hub:7777"], max_per_proto: 2, probe_timeout: "5s", refresh_interval: "30s", batch_size: 2}
storage:
  dir: "/data/.state"
  hot: {verify_on_read: "$1"}
overlay: {go: {rewrite_enabled: true}}
rescan: {interval: "6h", initial_depth: 3}
profiling: {enabled: true, listen: "127.0.0.1:6060", max_profile_seconds: 120}
release_mirrors:
  monolog: "https://github.com/Seldaek/monolog"
  log: "https://github.com/php-fig/log"
EOF
}
waitB(){ for _ in $(seq 1 40); do curl -fsS -m4 http://127.0.0.1:18082/monolog/3.10.0.zip -o /dev/null && return 0; sleep 3; done; return 1; }

run_load(){ # $1=label $2=url $3=pprofport
  local lbl=$1 url=$2 pp=$3
  log "---- $lbl : $url (c=$CONC d=${DUR}s) ----"
  curl -s -m$((DUR+8)) "http://127.0.0.1:$pp/debug/pprof/profile?seconds=$((DUR-4))" -o "$OUT/cpu-$lbl.pprof" &
  local ppid=$!
  sleep 3
  ( docker stats --no-stream --format '{{.MemUsage}} cpu={{.CPUPerc}}' yrv-node-b yrv-node-a 2>/dev/null | head -1 ) > /tmp/hl-stats &
  go run ./tests/bench/loadgen -url "$url" -c "$CONC" -d "$DUR" 2>&1 | tee -a "$RES"
  wait "$ppid" 2>/dev/null
  log "node mem/cpu under load: $(cat /tmp/hl-stats 2>/dev/null)"
  log "goroutines: $(curl -fsS -m4 http://127.0.0.1:$pp/debug/pprof/goroutine?debug=1 2>/dev/null | sed -n '1s/.*total //p')"
  log "CPU top-3: $(go tool pprof -cum -top -nodecount=8 "$OUT/cpu-$lbl.pprof" 2>/dev/null | sed -n '7,9p' | awk '{print $NF}' | tr '\n' ' ')"
  log ""
}

log "== high-load $(date -u +%FT%TZ) | conc=$CONC dur=${DUR}s =="
bash tests/scripts/up.sh --no-build >/dev/null 2>&1 || true

log "### read-heavy (monolog universal ~192KB, node-b) × hot.verify_on_read ###"
for v in sampled always never; do
  writeNodeB "$v"; "${COMPOSE[@]}" restart node-b >/dev/null 2>&1; waitB || { log "node-b not ready ($v)"; continue; }
  run_load "rh-$v" "http://127.0.0.1:18082/monolog/3.10.0.zip" 16061
done

log "### metadata contrast (catalog.json, node-a, cache hit) ###"
run_load "meta" "http://127.0.0.1:18081/catalog.json" 16060

bash tests/scripts/bootstrap.sh >/dev/null 2>&1; "${COMPOSE[@]}" restart node-b >/dev/null 2>&1 || true
log "[highload] done -> $RES"
