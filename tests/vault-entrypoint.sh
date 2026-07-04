#!/bin/sh
# Vault entrypoint. The pprof/runtime-trace listener must bind to loopback (the config validator
# rejects any non-loopback/unspecified bind — by design). A published Docker port maps to the
# container's routable interface, not 127.0.0.1, so we bridge it: socat forwards
# 0.0.0.0:${PPROF_FORWARD_PORT} -> 127.0.0.1:${PPROF_PORT}. Disabled if PPROF_FORWARD_PORT is unset.
set -eu

if [ -n "${PPROF_FORWARD_PORT:-}" ] && command -v socat >/dev/null 2>&1; then
  socat "TCP-LISTEN:${PPROF_FORWARD_PORT},fork,reuseaddr" "TCP:127.0.0.1:${PPROF_PORT:-6060}" &
fi

# exec so the vault is PID 1 and receives SIGTERM directly (graceful shutdown intact).
exec /usr/local/bin/yggvault "${@:-/data/config.yml}"
