#!/usr/bin/env bash
# Replay aether-testnet-1 from genesis with one aetherd build, checking the
# invariants (app/invariants.go) as it goes, to find the first block where
# this build computes something the chain didn't (nondeterminism, or a gate
# placed at the wrong height) or where an invariant breaks.
# docs/REVIEW.md, "Replaying the testnet".
#
# Run it on a workstation or a spare server, never on a validator: it is a
# full node syncing every block from genesis (days of CPU, the chain's
# whole history on disk). It makes its own home with a fresh node key and
# a consensus key nobody registered, so it never signs and holds no power.
#
#   BIN=./aetherd PEERS=<id@host:26656>[,...] bash scripts/replay/replay-testnet.sh
#
# Environment:
#   BIN       aetherd to replay with (required)
#   PEERS     persistent peers serving the whole block history (required)
#   HOME_DIR  node home (default ./replay-home); reused if it exists, so a
#             stopped replay continues where it left off
#   GENESIS   genesis file (default testnet/genesis.json)
#   EVERY     check the invariants every this many blocks (default 100;
#             1 checks every block and is slower)
#   TARGET    stop once this height is committed (default: never)
#   CHAIN_ID  default aether-testnet-1
#
# Outcomes, each printed as one RESULT line:
#   RESULT target <h>          reached TARGET with nothing broken
#   RESULT invariant <h> ...   an invariant broke at h (state left as is)
#   RESULT apphash <h> ...     this build executed block h differently
#                              from the chain (block h+1's header carries
#                              the chain's result); state left as is, for a
#                              closer look
#   RESULT stopped ...         aetherd stopped for another reason (log tail)
#
# At each height that adds a store (the node logs "restart this node") the
# script restarts aetherd once, as operators did on the live chain.
set -euo pipefail

: "${BIN:?set BIN to the aetherd to replay with}"
: "${PEERS:?set PEERS to peers that serve the whole history}"
HOME_DIR=${HOME_DIR:-./replay-home}
GENESIS=${GENESIS:-$(dirname "$0")/../../testnet/genesis.json}
EVERY=${EVERY:-100}
TARGET=${TARGET:-0}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
RPC_PORT=${RPC_PORT:-36657}
P2P_PORT=${P2P_PORT:-36656}
LOG=$HOME_DIR/replay.log

if [ ! -f "$HOME_DIR/config/genesis.json" ]; then
  "$BIN" init replay --chain-id "$CHAIN_ID" --home "$HOME_DIR" >/dev/null 2>&1
  cp "$GENESIS" "$HOME_DIR/config/genesis.json"
  cfg=$HOME_DIR/config/config.toml
  sed -i.bak \
    -e "s#^persistent_peers = .*#persistent_peers = \"$PEERS\"#" \
    -e "s#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:$RPC_PORT\"#" \
    -e "s#^laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:$P2P_PORT\"#" \
    -e 's#^pprof_laddr = .*#pprof_laddr = ""#' \
    "$cfg"
  app=$HOME_DIR/config/app.toml
  # No gRPC, REST or gRPC-web: nothing queries this node but this script.
  sed -i.bak -e '/^\[grpc\]/,/^\[/ s#^enable = true#enable = false#' \
             -e '/^\[api\]/,/^\[/ s#^enable = true#enable = false#' \
             -e '/^\[grpc-web\]/,/^\[/ s#^enable = true#enable = false#' "$app"
  echo "made $HOME_DIR (genesis sha256 $(sha256sum "$HOME_DIR/config/genesis.json" | cut -d' ' -f1))"
fi

height() {
  curl -s "localhost:$RPC_PORT/status" 2>/dev/null |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])' 2>/dev/null || echo 0
}

PID=
start() {
  echo "--- start $(date -u +%FT%TZ) at height $(height)" >>"$LOG"
  AETHER_INVARIANTS_EVERY=$EVERY AETHER_INVARIANTS_HALT=yes \
    "$BIN" start --home "$HOME_DIR" >>"$LOG" 2>&1 &
  PID=$!
}
stop() {
  [ -n "$PID" ] || return 0
  kill "$PID" 2>/dev/null || true
  for _ in $(seq 1 60); do kill -0 "$PID" 2>/dev/null || break; sleep 1; done
  kill -9 "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  PID=
}
trap stop EXIT

# Matches are counted from the log's length when (re)started, so a
# restart notice already handled isn't handled twice.
mark=0
newlog() { tail -c +"$((mark + 1))" "$LOG" 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g'; }

: >>"$LOG"
start
mark=$(stat -c %s "$LOG")
last=0
while :; do
  sleep 5
  out=$(newlog)
  if grep -q "invariant broken at height" <<<"$out"; then
    line=$(grep -o 'invariant broken at height [0-9]*: [^"]*' <<<"$out" | head -1)
    stop; echo "RESULT invariant ${line#invariant broken at height }"; exit 1
  fi
  if grep -qiE "wrong Block.Header.AppHash|app ?hash mismatch|wrong Block.Header.LastResultsHash" <<<"$out"; then
    line=$(grep -iE "wrong Block.Header.AppHash|app ?hash mismatch|wrong Block.Header.LastResultsHash" <<<"$out" | head -1)
    # Block h's header carries the chain's result for block h-1, so h-1
    # is the block this build executed differently.
    h=$(grep -o 'height=[0-9]*' <<<"$line" | head -1 | cut -d= -f2)
    stop
    if [ -n "$h" ]; then
      echo "RESULT apphash $((h - 1)) (block $h's header disagrees): ${line#*err=}"
    else
      echo "RESULT apphash $(height): $line"
    fi
    exit 1
  fi
  if grep -q "restart this node" <<<"$out"; then
    echo "restarting at $(height): $(grep -o '[a-z/]* activates at height [0-9]*' <<<"$out" | head -1)"
    stop; mark=$(stat -c %s "$LOG"); start; continue
  fi
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "RESULT stopped at $(height); last log lines:"; tail -20 "$LOG"; exit 1
  fi
  h=$(height)
  if [ "$TARGET" -gt 0 ] && [ "$h" -ge "$TARGET" ]; then
    stop; echo "RESULT target $h"; exit 0
  fi
  if [ $((h / 10000)) -ne $((last / 10000)) ]; then echo "height $h $(date -u +%FT%TZ)"; fi
  last=$h
done
