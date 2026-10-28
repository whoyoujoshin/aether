#!/usr/bin/env bash
# Points the seed's public services at the query node (docs/QUERY-NODE.md)
# instead of the seed's validator, or back. Run on the seed as root:
#
#   QUERY_GRPC=10.0.0.5:9090 QUERY_RPC=http://10.0.0.5:26657 \
#     bash scripts/querynode/point.sh services --dry-run
#   ... point.sh services        # the units, one at a time
#   ... point.sh public          # rpc. and grpc.<seed>.sslip.io in Caddy
#   bash scripts/querynode/point.sh status
#   bash scripts/querynode/point.sh back-public
#   bash scripts/querynode/point.sh back-services
#
# services: checks the query node (same chain, caught up, within a few
# blocks of this node, no voting power), writes /root/aether-query.env
# (install.sh and sandbox.sh then keep the query node on later redeploys),
# and moves the explorer, the public agentmcp, the test wallet,
# agentservices, the facilitator, the job board and every paywall. Each
# unit is backed up (<file>.bak-query-<stamp>), restarted and checked
# before the next; one that fails its check is put back and the script
# stops. aetherd, the powminer, the faucet and the watchers stay on the
# local node. It never touches aetherd.
#
# public: points the seed's public RPC and gRPC hostnames (Caddy) at the
# query node, so outside agents and wallets stop reading the validator.
#
# back-services / back-public: the reverse, using /root/aether-query.env
# (back-services renames it .off, so later redeploys use the local node).
# --dry-run (after the command) prints the diffs and changes nothing.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

