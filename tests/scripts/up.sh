#!/usr/bin/env bash
# Build images (project is COPYed into the image and built there — the host working tree, incl.
# go.mod/go.sum, is never touched), bootstrap keys+configs into tmp/tests, then start hub + 5 nodes.
#   --no-build : reuse existing images
#   --verify   : run the verifier after startup (go get/build + composer install against the nodes)
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export DOCKER_BUILDKIT=1
# Containers run as the host user (see docker-compose.yml) so tmp/ state stays user-owned.
export HOST_UID="$(id -u)" HOST_GID="$(id -g)"
COMPOSE=(docker compose -f tests/docker-compose.yml)

# GitHub rate-limit preflight. Unauthenticated = 60 req/h shared across the host's single egress IP; a
# GITHUB_TOKEN (baked into the seeds' config by bootstrap) lifts it to 5000/h. Warn early when the
# budget is low so a seed that can't ingest reads as a rate limit, not a stack failure. The /rate_limit
# endpoint itself does NOT consume the budget.
gh_rate_preflight() {
  local rem auth="unauthenticated (60/h)"; local hdr=()
  if [ -n "${GITHUB_TOKEN:-}" ]; then hdr=(-H "Authorization: Bearer ${GITHUB_TOKEN}"); auth="authenticated (5000/h)"; fi
  # || true: a failed/blocked curl must degrade to the "skipped" path below, not abort the whole
  # stand via `set -e` (the assignment inherits the pipeline's non-zero exit under pipefail).
  rem=$(curl -fsS -m8 "${hdr[@]}" https://api.github.com/rate_limit 2>/dev/null \
        | grep -m1 -oE '"remaining"[: ]+[0-9]+' | grep -oE '[0-9]+' || true)
  if [ -z "$rem" ]; then echo "[up] GitHub rate-limit preflight: skipped (no egress?)"; return 0; fi
  echo "[up] GitHub rate-limit preflight: ${auth}, remaining=${rem}"
  if [ -z "${GITHUB_TOKEN:-}" ] && [ "$rem" -lt 15 ]; then
    echo "[up] WARNING: low unauth budget (${rem}/60). Seeds (node-a/b) may fail to ingest; export"
    echo "[up]          GITHUB_TOKEN before up.sh for 5000/h, or wait for the hourly reset."
  fi
}

build=1; verify=0
for a in "$@"; do case "$a" in --no-build) build=0 ;; --verify) verify=1 ;; esac; done

if [ "$build" = 1 ]; then
  echo "[up] building vault image (no-CGO / static / modernc)"
  docker build -f tests/vault.Dockerfile --build-arg CGO_ENABLED=0 --build-arg RUNTIME=alpine:3.20         -t yrv:nocgo .
  echo "[up] building vault image (CGO / glibc / mattn)"
  docker build -f tests/vault.Dockerfile --build-arg CGO_ENABLED=1 --build-arg RUNTIME=debian:bookworm-slim -t yrv:cgo   .
  echo "[up] building ygg hub + verifier"
  docker build -f tests/ygghub.Dockerfile   -t yrv-ygghub:latest   .
  docker build -f tests/verifier.Dockerfile -t yrv-verifier:latest .
fi

gh_rate_preflight
bash tests/scripts/bootstrap.sh
echo "[up] starting 2 hubs + 5 nodes + TLS edge"
"${COMPOSE[@]}" up -d ygg-hub-1 ygg-hub-2 node-a node-b node-c node-d node-e edge
"${COMPOSE[@]}" ps

verify_rc=0
if [ "$verify" = 1 ]; then
  echo "[up] running verifier (brother sync over ygg can take a couple of rescan cycles; web RPC is checked separately)"
  "${COMPOSE[@]}" run --rm verifier || verify_rc=$?
  echo "[up] results -> tmp/tests/results/verify-results.txt"
  if [ "$verify_rc" -ne 0 ]; then
    echo "[up] WARNING: verifier FAILED (exit ${verify_rc}); inspect results above before trusting this run"
  fi
fi
echo "[up] done. node web: 18081..18085 -> :8080 | https://vault.test -> 127.0.0.1:18443 (edge)"
echo "[up] logs+storage: tmp/tests/<node>/ | certs: tmp/tests/certs/ | topology: tmp/tests/topology.txt"
exit "$verify_rc"
