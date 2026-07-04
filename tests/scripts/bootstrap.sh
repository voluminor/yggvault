#!/usr/bin/env bash
# Generate per-node Yggdrasil keys (openssl ed25519 PKCS8 — accepted verbatim by the vault) and render
# each node's config.yml into tmp/tests/<node>/. Brother nodes reference their seed's derived .pk.ygg host.
# Idempotent: existing keys are kept so node identities (.pk.ygg) stay stable across runs.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TDIR="$ROOT/tmp/tests"
NODES="node-a node-b node-c node-d node-e"
# Shared canonical domain across the whole fleet: every node serves the SAME module path
# (vault.test/<key>), so the one committed consumer in tests/stub imports it regardless of which
# node answers — seed or brother. (Domain is the web canonical host; ygg identity is key-derived.)
DOMAIN="vault.test"

mkdir -p "$TDIR/keys" "$TDIR/results" "$TDIR/certs"

# // // // // // // // // //

ygg_host() { # PEM path -> "<hex-ed25519-pubkey>.pk.ygg" (same derivation as mesh.hostFromPublicKey)
  local pem=$1
  echo "$(openssl pkey -in "$pem" -pubout -outform DER 2>/dev/null | tail -c 32 | od -An -tx1 | tr -d ' \n').pk.ygg"
}

for n in $NODES; do
  [ -f "$TDIR/keys/$n.pem" ] || openssl genpkey -algorithm ed25519 -out "$TDIR/keys/$n.pem" 2>/dev/null
done

HOST_A="$(ygg_host "$TDIR/keys/node-a.pem")"
HOST_B="$(ygg_host "$TDIR/keys/node-b.pem")"
HOST_C="$(ygg_host "$TDIR/keys/node-c.pem")"

# // // // // // // // // //

# TLS material for the canonical host vault.test: a self-signed mini-CA + an ECDSA P-256 leaf with
# SAN=vault.test, served by the edge proxy. Clients trust ca.crt. ECDSA P-256 is chosen for universal
# client support (Go/PHP/curl). Idempotent: regenerated only if the leaf is missing.
C="$TDIR/certs"
if [ ! -f "$C/vault.test.crt" ]; then
  openssl ecparam -name prime256v1 -genkey -noout -out "$C/ca.key" 2>/dev/null
  MSYS_NO_PATHCONV=1 openssl req -x509 -new -key "$C/ca.key" -days 825 -subj "/CN=vault.test test CA" -out "$C/ca.crt" 2>/dev/null
  openssl ecparam -name prime256v1 -genkey -noout -out "$C/vault.test.key" 2>/dev/null
  MSYS_NO_PATHCONV=1 openssl req -new -key "$C/vault.test.key" -subj "/CN=vault.test" -out "$C/vault.test.csr" 2>/dev/null
  openssl x509 -req -in "$C/vault.test.csr" -CA "$C/ca.crt" -CAkey "$C/ca.key" -CAcreateserial -days 825 \
    -extfile <(printf 'subjectAltName=DNS:vault.test\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\n') \
    -out "$C/vault.test.crt" 2>/dev/null
  cat "$C/ca.crt" >> "$C/vault.test.crt"   # full chain (leaf + CA) for nginx
  rm -f "$C/vault.test.csr" "$C/ca.srl"
fi

# // // // // // // // // //

# Optional GitHub auth for the SEED nodes: if GITHUB_TOKEN is set in the host env, render it into the
# seeds' config (source.credentials.github) so the GitHub REST API is hit authenticated (5000 req/h vs
# the 60/h unauth ceiling). The vault reads this ONLY from config (no env in the product); this is just
# the harness baking the literal token into the config it generates. Brothers fetch from vault siblings
# over ygg, so they never get it. Empty GITHUB_TOKEN -> anonymous (unchanged behaviour).
GH_CREDS=""
if [ -n "${GITHUB_TOKEN:-}" ]; then
  GH_CREDS=$(cat <<EOF
source:
  credentials:
    github: "${GITHUB_TOKEN}"
EOF
)
fi

NODE_B_EXTRA=$(cat <<EOF
${GH_CREDS}
brother:
  rpc:
    web_enabled: false
    ygg_enabled: false
EOF
)

# // // // // // // // // //