UNIT_DIR=${UNIT_DIR:-/etc/systemd/system}
QUERY_ENV=${QUERY_ENV:-/root/aether-query.env}
CADDYFILE=${CADDYFILE:-/etc/caddy/Caddyfile}
LOCAL_RPC=${LOCAL_RPC:-http://127.0.0.1:26657}
MAX_LAG=${MAX_LAG:-5}
EXPLORER_PORT=${EXPLORER_PORT:-8081}
STAMP=$(date +%Y%m%d-%H%M%S)

cmd=${1:-}
DRY=no
[[ "${2:-}" == --dry-run ]] && DRY=yes

say() { printf '\n== %s\n' "$*"; }
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

load_env() {
  local f
  for f in "$QUERY_ENV" "$QUERY_ENV.off"; do
    if [[ -z "${QUERY_GRPC:-}" || -z "${QUERY_RPC:-}" ]] && [[ -r "$f" ]]; then
      # shellcheck disable=SC1090
      . "$f"
    fi
  done
  [[ -n "${QUERY_GRPC:-}" && -n "${QUERY_RPC:-}" ]] || die "set QUERY_GRPC=<host>:9090 and QUERY_RPC=http://<host>:26657 (or have $QUERY_ENV)"
  valid_hostport "$QUERY_GRPC" || die "QUERY_GRPC must be host:port, got: $QUERY_GRPC"
  valid_rpc "$QUERY_RPC" || die "QUERY_RPC must be http://host:port, got: $QUERY_RPC"
}

# units prints the moving units that exist here, the least visible first.
units() {
  local u f
  for u in aether-jobboard aether-facilitator aether-agentservices; do
    [[ -f "$UNIT_DIR/$u.service" ]] && echo "$u"
  done
  for f in "$UNIT_DIR"/aether-paywall-*.service; do
    [[ -f "$f" ]] && basename "$f" .service
  done
  for u in aether-sandbox aether-agentmcp aether-explorer; do
    [[ -f "$UNIT_DIR/$u.service" ]] && echo "$u"
  done
}

# files <unit>: the unit file and its drop-ins.
files() {
  echo "$UNIT_DIR/$1.service"
  local d
  for d in "$UNIT_DIR/$1.service.d"/*.conf; do
    [[ -f "$d" ]] && echo "$d"
  done
}

status_json() { curl -fsS --max-time 5 "$1/status"; }

check_query_node() {
  say "Checking the query node at $QUERY_RPC (gRPC $QUERY_GRPC)"
  command -v jq >/dev/null || die "jq is not installed"
  local q l qnet lnet qh lh qid lid qcatch qpower
  q=$(status_json "$QUERY_RPC") || die "the query node's RPC doesn't answer at $QUERY_RPC"
  l=$(status_json "$LOCAL_RPC") || die "this node's RPC doesn't answer at $LOCAL_RPC"
  qnet=$(jq -r .result.node_info.network <<<"$q")
  lnet=$(jq -r .result.node_info.network <<<"$l")
  qid=$(jq -r .result.node_info.id <<<"$q")
  lid=$(jq -r .result.node_info.id <<<"$l")
  qh=$(jq -r .result.sync_info.latest_block_height <<<"$q")
  lh=$(jq -r .result.sync_info.latest_block_height <<<"$l")
  qcatch=$(jq -r .result.sync_info.catching_up <<<"$q")
  qpower=$(jq -r .result.validator_info.voting_power <<<"$q")
  [[ "$qnet" == "$lnet" ]] || die "the query node is on $qnet, this node on $lnet"
  [[ "$qid" != "$lid" ]] || die "QUERY_RPC is this node itself"
  [[ "$qcatch" == false ]] || die "the query node is still catching up (at $qh)"
  [[ "$qpower" == 0 ]] || die "the query node has voting power $qpower: it must not hold a validator key"
  ((lh - qh <= MAX_LAG && qh - lh <= MAX_LAG)) || die "the query node is at $qh, this node at $lh"
  local host=${QUERY_GRPC%:*} port=${QUERY_GRPC##*:}
  timeout 5 bash -c "</dev/tcp/$host/$port" 2>/dev/null || die "nothing answers on $QUERY_GRPC (gRPC)"
  echo "$qnet, height $qh (this node $lh), not a validator, gRPC port open"
}

# healthy <unit>: a check that reads the chain where the service has one.
healthy() {
  local u=$1 port body
  code() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@" || true; }
  for _ in $(seq 1 15); do
    sleep "${QN_SLEEP:-2}"
    systemctl is-active --quiet "$u" || continue
    case "$u" in
      aether-explorer) [[ $(code "http://127.0.0.1:$EXPLORER_PORT/api/stats") == 200 ]] && return 0 ;;
      aether-facilitator) [[ $(code http://127.0.0.1:8403/healthz) == 200 ]] && return 0 ;;
      aether-jobboard) [[ $(code http://127.0.0.1:8404/jobs/healthz) == 200 ]] && return 0 ;;
      aether-sandbox) [[ $(code http://127.0.0.1:8092/healthz) == 200 ]] && return 0 ;;
      aether-agentmcp)
        # find_services searches transactions: the read the move is for.
        body=$(curl -s --max-time 20 -X POST http://127.0.0.1:8090/mcp -H 'Content-Type: application/json' \
          -H 'Accept: application/json, text/event-stream' \
          -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"find_services","arguments":{}}}' || true)
        grep -q '"result"' <<<"$body" && ! grep -q '"isError":true' <<<"$body" && return 0 ;;
      aether-paywall-*)
        port=$(grep -oE -- '--listen 127\.0\.0\.1:[0-9]+' "$UNIT_DIR/$u.service" | head -1 | grep -oE '[0-9]+$' || true)
        [[ -z "$port" ]] && return 0
        [[ $(code "http://127.0.0.1:$port/help") == 200 ]] && return 0 ;;
      *) return 0 ;;
    esac
  done
  return 1
}

# edit <file> <fn> <args...>: runs fn on a copy of file. Prints nothing and
# returns 1 if that changes nothing; with --dry-run prints the diff;
# otherwise backs the file up (<file>.bak-query-<stamp>) and writes it.
edit() {
  local f=$1 fn=$2 tmp
  shift 2
  tmp=$(mktemp)
  cat "$f" >"$tmp"
  "$fn" "$tmp" "$@"
  if cmp -s "$f" "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  if [[ "$DRY" == yes ]]; then
    diff -u --label "$f" --label "$f (new)" "$f" "$tmp" || true
  else
    cp -a "$f" "$f.bak-query-$STAMP"
    cat "$tmp" >"$f"
  fi
  rm -f "$tmp"
}

move_units() {
  local direction=$1 u f changed
  for u in $(units); do
    qn_target "$u" || continue
    changed=()
    for f in $(files "$u"); do
      if [[ "$direction" == to ]]; then
        edit "$f" qn_rewrite "$(qn_takes_rpc "$u" && echo yes || echo no)" "$QUERY_GRPC" "$QUERY_RPC" && changed+=("$f")
      else
        edit "$f" qn_restore "$QUERY_GRPC" "$QUERY_RPC" && changed+=("$f")
      fi
    done
    if ((${#changed[@]} == 0)); then
      echo "$u: nothing to change"
      continue
    fi
    [[ "$DRY" == yes ]] && continue
    systemctl daemon-reload
    systemctl restart "$u"
    if healthy "$u"; then
      echo "$u: done, answering (backup ${changed[0]}.bak-query-$STAMP)"
    else
      journalctl -u "$u" -n 20 --no-pager || true
      for f in "${changed[@]}"; do cp -a "$f.bak-query-$STAMP" "$f"; done
      systemctl daemon-reload
      systemctl restart "$u"
      die "$u didn't answer after the change; put it back as it was. Units changed before it stay changed (status shows them)."
    fi
  done
}

move_caddy() {
  local direction=$1 rpc_from grpc_from rpc_to grpc_to
  [[ -f "$CADDYFILE" ]] || die "no $CADDYFILE"
  if [[ "$direction" == to ]]; then
    rpc_from='(127\.0\.0\.1|localhost):26657' rpc_to=${QUERY_RPC#http://}
    grpc_from='(127\.0\.0\.1|localhost):9090' grpc_to=$QUERY_GRPC
  else
    rpc_from=$(_re_escape "${QUERY_RPC#http://}") rpc_to=127.0.0.1:26657
    grpc_from=$(_re_escape "$QUERY_GRPC") grpc_to=127.0.0.1:9090
  fi
  caddy_both() { caddy_upstream "$1" "$rpc_from" "$rpc_to" >/dev/null; caddy_upstream "$1" "$grpc_from" "$grpc_to" >/dev/null; }
  if ! edit "$CADDYFILE" caddy_both; then
    echo "Caddy: nothing to change"
    return
  fi
  [[ "$DRY" == yes ]] && return
  if ! caddy validate --config "$CADDYFILE" --adapter caddyfile >/dev/null 2>&1; then
    cp -a "$CADDYFILE.bak-query-$STAMP" "$CADDYFILE"
    die "Caddy rejected the change; put the file back"
  fi
  systemctl reload caddy
  echo "Caddy reloaded (backup $CADDYFILE.bak-query-$STAMP)"
}

status() {
  local u
  for u in $(units); do
    printf '%-24s %s\n' "$u" "$(cat $(files "$u") | grep -oE -- '--(grpc|rpc)[ =][^ ]+' | tr '\n' ' ')"
  done
  [[ -f "$CADDYFILE" ]] && grep -nE 'reverse_proxy[[:space:]]+[^ ]+:(26657|9090)\b' "$CADDYFILE" || true
  [[ -r "$QUERY_ENV" ]] && { echo "$QUERY_ENV:"; cat "$QUERY_ENV"; } || echo "no $QUERY_ENV: services use the local node"
}

[[ $EUID -eq 0 || "$DRY" == yes || "$cmd" == status || -n "${QN_TEST:-}" ]] || die "run as root"
case "$cmd" in
  services)
    load_env
    check_query_node
    if [[ "$DRY" == no ]]; then
      printf 'QUERY_GRPC=%s\nQUERY_RPC=%s\n' "$QUERY_GRPC" "$QUERY_RPC" >"$QUERY_ENV"
      chmod 644 "$QUERY_ENV"
    fi
    say "Moving the services to the query node"
    [[ "$DRY" == yes ]] && echo "(dry run: diffs only, nothing written or restarted)"
    move_units to
    ;;
  public)
    load_env
    check_query_node
    say "Pointing the public RPC and gRPC hostnames at the query node"
    move_caddy to
    ;;
  back-services)
    load_env
    say "Moving the services back to this node"
    move_units back
    # Later redeploys use the local node again; back-public can still read it.
    if [[ "$DRY" == no && -f "$QUERY_ENV" ]]; then mv -f "$QUERY_ENV" "$QUERY_ENV.off"; fi
    ;;
  back-public)
    load_env
    say "Pointing the public hostnames back at this node"
    move_caddy back
    ;;
  status) status ;;
  *) sed -n '2,29p' "$0"; exit 2 ;;
esac
