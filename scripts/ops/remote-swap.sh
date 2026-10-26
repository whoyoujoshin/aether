#!/usr/bin/env bash
# Runs on a Linux validator, as root, during a cutover. The workstation
# script feeds this on stdin (`bash -s`) after copying the new binary to
# --stage. It re-checks voting power immediately before stopping the unit
# so a set change between the workstation's preflight and this moment
# still blocks an unsafe restart.
#
# Dry-run lives in cutover.sh. This script always performs the swap.
# Rollback of a failed start happens only while the height read here is
# still below --activation-height.
set -euo pipefail

# Same inequality as scripts/ops/lib.sh and cmd/chainwatch/power.go.
safe_to_stop() {
  awk -v t="$1" -v p="$2" 'BEGIN {
    if (t !~ /^[0-9]+$/ || p !~ /^[0-9]+$/) exit 1
    if ((t + 0) <= 0 || (p + 0) < 0 || (p + 0) > (t + 0)) exit 1
    rem = t - p
    if ((rem * 3) > (t * 2)) exit 0
    exit 1
  }'
}
quorum_ok() {
  awk -v s="$1" -v t="$2" 'BEGIN {
    if (s !~ /^[0-9]+$/ || t !~ /^[0-9]+$/) exit 1
    if ((s * 3) > (t * 2)) exit 0
    exit 1
  }'
}

unit="" stage="" suffix=".pre-cutover" sha="" activation="" rpc="" validator="" min_peers=1 chain_id="" allow_down=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --unit) unit=$2; shift 2 ;;
    --stage) stage=$2; shift 2 ;;
    --suffix) suffix=$2; shift 2 ;;
    --sha256) sha=$2; shift 2 ;;
    --activation-height) activation=$2; shift 2 ;;
    --rpc) rpc=$2; shift 2 ;;
    --validator) validator=${2^^}; shift 2 ;;
    --min-peers) min_peers=$2; shift 2 ;;
    --chain-id) chain_id=$2; shift 2 ;;
    --allow-down) allow_down=1; shift ;;
    *) echo "remote-swap: unknown argument $1" >&2; exit 2 ;;
  esac
done

[[ -n "$unit" && -n "$stage" && -n "$sha" && -n "$activation" && -n "$rpc" && -n "$validator" ]] || {
  echo "remote-swap: missing required arguments" >&2
  exit 2
}
command -v jq >/dev/null 2>&1 || { echo "remote-swap: jq is required on the node" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "remote-swap: curl is required on the node" >&2; exit 1; }
[[ "$activation" =~ ^[0-9]+$ && "$activation" -gt 0 ]] || { echo "remote-swap: activation height must be a positive integer" >&2; exit 2; }

exec_line=$(systemctl show -p ExecStart --value "$unit")
bin=$(printf '%s\n' "$exec_line" | grep -oE '/[^ ;]*aetherd[^ ;]*' | head -1 || true)
[[ -n "$bin" && -f "$bin" ]] || { echo "remote-swap: cannot find the aetherd binary for unit $unit" >&2; exit 1; }
backup="${bin}${suffix}"
[[ ! -e "$backup" ]] || { echo "remote-swap: backup $backup already exists; refusing to overwrite the previous binary" >&2; exit 1; }

