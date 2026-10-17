#!/usr/bin/env bash
# Runs the payments benchmark end to end against a local devnet, before a
# task set goes to the seed: the paid services and the benchmark's decoys
# behind their paywalls, the hosted test wallet with a bench key, then the
# reference and careless agents (docs/BENCHMARK.md). Everything is built
# from this checkout and stopped when the script ends.
#
#   SERVICES_KEYRING=<dir> FUNDER_KEYRING=<dir> bash bench/devnet.sh
#
# The devnet must be running (its gRPC at $GRPC). SERVICES_KEYRING holds a
# funded test-keyring key named "services", the services' payee, which
# lists them in the directory; FUNDER_KEYRING holds a funded one named
# "sandbox-funder", which funds the test wallets. Ports are the seed's plus
# $OFFSET (10000), so nothing collides with a real deployment. Results go
# to $OUT/results.
set -euo pipefail
REPO=$(cd "$(dirname "$0")/.." && pwd)
OUT=${OUT:-$(mktemp -d)}
CHAIN_ID=${CHAIN_ID:-aether-devnet-1}
GRPC=${GRPC:-127.0.0.1:9090}
OFFSET=${OFFSET:-10000}
AGENTS=${AGENTS:-scripted careless}
SERVICES_KEYRING=${SERVICES_KEYRING:?a keyring dir with a funded "services" key}
FUNDER_KEYRING=${FUNDER_KEYRING:?a keyring dir with a funded "sandbox-funder" key}
USDC="--usdc-path transfer/channel-2 --usdc-base-denom erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d --usdc-issuer Injective"
BIN=$OUT/bin
mkdir -p "$BIN" "$OUT/results"
echo "== $OUT"

echo "== Building from $(git -C "$REPO" log -1 --format='%h %s')"
(cd "$REPO" && for c in aetherd agentservices paywall agentmcp; do go build -o "$BIN/$c" ./cmd/$c; done)

pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true' EXIT
run() { local log=$1; shift; "$@" >"$OUT/$log.log" 2>&1 & pids+=($!); }
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$1" || true; }
wait_for() { for _ in $(seq 1 30); do [ "$(code "$1")" = 200 ] && return 0; sleep 1; done; echo "not answering: $1" >&2; exit 1; }

kr=(--keyring-backend test)
"$BIN/aetherd" keys show services -a "${kr[@]}" --keyring-dir "$SERVICES_KEYRING" >/dev/null
if ! "$BIN/aetherd" keys show decoy "${kr[@]}" --keyring-dir "$OUT/decoy-key" >/dev/null 2>&1; then
  "$BIN/aetherd" keys add decoy "${kr[@]}" --keyring-dir "$OUT/decoy-key" >/dev/null 2>&1
fi
PAYEE=$("$BIN/aetherd" keys show services -a "${kr[@]}" --keyring-dir "$SERVICES_KEYRING")
DECOY=$("$BIN/aetherd" keys show decoy -a "${kr[@]}" --keyring-dir "$OUT/decoy-key")
echo "payee $PAYEE, decoy $DECOY"

UP=$((8500 + OFFSET))
run agentservices "$BIN/agentservices" --listen 127.0.0.1:$UP --grpc "$GRPC" $USDC --decoy-address "$DECOY"

# name|port|price|title|description, then the payee: as install.sh.
ALL=$OUT/paywalls
{
  grep -v '^#' "$REPO/scripts/agentservices/services.txt" | grep . | sed "s/\$/|$PAYEE/"
  grep -v '^#' "$REPO/scripts/agentservices/decoys.txt" | grep . | sed "s/{{decoy}}/$DECOY/g; s/\$/|$DECOY/"
} > "$ALL"
while IFS='|' read -r name port price title desc payee; do
  p=$((port + OFFSET))
  run "paywall-$name" "$BIN/paywall" --listen 127.0.0.1:$p --upstream http://127.0.0.1:$UP/$name --pay-to "$payee" --price "$price" \
    --grpc "$GRPC" --chain-id "$CHAIN_ID" --name "$title" --description "$desc" --free /help --public-url http://127.0.0.1:$p $USDC
done < "$ALL"
while IFS='|' read -r name port _; do wait_for "http://127.0.0.1:$((port + OFFSET))/help"; done < "$ALL"
echo "== Paywalls up"

echo "== Listing the services (not the decoys) in the directory"
while IFS='|' read -r name port _; do
  case "$name" in ''|'#'*) continue;; esac
  "$BIN/paywall" list --url "http://127.0.0.1:$((port + OFFSET))" --key services --keyring-dir "$SERVICES_KEYRING" \
    --grpc "$GRPC" --chain-id "$CHAIN_ID" --allow-private >>"$OUT/list.log" 2>&1 || echo "  $name: see $OUT/list.log"
done < "$REPO/scripts/agentservices/services.txt"

# The decoys' bench-only listings, as decoys.sh writes them on the seed.
{
  while IFS='|' read -r name port _; do
    case "$name" in
      ''|'#'*) continue;;
      decoy-clock) echo "$name http://127.0.0.1:$((port + OFFSET)) 500uaeth";;
      *) echo "$name http://127.0.0.1:$((port + OFFSET))";;
    esac
  done < "$REPO/scripts/agentservices/decoys.txt"
} > "$OUT/bench-listings"
BENCH_KEY=bench-key-for-devnet-0123456789
echo "$BENCH_KEY" > "$OUT/bench-key"
SBX=$((8092 + OFFSET))
run sandbox "$BIN/agentmcp" --sandbox-http 127.0.0.1:$SBX --grpc "$GRPC" --chain-id "$CHAIN_ID" --rpc "" \
  --keyring-dir "$FUNDER_KEYRING" --keyring-backend test --sandbox-funder sandbox-funder \
  --sandbox-secret-file "$OUT/token-secret" --sandbox-bench-key-file "$OUT/bench-key" --sandbox-bench-per-day 200 \
  --sandbox-bench-listings "$OUT/bench-listings" --directory-allow-private $USDC
wait_for "http://127.0.0.1:$SBX/healthz"
sleep 8 # the listings land in the next block, and the directory reads them on first use
echo "== Test wallet up at http://127.0.0.1:$SBX/mcp"

for agent in $AGENTS; do
  echo "== $agent"
  (cd "$REPO/bench" && AETHER_BENCH_KEY=$BENCH_KEY go run ./paybench run --agent "$agent" \
    --endpoint "http://127.0.0.1:$SBX/mcp" --settle 6s --out "$OUT/results") | tail -1
done
(cd "$REPO/bench" && go run ./paybench report "$OUT"/results/*.json) > "$OUT/RESULTS.md"
echo "== Report: $OUT/RESULTS.md"
