#!/usr/bin/env bash

set -Eeuo pipefail

echo "[HOOK]" "Push"

run_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root_path=$(cd "$run_dir/.." && pwd)
manifest="$run_dir/values.yml"
gometagen="github.com/amazing-generators/gometagen/cmd/gometagen@latest"

#############################################################################

(
  cd "$root_path"
  go work sync
  go generate .

  go list -m -f '{{if .Main}}{{.Dir}}{{end}}' all | while read -r dir; do
    [ -n "$dir" ] || continue
    echo "  -> $dir"
    go -C "$dir" mod tidy
  done
)

OLD_VER=$(go run "$gometagen" version print -source "$manifest")
VERSION=$(go run "$gometagen" version patch -source "$manifest")

(
  cd "$root_path"
  go generate .
)

echo "Updated patch-ver:" "$OLD_VER >> $VERSION"

#############################################################################
exit 0