got=$(sha256sum "$stage" | awk '{print tolower($1)}')
want=$(printf '%s' "$sha" | tr 'A-F' 'a-f')
[[ "$got" == "$want" ]] || { echo "remote-swap: staged checksum $got does not match $want" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -sf --max-time 20 "$rpc/status" >"$tmp/status"
curl -sf --max-time 20 "$rpc/net_info" >"$tmp/net"
curl -sf --max-time 20 "$rpc/block" >"$tmp/block"
page=1
echo '{"result":{"validators":[],"total":"0"}}' >"$tmp/vals"
while [[ "$page" -le 20 ]]; do
  curl -sf --max-time 20 "$rpc/validators?per_page=100&page=$page" >"$tmp/page"
  count=$(jq -r '.result.validators | length' "$tmp/page")
  total_n=$(jq -r '.result.total' "$tmp/page")
  jq -s '.[0].result.validators += .[1].result.validators | .[0].result.total = .[1].result.total | .[0]' "$tmp/vals" "$tmp/page" >"$tmp/merged"
  mv "$tmp/merged" "$tmp/vals"
  have=$(jq '.result.validators | length' "$tmp/vals")
  [[ "$count" -eq 0 ]] && break
  [[ "$have" -ge "$total_n" ]] && break
  page=$((page + 1))
done

height=$(jq -r '.result.sync_info.latest_block_height' "$tmp/status")
catching=$(jq -r '.result.sync_info.catching_up' "$tmp/status")
network=$(jq -r '.result.node_info.network // empty' "$tmp/status")
peers=$(jq -r '.result.n_peers' "$tmp/net")
[[ "$height" =~ ^[0-9]+$ ]] || { echo "remote-swap: status height $height" >&2; exit 1; }

if [[ -n "$chain_id" && "$network" != "$chain_id" ]]; then
  echo "remote-swap: chain id $network, want $chain_id" >&2
  exit 1
fi
if [[ "$allow_down" -eq 0 && "$catching" != false ]]; then
  echo "remote-swap: node reports catching_up; wait until it is synced" >&2
  exit 1
fi
if [[ "$allow_down" -eq 0 && "$peers" -lt "$min_peers" ]]; then
  echo "remote-swap: $peers peers, want at least $min_peers" >&2
  exit 1
fi

declare -A powers=()
total=0
while read -r addr vp; do
  [[ -z "$addr" ]] && continue
  powers["${addr^^}"]=$vp
  total=$((total + vp))
done < <(jq -r '.result.validators[] | "\(.address) \(.voting_power)"' "$tmp/vals")
power=${powers[$validator]:-}
[[ -n "$power" ]] || { echo "remote-swap: $validator is not in the validator set" >&2; exit 1; }

jq -r '.result.block.last_commit.signatures[]? | select(.block_id_flag==2) | .validator_address' "$tmp/block" >"$tmp/signers"
signed=0
while read -r addr; do
  [[ -z "$addr" ]] && continue
  vp=${powers[${addr^^}]:-}
  [[ -n "$vp" ]] && signed=$((signed + vp))
done <"$tmp/signers"

if [[ "$allow_down" -eq 0 ]]; then
  if ! safe_to_stop "$total" "$power"; then
    echo "remote-swap: UNSAFE to stop $validator (power $power of $total). The others would hold 2/3 or less and the chain would halt." >&2
    exit 1
  fi
  if ! quorum_ok "$signed" "$total"; then
    echo "remote-swap: the latest block has $signed of $total voting power signed, which is not more than 2/3. Fix liveness before swapping a binary." >&2
    exit 1
  fi
  if ! grep -qi "^${validator}$" "$tmp/signers"; then
    echo "remote-swap: $validator did not sign the latest block" >&2
    exit 1
  fi
fi

echo "remote-swap: height $height, stopping $unit ($bin -> $backup)"
cp -a "$bin" "$backup"
if ! systemctl stop "$unit"; then
  echo "remote-swap: systemctl stop failed; the previous binary is still at $bin and a copy is at $backup" >&2
  exit 1
fi
if ! install -m 0755 "$stage" "$bin"; then
  echo "remote-swap: install failed; restoring $backup" >&2
  install -m 0755 "$backup" "$bin" || true
  systemctl start "$unit" || true
  exit 1
fi
if ! systemctl start "$unit"; then
  if [[ "$height" -lt "$activation" ]]; then
    echo "remote-swap: start failed at height $height, below activation $activation; restoring $backup" >&2
    install -m 0755 "$backup" "$bin" || true
    systemctl start "$unit" || true
  else
    echo "remote-swap: start failed at height $height, at or after activation $activation; leaving the new binary in place" >&2
  fi
  exit 1
fi
echo "remote-swap: started $bin"
