#!/usr/bin/env bash
# Drives the full system bench: brings the tests/ stack up, characterizes each load profile (pprof hot
# funcs + throughput), then sweeps node-a's storage.hot.verify_on_read (the serve-path verify lever)
# under the read-heavy hotzip profile. Results → tmp/tests/bench-results/system-*.txt + pprof artifacts.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$ROOT"
SYS="$ROOT/tests/bench/system.sh"
TDIR="$ROOT/tmp/tests"
COMPOSE=(docker compose -f tests/docker-compose.yml)
export HOST_UID="$(id -u)" HOST_GID="$(id -g)"

# writeNodeA <verify_on_read> <pebble_compression> — full node-a config with the swept knobs.
writeNodeA(){
  local verify=$1 comp=$2
  mkdir -p "$TDIR/node-a/.state" "$TDIR/node-a/.logs"
  cat > "$TDIR/node-a/config.yml" <<EOF
logging:
  console: {enabled: true, level: "info"}
web:
  server: {domain: "vault.test", mode: "single", single: {proto: "http", listen: "0.0.0.0:8080"}}
metrics:
  web: {internal: true}
ygg:
  pem_key: "/keys/node-a.pem"
  peers: {initial: ["tcp://ygg-hub:7777"], max_per_proto: 2, probe_timeout: "5s", refresh_interval: "30s", batch_size: 2}
storage:
  dir: "/data/.state"
  pebble: {compression: "${comp}"}
  hot: {verify_on_read: "${verify}"}
overlay:
  go: {rewrite_enabled: true}
rescan: {interval: "2m", initial_depth: 3}
profiling: {enabled: true, listen: "127.0.0.1:6060", max_profile_seconds: 120}
release_mirrors:
  errors: "https://github.com/go-faster/errors"
EOF
}

waitKey(){ for _ in $(seq 1 40); do curl -fsS -m4 "http://127.0.0.1:18081/errors/@v/list" 2>/dev/null | grep -q . && return 0; sleep 3; done; return 1; }

echo "[sweep] bring stack up"
bash tests/scripts/up.sh --no-build >/dev/null 2>&1 || true
waitKey || { echo "node-a never ingested"; exit 1; }

echo "[sweep] === profile characterization (default config) ==="
DUR=20 CONC=24 bash "$SYS" hotzip   18081 16060 hotzip-default
DUR=20 CONC=24 bash "$SYS" metadata 18081 16060 metadata
DUR=20 CONC=24 bash "$SYS" composer 18082 16061 composer

echo "[sweep] === hot.verify_on_read sweep (read-heavy hotzip, node-a) ==="
for v in always sampled never; do
  writeNodeA "$v" "snappy"
  "${COMPOSE[@]}" restart node-a >/dev/null 2>&1
  waitKey || { echo "node-a not ready for verify=$v"; continue; }
  DUR=20 CONC=24 bash "$SYS" hotzip 18081 16060 "verify-$v"
done

echo "[sweep] restore default node-a config"
bash tests/scripts/bootstrap.sh >/dev/null 2>&1
"${COMPOSE[@]}" restart node-a >/dev/null 2>&1 || true
echo "[sweep] done -> $TDIR/bench-results/system-*.txt"
