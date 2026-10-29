#!/usr/bin/env bash
# Prepares the query node (docs/QUERY-NODE.md): a full node of
# aether-testnet-1 with no validator key, which the seed's public services
# read instead of the seed's validator. Run as root on the new server,
# never on a validator:
#
#   AETHERD_BIN=/root/aetherd-<commit> LISTEN_IP=10.116.0.5 ALLOW_FROM=10.116.0.2 \
#   PEERS=<id>@<seed>:26656,<id>@<sync3>:26656,<id>@<sync4>:26656 \
#   MIN_GAS_PRICES=<the seed's minimum-gas-prices> \
#     bash scripts/querynode/setup.sh
#
# LISTEN_IP is this server's private (VPC) address: RPC and gRPC listen
# only there. ALLOW_FROM is the seed's private address, the only one the
# firewall lets reach them. MIN_GAS_PRICES must be the seed's own, so a
# transaction the seed's services broadcast here is admitted exactly as
# the seed admitted it before.
#
# It installs the binary as /usr/local/bin/aetherd-query, makes the home
# ($HOME_DIR, /root/.aether-query) with a fresh key that is in no
# validator set, sets the config, writes aether-query.service (enabled,
# not started) and the firewall. Safe to run again: an existing home
# keeps its keys and only has its config set again. The chain data and
# genesis.json are copied from sync4 next (docs/QUERY-NODE.md, step 4).
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

AETHERD_BIN=${AETHERD_BIN:?set AETHERD_BIN to the aetherd the validators run (sha256 matching theirs)}
LISTEN_IP=${LISTEN_IP:?set LISTEN_IP to this server private address}
ALLOW_FROM=${ALLOW_FROM:?set ALLOW_FROM to the seed private address}
PEERS=${PEERS:?set PEERS to id@host:26656 of the seed, sync3 and sync4, comma-separated}
MIN_GAS_PRICES=${MIN_GAS_PRICES:?set MIN_GAS_PRICES to the seed own minimum-gas-prices}
HOME_DIR=${HOME_DIR:-/root/.aether-query}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
IAVL_CACHE=${IAVL_CACHE:-200000}
RPC_CORS=${RPC_CORS:-}
BIN=/usr/local/bin/aetherd-query
UNIT=/etc/systemd/system/aether-query.service

die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }
say() { printf '\n== %s\n' "$*"; }

[[ $EUID -eq 0 ]] || die "run as root"
if systemctl is-active --quiet aetherd 2>/dev/null || [[ -f /root/.aether/config/priv_validator_key.json ]]; then
  die "this server runs (or has run) a validator; the query node goes on a server of its own"
fi
[[ "$LISTEN_IP" =~ ^[0-9.]+$ && "$ALLOW_FROM" =~ ^[0-9.]+$ ]] || die "LISTEN_IP and ALLOW_FROM must be IPv4 addresses"
[[ "$PEERS" =~ ^[0-9a-f]{40}@[A-Za-z0-9.-]+:[0-9]+(,[0-9a-f]{40}@[A-Za-z0-9.-]+:[0-9]+)*$ ]] || die "PEERS must be id@host:port,..."
[[ "$MIN_GAS_PRICES" =~ ^[0-9.a-zA-Z/,]*$ ]] || die "MIN_GAS_PRICES looks wrong: $MIN_GAS_PRICES"
ip -4 addr show | grep -q "inet $LISTEN_IP/" || die "$LISTEN_IP isn't an address of this server"
command -v jq >/dev/null || die "jq is not installed (apt-get install -y jq)"

say "Installing $AETHERD_BIN as $BIN"
sha256sum "$AETHERD_BIN"
install -m 755 "$AETHERD_BIN" "$BIN.new" && mv "$BIN.new" "$BIN"

if [[ -f "$HOME_DIR/config/config.toml" ]]; then
  say "$HOME_DIR exists; keeping its keys, setting its config again"
else
  say "Making $HOME_DIR with a fresh key"
  "$BIN" init aether-query --chain-id "$CHAIN_ID" --home "$HOME_DIR" >/dev/null 2>&1
fi

say "Config"
C=$HOME_DIR/config/config.toml
A=$HOME_DIR/config/app.toml
toml_set "$C" "" moniker '"aether-query"'
toml_set "$C" rpc laddr "\"tcp://$LISTEN_IP:26657\""
[[ -n "$RPC_CORS" ]] && toml_set "$C" rpc cors_allowed_origins "$RPC_CORS"
toml_set "$C" p2p persistent_peers "\"$PEERS\""
toml_set "$C" consensus timeout_commit '"5s"'
# Paywalls, the facilitator and the job board find transactions by hash and by event.
toml_set "$C" tx_index indexer '"kv"'
toml_set "$A" "" minimum-gas-prices "\"$MIN_GAS_PRICES\""
toml_set "$A" "" iavl-cache-size "$IAVL_CACHE"
toml_set "$A" grpc enable true
toml_set "$A" grpc address "\"$LISTEN_IP:9090\""
for k in "rpc laddr" "p2p persistent_peers" "tx_index indexer"; do
  set -- $k
  printf '%-28s %s\n' "config.toml [$1] $2" "$(toml_get "$C" "$1" "$2")"
done
for k in " minimum-gas-prices" " iavl-cache-size" "grpc address"; do
  sec=${k% *} key=${k##* }
  printf '%-28s %s\n' "app.toml [${sec}] $key" "$(toml_get "$A" "$sec" "$key")"
done

say "Unit $UNIT (enabled, not started)"
mem_kib=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
limit_mib=$((mem_kib * 7 / 10 / 1024))
cat >"$UNIT" <<UNIT
[Unit]
Description=Aether query node (full node, no validator key; docs/QUERY-NODE.md)
After=network-online.target
Wants=network-online.target

[Service]
User=root
ExecStart=$BIN start --home $HOME_DIR
Environment=GOMEMLIMIT=${limit_mib}MiB
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable aether-query >/dev/null 2>&1
echo "GOMEMLIMIT=${limit_mib}MiB (70% of this server's memory)"

if command -v ufw >/dev/null; then
  say "Firewall: SSH from anywhere, RPC and gRPC from $ALLOW_FROM only"
  ufw allow 22/tcp >/dev/null
  ufw allow from "$ALLOW_FROM" to "$LISTEN_IP" port 26657 proto tcp >/dev/null
  ufw allow from "$ALLOW_FROM" to "$LISTEN_IP" port 9090 proto tcp >/dev/null
  ufw --force enable >/dev/null
  ufw status | sed 's/^/  /'
else
  echo "no ufw: make sure only $ALLOW_FROM can reach $LISTEN_IP:26657 and :9090"
fi

say "Next"
if [[ -d "$HOME_DIR/data/blockstore.db" ]]; then
  echo "Chain data is in place: systemctl start aether-query"
else
  echo "Copy the chain data and genesis.json from sync4 (docs/QUERY-NODE.md, step 4), then: systemctl start aether-query"
fi
