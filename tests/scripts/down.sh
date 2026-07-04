#!/usr/bin/env bash
# Stop and remove the test stack. Caches are Docker-side by design, never in the project:
#   * build cache  -> BuildKit cache mounts (docker storage), shared/fast, reclaimed by `docker builder prune`
#   * verifier run -> container /work, dies with `--rm`
#   * runtime state-> tmp/tests bind mounts (user-owned), survives unless --clean
#
#   (no flags) : stop + remove containers/network, keep tmp/tests state + images + build cache
#   --clean    : also delete tmp/tests (keys, storage, logs, results)
#   --prune    : also remove the built images AND reclaim BuildKit cache (note: builder prune is GLOBAL)
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
clean=0; prune=0
for a in "$@"; do case "$a" in --clean) clean=1 ;; --prune) prune=1 ;; esac; done

docker compose -f "$ROOT/tests/docker-compose.yml" --profile verify down --remove-orphans

if [ "$clean" = 1 ]; then
  rm -rf "$ROOT/tmp/tests"
  echo "[down] removed tmp/tests state (keys + storage gone; identities regenerate next up)"
fi

if [ "$prune" = 1 ]; then
  docker image rm -f yrv:nocgo yrv:cgo yrv-ygghub:latest yrv-verifier:latest 2>/dev/null || true
  echo "[down] removed test images; reclaiming BuildKit cache (GLOBAL across all projects)..."
  docker builder prune -f >/dev/null 2>&1 || true
  echo "[down] build cache reclaimed"
fi

echo "[down] stopped"