write_cfg() { # $1=node $2=domain $3=hub $4=rescan_interval(opt) $5=extra_yaml(opt)  (stdin = release_mirrors)
  local node=$1 domain=$2 hub=$3 interval=${4:-2m} extra=${5:-}
  # dot-prefixed so `go test ./...` (which ignores .gitignore but skips dot/underscore dirs) never
  # descends into pebble/sqlite storage or logs under tmp/ — those would otherwise break the build tree.
  mkdir -p "$TDIR/$node/.state" "$TDIR/$node/.logs"
  {
    cat <<EOF
logging:
  console:
    enabled: true
    level: "info"
  file:
    enabled: true
    level: "debug"
    dir: "/data/.logs"
    max_size: "50mb"
    max_backups: 3
    max_age: "168h"
    compress: false

web:
  server:
    domain: "${domain}"
    # shared = one plain-TCP listener behind a TLS-terminating edge; the public scheme is always https.
    # Nodes are reachable directly over http:8080 and canonically through the https edge.
    mode: "shared"
    shared:
      listen: "0.0.0.0:8080"

ygg:
  pem_key: "/keys/${node}.pem"
  peers:
    initial: ["tcp://${hub}:7777"]
    max_per_proto: 2
    probe_timeout: "5s"
    refresh_interval: "30s"
    batch_size: 2

storage:
  dir: "/data/.state"

overlay:
  go:
    rewrite_enabled: true

rescan:
  interval: "${interval}"
  initial_depth: 3

profiling:
  enabled: true
  listen: "127.0.0.1:6060"
  max_profile_seconds: 120

metrics:
  web:
    internal: true

${extra}

release_mirrors:
EOF
    cat   # release_mirrors entries from stdin
  } > "$TDIR/$node/config.yml"
}

# Hub split: seeds + node-e on hub-1, the a/b brothers on hub-2 -> every brother link crosses hubs.
# Rescan cadence is sized against GitHub's UNAUTH limit: 60 req/h shared across ONE egress IP (all
# containers + host). Initial ingest is immediate regardless of interval, and the mirrored repos don't
# change during a test, so seeds re-poll only every 10m (2 seeds * a few calls each stays well under 60).
# Brothers pull from a vault sibling (no GitHub) -> a short 1m interval is free and makes the depth-2
# chain converge fast on a cold start. See up.sh's preflight rate-limit check.
# node-a — GIT seed (Go module)   [hub-1, GitHub + GitLab]  -> gets github creds if GITHUB_TOKEN set
# GitLab keys are a separate provider (own rate limit), so ingest works when GitHub's 60/h unauth is exhausted.
write_cfg node-a "$DOMAIN" ygg-hub-1 10m "$GH_CREDS" <<EOF
  errors: "https://github.com/go-faster/errors"
  gitlabgo: "https://gitlab.com/gitlab-org/api/client-go"
  tozderr: "https://gitlab.com/tozd/go/errors"
EOF

# node-b — GIT seed (composer: monolog + its psr/log dependency)   [hub-1, GitHub]  -> gets github creds.
# Its RPC is disabled intentionally: node-d must prove brother sync through the public release API fallback.
write_cfg node-b "$DOMAIN" ygg-hub-1 10m "$NODE_B_EXTRA" <<EOF
  monolog: "https://github.com/Seldaek/monolog"
  log: "https://github.com/php-fig/log"
EOF

# node-c — BROTHER of node-a over ygg   [hub-2 -> cross-hub to a]
write_cfg node-c "$DOMAIN" ygg-hub-2 1m <<EOF
  errors: "http://${HOST_A}/errors"
EOF

# node-d — BROTHER of node-b over ygg   [hub-2 -> cross-hub to b, public API fallback because node-b RPC is off]
write_cfg node-d "$DOMAIN" ygg-hub-2 1m <<EOF
  monolog: "http://${HOST_B}/monolog"
  log: "http://${HOST_B}/log"
EOF

# node-e — BROTHER of node-c over ygg (brother-of-brother)   [hub-1 -> cross-hub to c]
write_cfg node-e "$DOMAIN" ygg-hub-1 1m <<EOF
  errors: "http://${HOST_C}/errors"
EOF

# // // // // // // // // //

{
  echo "domain (all nodes)      : ${DOMAIN}  -> module path vault.test/<key>  (TLS edge terminates it)"
  echo "hubs                    : ygg-hub-2 peers tcp://ygg-hub-1:7777  (peered backbone)"
  echo "node-a (git, Go)  [hub1]: serves ${DOMAIN}/errors   ygg=${HOST_A}"
  echo "node-b (git, php) [hub1]: serves monolog/monolog,psr/log   ygg=${HOST_B}   rpc=off"
  echo "node-c (bro of a) [hub2]: ygg=${HOST_C}  -> http://${HOST_A}/errors   (cross-hub)"
  echo "node-d (bro of b) [hub2]: -> http://${HOST_B}/{monolog,log}   (cross-hub, public API fallback)"
  echo "node-e (bro of c) [hub1]: -> http://${HOST_C}/errors   (cross-hub)"
  echo "edge (TLS)              : https://${DOMAIN}  /errors->node-a  rest->node-b   CA=certs/ca.crt"
} | tee "$TDIR/topology.txt"
echo "[bootstrap] keys + certs + configs written to $TDIR"
