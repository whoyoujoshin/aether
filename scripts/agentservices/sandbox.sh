#!/usr/bin/env bash
# Installs (or updates) the hosted test wallet on the seed: agentmcp
# --sandbox-http on 127.0.0.1:8092, a public MCP server that gives any
# caller a small custodial testnet wallet (docs/TEST-WALLET.md). caddy.sh
# routes https://explorer.157-245-252-221.sslip.io/sandbox/mcp to it.
# Run as root from the repo checkout:
#
#   cd /root/aether-src && bash scripts/agentservices/sandbox.sh
#
# The first run creates the funder key and the token secret in
# /root/aether-sandbox-key and prints the funder's address: send it test
# AETH (each new wallet takes --sandbox-grant, 0.01 AETH by default; at
# most 200 wallets a day). Each also gets 0.01 test USDC while the funder
# holds some (--sandbox-grant-usdc; benchmark wallets never do): send the
# funder Circle's testnet USDC over IBC from Injective (docs/TEST-WALLET.md).
# It installs its own binary, /root/agentmcp-sandbox,
# so the public read-only /mcp's /root/agentmcp is never touched. The seed
# is a validator, so build on a workstation (scripts/prebuild.sh) and pass
# PREBUILT=<that folder>; it won't compile where aetherd runs unless
# ALLOW_BUILD_ON_VALIDATOR=yes.
#
# It also creates $KEYDIR/bench-key: callers that send it as
# X-Aether-Bench-Key draw new wallets from the payments benchmark's own
# allowance (200 a day, enough for 15 thirteen-task runs) instead of the per-caller one (docs/BENCHMARK.md).
# Hand it to whoever runs the benchmark as AETHER_BENCH_KEY, not in a chat.
set -euo pipefail
REPO=${REPO:-$(pwd)}
# Once scripts/querynode/point.sh has moved the services to the query node
# (docs/QUERY-NODE.md), redeploys keep them there.
QUERY_ENV=${QUERY_ENV:-/root/aether-query.env}
[ -r "$QUERY_ENV" ] && . "$QUERY_ENV"
GRPC=${GRPC:-${QUERY_GRPC:-localhost:9090}}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
KEYDIR=${KEYDIR:-/root/aether-sandbox-key}
USDC="--usdc-path transfer/channel-2 --usdc-base-denom erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d --usdc-issuer Injective"
[ -f "$REPO/cmd/agentmcp/sandbox.go" ] || { echo "run from the repo checkout (no cmd/agentmcp/sandbox.go)" >&2; exit 1; }
. "$(dirname "$0")/../lib/prebuilt.sh"
prebuilt_check
no_build_on_validator

[ -n "${PREBUILT:-}" ] || echo "== Building from $(git -C "$REPO" log -1 --format='%h %s')"
place_bin agentmcp /root/agentmcp-sandbox.new ./cmd/agentmcp
place_bin aetherd /root/aetherd-keys.tmp ./cmd/aetherd
mv /root/agentmcp-sandbox.new /root/agentmcp-sandbox

mkdir -p "$KEYDIR" && chmod 700 "$KEYDIR"
if ! /root/aetherd-keys.tmp keys show sandbox-funder --keyring-backend test --keyring-dir "$KEYDIR" >/dev/null 2>&1; then
  echo "== Creating the funder key (its mnemonic is in $KEYDIR/funder-mnemonic.txt: keep it there or back it up)"
  /root/aetherd-keys.tmp keys add sandbox-funder --keyring-backend test --keyring-dir "$KEYDIR" > "$KEYDIR/funder-mnemonic.txt" 2>&1
  chmod 600 "$KEYDIR/funder-mnemonic.txt"
fi
FUNDER=$(/root/aetherd-keys.tmp keys show sandbox-funder -a --keyring-backend test --keyring-dir "$KEYDIR")
if [ ! -s "$KEYDIR/bench-key" ]; then
  echo "== Creating the benchmark key ($KEYDIR/bench-key)"
  (umask 077 && openssl rand -hex 24 > "$KEYDIR/bench-key")
fi

# The payments benchmark's decoys (decoys.sh), shown only to bench callers.
LISTINGS=""
[ -s "$KEYDIR/bench-listings" ] && LISTINGS="--sandbox-bench-listings $KEYDIR/bench-listings"

echo "== Unit"
cat > /etc/systemd/system/aether-sandbox.service <<UNIT
[Unit]
Description=Aether hosted test wallet (custodial testnet wallets over MCP)
After=network-online.target

[Service]
ExecStart=/root/agentmcp-sandbox --sandbox-http 127.0.0.1:8092 --grpc $GRPC --chain-id $CHAIN_ID --rpc "" --keyring-dir $KEYDIR --keyring-backend test --sandbox-funder sandbox-funder --sandbox-secret-file $KEYDIR/token-secret --sandbox-bench-key-file $KEYDIR/bench-key --sandbox-bench-per-day 200 $LISTINGS --sandbox-grant-usdc "${USDC_GRANT:-0.01 USDC}" $USDC
Environment=HOME=/root
Restart=always
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable aether-sandbox >/dev/null 2>&1
systemctl restart aether-sandbox

echo "== Check"
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$1" || true; }
for _ in $(seq 1 10); do [ "$(code http://127.0.0.1:8092/healthz)" = 200 ] && break; sleep 1; done
health=$(code http://127.0.0.1:8092/healthz)
tools=$(curl -s --max-time 5 -X POST http://127.0.0.1:8092/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | grep -o '"create_test_wallet"' | head -1 || true)
echo "healthz $health, tools/list ${tools:-missing}"
echo "funder: $FUNDER"
balance=$(curl -s --max-time 5 "http://127.0.0.1:1317/cosmos/bank/v1beta1/balances/$FUNDER/by_denom?denom=uaeth" | grep -o '"amount":"[0-9]*"' || true)
[ -n "$balance" ] || balance=$(timeout 10 /root/aetherd-keys.tmp q bank balance "$FUNDER" uaeth --node "${NODE:-tcp://127.0.0.1:26657}" -o json 2>/dev/null | grep -o '"amount":"[0-9]*"' || true)
USDC_DENOM=ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C
usdc=$(curl -s --max-time 5 "http://127.0.0.1:1317/cosmos/bank/v1beta1/balances/$FUNDER/by_denom?denom=$USDC_DENOM" | grep -o '"amount":"[0-9]*"' || true)
[ -n "$usdc" ] || usdc=$(timeout 10 /root/aetherd-keys.tmp q bank balance "$FUNDER" "$USDC_DENOM" --node "${NODE:-tcp://127.0.0.1:26657}" -o json 2>/dev/null | grep -o '"amount":"[0-9]*"' || true)
rm -f /root/aetherd-keys.tmp
echo "funder balance: ${balance:-unknown (check: aetherd q bank balances $FUNDER)}"
echo "funder USDC (base units, 1 USDC = 1000000): ${usdc:-unknown}"
[ "$health" = 200 ] && [ -n "$tools" ] || { echo "Not answering. Why: journalctl -u aether-sandbox -n 20 --no-pager" >&2; exit 1; }
echo "Running. If the funder has no AETH yet, send it some, e.g. 20 AETH from the faucet key."
