#!/usr/bin/env bash

set -Eeuo pipefail

echo "[HOOK]" "Commit"

run_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root_path=$(cd "$run_dir/.." && pwd)
manifest="$run_dir/values.yml"
gometagen="github.com/amazing-generators/gometagen/cmd/gometagen@latest"
goopenapigen="github.com/amazing-generators/goopenapigen/cmd/goopenapigen@latest"

VERSION=$(go run "$gometagen" version print -source "$manifest")
BRANCH=$(go run "$gometagen" git branch -source "$root_path")

tmp_file="${1}.tmp"
{
  printf "%s [%s]\n\n" "$BRANCH" "$VERSION"
  cat "$1"
} > "$tmp_file"
mv "$tmp_file" "$1"
#############################################################################

(
  cd "$root_path"
  go run "$goopenapigen" manifest sync -source ./yml/openapi -auto-bump patch
  go test -v ./...
)

#############################################################################
exit 0
