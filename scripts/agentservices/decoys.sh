#!/usr/bin/env bash
# Sets up the payments benchmark's decoys on the seed (docs/BENCHMARK-V3.md):
# the decoy account, whose address the decoys name and which receives
# whatever an agent is talked into paying them, and the test wallet's list
# of bench-only listings. Run as root from the repo checkout, then rerun
# install.sh, caddy.sh and sandbox.sh, which pick both up:
#
#   cd /root/aether-src && bash scripts/agentservices/decoys.sh
#
# SWEEP=yes sends what the decoy account holds back to the test wallet's
# funder (keeping a little for the fee). The decoys are never announced in
# the on-chain directory.
set -euo pipefail
REPO=${REPO:-$(pwd)}
DECOY_KEYDIR=${DECOY_KEYDIR:-/root/aether-decoy-key}
SANDBOX_KEYDIR=${SANDBOX_KEYDIR:-/root/aether-sandbox-key}
PUBLIC=${PUBLIC:-https://explorer.157-245-252-221.sslip.io/svc}
NODE=${NODE:-tcp://127.0.0.1:26657}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
LIST="$REPO/scripts/agentservices/decoys.txt"
[ -f "$LIST" ] || { echo "run from the repo checkout (no $LIST)" >&2; exit 1; }
[ -d "$SANDBOX_KEYDIR" ] || { echo "no $SANDBOX_KEYDIR: run sandbox.sh first" >&2; exit 1; }

(cd "$REPO" && go build -o /root/aetherd-keys.tmp ./cmd/aetherd)
trap 'rm -f /root/aetherd-keys.tmp' EXIT
kr=(--keyring-backend test --keyring-dir "$DECOY_KEYDIR")
mkdir -p "$DECOY_KEYDIR" && chmod 700 "$DECOY_KEYDIR"
if ! /root/aetherd-keys.tmp keys show decoy "${kr[@]}" >/dev/null 2>&1; then
  echo "== Creating the decoy key (its mnemonic is in $DECOY_KEYDIR/decoy-mnemonic.txt)"
  /root/aetherd-keys.tmp keys add decoy "${kr[@]}" > "$DECOY_KEYDIR/decoy-mnemonic.txt" 2>&1
  chmod 600 "$DECOY_KEYDIR/decoy-mnemonic.txt"
fi
DECOY=$(/root/aetherd-keys.tmp keys show decoy -a "${kr[@]}")
echo "$DECOY" > "$DECOY_KEYDIR/address"
echo "decoy: $DECOY"

echo "== Bench-only listings ($SANDBOX_KEYDIR/bench-listings)"
{
  echo "# The payments benchmark's decoys: shown only to bench-key callers that name them."
  while IFS='|' read -r name _; do
    case "$name" in ''|'#'*) continue;; esac
    echo "$name $PUBLIC/$name"
  done < "$LIST"
} > "$SANDBOX_KEYDIR/bench-listings"
cat "$SANDBOX_KEYDIR/bench-listings"

if [ "${SWEEP:-}" = yes ]; then
  FUNDER=$(/root/aetherd-keys.tmp keys show sandbox-funder -a --keyring-backend test --keyring-dir "$SANDBOX_KEYDIR")
  bal=$(/root/aetherd-keys.tmp q bank balance "$DECOY" uaeth --node "$NODE" -o json | python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"]["amount"])')
  if [ "$bal" -gt 1000 ]; then
    amt=$((bal - 1000))
    echo "== Sweeping ${amt}uaeth to the funder $FUNDER"
    /root/aetherd-keys.tmp tx bank send decoy "$FUNDER" "${amt}uaeth" "${kr[@]}" --chain-id "$CHAIN_ID" --node "$NODE" \
      --gas auto --gas-prices 0.0001uaeth -y -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print("tx", d.get("txhash"), "code", d.get("code"))'
  else
    echo "== Nothing to sweep (${bal}uaeth)"
  fi
fi
echo "Next: bash scripts/agentservices/install.sh (PAY_TO as before), caddy.sh, then sandbox.sh."
