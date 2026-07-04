#!/bin/sh
# Generate an ephemeral yggdrasil config with a static TCP Listen and no TUN (IfName=none) so the
# node runs unprivileged and only relays peer traffic between the vault leaf-nodes.
#
# YGG_PEERS (optional, space/comma-separated tcp:// URIs): outbound peers this hub dials. Used to
# build a peered backbone — e.g. hub-2 dials hub-1 so leaf nodes on different hubs route across the
# inter-hub link (multi-hop mesh), exercising the realistic two-hub topology.
set -eu

CONF=/tmp/ygg.conf.json
LISTEN_PORT="${YGG_LISTEN_PORT:-7777}"
NAME="${YGG_NAME:-ygg-hub}"

# Space/comma-separated YGG_PEERS -> JSON array (empty -> []).
PEERS_JSON='[]'
if [ -n "${YGG_PEERS:-}" ]; then
  PEERS_JSON=$(printf '%s' "$YGG_PEERS" | tr ', ' '\n\n' | grep -v '^$' | jq -R . | jq -cs .)
fi

yggdrasil -genconf -json > "$CONF"
jq --arg l "tcp://0.0.0.0:${LISTEN_PORT}" --arg n "$NAME" --argjson peers "$PEERS_JSON" \
   '.Listen=[$l] | .Peers=$peers | .IfName="none" | .AdminListen="none" | .MulticastInterfaces=[] | .NodeInfo={"name":$n}' \
   "$CONF" > "$CONF.tmp" && mv "$CONF.tmp" "$CONF"

echo "[${NAME}] starting, Listen tcp://0.0.0.0:${LISTEN_PORT}, IfName=none, peers=${YGG_PEERS:-<none>}"
exec yggdrasil -useconffile "$CONF" -loglevel info
