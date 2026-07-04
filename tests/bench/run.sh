#!/usr/bin/env bash
# Storage-engine OAT microbench, in a container (no host/tmp pollution; project COPYed in, dataset
# mounted read-only). Builds CGO=0 and CGO=1 images and runs the matrix; JSON → tmp/tests/bench-results.
#   DATASET=<dir>   dataset root (default ~/go/pkg/mod; cache/ subtree is skipped)
#   REPS=<n>        repetitions per point (median-of-n; default 3)
#   RAW_CAP_MB=<n>  dataset raw cap (default 150)
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export DOCKER_BUILDKIT=1
DATASET="${DATASET:-$HOME/go/pkg/mod}"
OUT="$ROOT/tmp/tests/bench-results"
mkdir -p "$OUT"

echo "[bench] building images (project COPYed in; host go.mod untouched)…"
docker build -q -f tests/bench/Dockerfile --build-arg CGO_ENABLED=0 -t yrv-bench:nocgo . >/dev/null
docker build -q -f tests/bench/Dockerfile --build-arg CGO_ENABLED=1 -t yrv-bench:cgo   . >/dev/null

common=(--rm -u "$(id -u):$(id -g)" -v "$DATASET:/dataset:ro"
        -e REPS="${REPS:-3}" -e RAW_CAP_MB="${RAW_CAP_MB:-150}" -e FILE_CAP_MB="${FILE_CAP_MB:-20}")

echo "[bench] CGO=0 full matrix (compression/memtable/cache/blobsize)…"
docker run "${common[@]}" yrv-bench:nocgo > "$OUT/storage-nocgo.json"
echo "[bench] CGO=1 compression axis (zstd-codec interaction)…"
docker run "${common[@]}" -e AXES=compression yrv-bench:cgo > "$OUT/storage-cgo.json"

echo "[bench] done -> $OUT/storage-{nocgo,cgo}.json"
