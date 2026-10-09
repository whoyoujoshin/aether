#!/usr/bin/env bash
# The payee account for the paid agent services, and their listings in the
# on-chain service directory. Run as root from the repo checkout:
#
#   cd /root/aether-src && bash scripts/agentservices/announce.sh
#
# First run: creates the account (key in /root/aether-services-key, a
# disposable testnet key: it only receives service payments), asks the
# local faucet to fund it, prints its address, and stops if the funds
# haven't arrived yet. Once install.sh and caddy.sh have run and the
# services answer publicly, run it again: it lists each one (1 uaeth to
# the directory address, memo "x402-service:<url>"), waiting for each
# transaction to land before the next.
set -euo pipefail
REPO=${REPO:-$(pwd)}
KEYDIR=${KEYDIR:-/root/aether-services-key}
KEY=${KEY:-services}
PUBLIC=${PUBLIC:-https://explorer.157-245-252-221.sslip.io/svc}
NODE=${NODE:-tcp://127.0.0.1:26657}
FAUCET=${FAUCET:-http://127.0.0.1:8080/request}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
DIRECTORY=aether1q33l05h32falelyk69p79p3ypxwka6yk2sq9e2
LIST="$REPO/scripts/agentservices/services.txt"
[ -f "$LIST" ] || { echo "run from the repo checkout (no $LIST)" >&2; exit 1; }
AETHERD=${AETHERD:-$(systemctl cat aetherd --no-pager 2>/dev/null | sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' | head -1)}
AETHERD=${AETHERD:-$(command -v aetherd || true)}
[ -x "$AETHERD" ] || { echo "can't find aetherd: set AETHERD=/path/to/aetherd" >&2; exit 1; }
kr=(--keyring-backend test --keyring-dir "$KEYDIR")

if ! "$AETHERD" keys show "$KEY" "${kr[@]}" >/dev/null 2>&1; then
  mkdir -p "$KEYDIR" && chmod 700 "$KEYDIR"
  "$AETHERD" keys add "$KEY" "${kr[@]}" --output json > "$KEYDIR/created.json" 2>&1
  chmod 600 "$KEYDIR/created.json"
  echo "created the payee account; its recovery phrase is in $KEYDIR/created.json"
fi
ADDR=$("$AETHERD" keys show "$KEY" -a "${kr[@]}")
echo "payee: $ADDR"

balance() { "$AETHERD" q bank balance "$ADDR" uaeth --node "$NODE" --output json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"]["amount"])' 2>/dev/null || echo 0; }
if [ "$(balance)" -lt 100000 ]; then
  echo "asking the faucet for funds..."
  curl -s -X POST -H 'Content-Type: application/json' -d "{\"address\":\"$ADDR\"}" "$FAUCET"; echo
  for _ in $(seq 1 24); do [ "$(balance)" -ge 100000 ] && break; sleep 5; done
fi
echo "balance: $(balance) uaeth"
[ "$(balance)" -ge 100000 ] || { echo "not funded yet: run this again in a minute" >&2; exit 1; }

if [ "${LIST_SERVICES:-}" != yes ]; then
  echo
  echo "Funded. Next: PAY_TO=$ADDR bash scripts/agentservices/install.sh, then bash scripts/agentservices/caddy.sh,"
  echo "then list them with: LIST_SERVICES=yes bash scripts/agentservices/announce.sh"
  exit 0
fi

while IFS='|' read -r name _; do
  case "$name" in ''|'#'*) continue;; esac
  url="$PUBLIC/$name"
  code=$(curl -s -o /dev/null -w '%{http_code}' "$url/.well-known/x402")
  [ "$code" = 200 ] || { echo "$url/.well-known/x402 answered $code: run install.sh and caddy.sh first" >&2; exit 1; }
  out=$("$AETHERD" tx bank send "$KEY" "$DIRECTORY" 1uaeth --note "x402-service:$url" \
        --chain-id "$CHAIN_ID" --node "$NODE" "${kr[@]}" --gas auto --gas-prices 0.0001uaeth -y --output json)
  hash=$(echo "$out" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("txhash",""))')
  code=$(echo "$out" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("code",0))')
  [ -n "$hash" ] && [ "$code" = 0 ] || { echo "listing $name failed: $out" >&2; exit 1; }
  printf '%-8s %s ' "$name" "$hash"
  for _ in $(seq 1 30); do
    if "$AETHERD" q tx "$hash" --node "$NODE" --output json >/dev/null 2>&1; then echo listed; continue 2; fi
    sleep 3
  done
  echo "not in a block after 90s: check it, then run this again" >&2; exit 1
done < "$LIST"
echo "All five listed. Agents find them with find_services."
