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
# most 200 wallets a day). It builds its own binary, /root/agentmcp-sandbox,
# so the public read-only /mcp's /root/agentmcp is never touched.
set -euo pipefail
REPO=${REPO:-$(pwd)}
GRPC=${GRPC:-localhost:9090}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
KEYDIR=${KEYDIR:-/root/aether-sandbox-key}
USDC="--usdc-path transfer/channel-2 --usdc-base-denom erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d --usdc-issuer Injective"
[ -f "$REPO/cmd/agentmcp/sandbox.go" ] || { echo "run from the repo checkout (no cmd/agentmcp/sandbox.go)" >&2; exit 1; }

echo "== Building from $(git -C "$REPO" log -1 --format='%h %s')"
(cd "$REPO" && go build -o /root/agentmcp-sandbox.new ./cmd/agentmcp && go build -o /root/aetherd-keys.tmp ./cmd/aetherd)
mv /root/agentmcp-sandbox.new /root/agentmcp-sandbox

mkdir -p "$KEYDIR" && chmod 700 "$KEYDIR"
if ! /root/aetherd-keys.tmp keys show sandbox-funder --keyring-backend test --keyring-dir "$KEYDIR" >/dev/null 2>&1; then
  echo "== Creating the funder key (its mnemonic is in $KEYDIR/funder-mnemonic.txt: keep it there or back it up)"
  /root/aetherd-keys.tmp keys add sandbox-funder --keyring-backend test --keyring-dir "$KEYDIR" > "$KEYDIR/funder-mnemonic.txt" 2>&1
  chmod 600 "$KEYDIR/funder-mnemonic.txt"
fi
FUNDER=$(/root/aetherd-keys.tmp keys show sandbox-funder -a --keyring-backend test --keyring-dir "$KEYDIR")
rm -f /root/aetherd-keys.tmp

echo "== Unit"
cat > /etc/systemd/system/aether-sandbox.service <<UNIT
[Unit]
Description=Aether hosted test wallet (custodial testnet wallets over MCP)
After=network-online.target

[Service]
ExecStart=/root/agentmcp-sandbox --sandbox-http 127.0.0.1:8092 --grpc $GRPC --chain-id $CHAIN_ID --rpc "" --keyring-dir $KEYDIR --keyring-backend test --sandbox-funder sandbox-funder --sandbox-secret-file $KEYDIR/token-secret $USDC
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
echo "funder balance: ${balance:-unknown (check: aetherd q bank balances $FUNDER)}"
[ "$health" = 200 ] && [ -n "$tools" ] || { echo "Not answering. Why: journalctl -u aether-sandbox -n 20 --no-pager" >&2; exit 1; }
echo "Running. If the funder has no AETH yet, send it some, e.g. 20 AETH from the faucet key."
