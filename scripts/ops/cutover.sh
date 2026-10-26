#!/usr/bin/env bash
# Swap aetherd on Linux validators, one node at a time.
#
# Dry-run is the default: read-only preflight runs for real, and every
# restart is printed and not performed. Pass --apply to restart nodes.
# This script does not compile. Build on a workstation with
# build-aetherd.sh (Go 1.26.10) and pass that folder as --prebuilt.
#
# Before each restart the others must still hold strictly more than 2/3
# of the voting power, and the latest block must have been signed by
# strictly more than 2/3. See docs/OPS-RUNBOOK.md.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

usage() {
  cat <<'EOF'
Usage: cutover.sh --nodes nodes.txt --activation-height HEIGHT (--prebuilt DIR | --binary FILE --sha256 HEX --built-with go1.26.10) [--apply]

  --nodes FILE              lines: name ssh rpc validator [unit]
  --activation-height N     rollback is refused at and after this height
  --prebuilt DIR            output of build-aetherd.sh (checks GO_VERSION and SHA256SUMS)
  --binary FILE             linux aetherd to install
  --sha256 HEX              checksum of --binary
  --built-with go1.26.10    required with --binary; the Go that produced it
  --apply                   perform the restarts (default: dry-run)
  --only NAME               one node from the file
  --backup-suffix SUFFIX    default .pre-cutover; refused if that backup exists
  --min-peers N             default 1
  --chain-id ID             require /status network to match
  --wait-timeout SECONDS    default 300, how long a node may take to sign again
  --poll-every SECONDS      default 5
  --allow-down              the node is already stopped: skip signing, peers, and the safe-to-stop check
  --accept-halt             with --only: swap that node even though stopping it leaves the others at
                            2/3 or less. Blocks stop until it is back (seconds, if the new binary
                            starts). Every other check still applies.
EOF
}

main() {
  local apply=0 binary="" sha="" built_with="" prebuilt="" nodes="" activation="" only=""
  local suffix=".pre-cutover" min_peers=1 chain_id="" wait_timeout=300 poll_every=5 allow_down=0 accept_halt=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --apply) apply=1; shift ;;
      --binary) binary=$2; shift 2 ;;
      --sha256) sha=$2; shift 2 ;;
      --built-with) built_with=$2; shift 2 ;;
      --prebuilt) prebuilt=$2; shift 2 ;;
      --nodes) nodes=$2; shift 2 ;;
      --activation-height) activation=$2; shift 2 ;;
      --only) only=$2; shift 2 ;;
      --backup-suffix) suffix=$2; shift 2 ;;
      --min-peers) min_peers=$2; shift 2 ;;
      --chain-id) chain_id=$2; shift 2 ;;
      --wait-timeout) wait_timeout=$2; shift 2 ;;
      --poll-every) poll_every=$2; shift 2 ;;
      --allow-down) allow_down=1; shift ;;
      --accept-halt) accept_halt=1; shift ;;
      -h|--help) usage; return 0 ;;
      *) echo "unknown argument: $1" >&2; usage >&2; return 2 ;;
    esac
  done

  [[ -n "$nodes" && -n "$activation" ]] || { usage >&2; echo "need --nodes and --activation-height" >&2; return 2; }
  if [[ "$accept_halt" -eq 1 && -z "$only" ]]; then
    echo "--accept-halt needs --only NODE: it is for one validator that holds a third or more, never for a whole run" >&2
    return 2
  fi
  [[ "$activation" =~ ^[0-9]+$ && "$activation" -gt 0 ]] || { echo "--activation-height must be a positive integer" >&2; return 2; }
  command -v jq >/dev/null 2>&1 || { echo "jq is required on the workstation" >&2; return 1; }
  parse_nodes_file "$nodes"

  if [[ -n "$prebuilt" ]]; then
    [[ -f "$prebuilt/GO_VERSION" && -f "$prebuilt/SHA256SUMS" && -f "$prebuilt/aetherd" ]] || {
      echo "--prebuilt $prebuilt needs aetherd, SHA256SUMS and GO_VERSION from build-aetherd.sh" >&2
      return 1
    }
    local gv
    gv=$(tr -d '[:space:]' <"$prebuilt/GO_VERSION")
    [[ "$gv" == "$REQUIRED_GO" ]] || { echo "prebuilt Go is $gv, want $REQUIRED_GO" >&2; return 1; }
    binary=$prebuilt/aetherd
    sha=$(awk '$2=="aetherd"{print $1; exit}' "$prebuilt/SHA256SUMS")
    built_with=$gv
  fi
  [[ -n "$binary" && -n "$sha" ]] || { echo "pass --prebuilt or both --binary and --sha256" >&2; return 2; }
  [[ "$built_with" == "$REQUIRED_GO" ]] || {
    echo "pass --built-with $REQUIRED_GO (the compiler recorded by build-aetherd.sh). Got: ${built_with:-empty}" >&2
    return 1
  }
  verify_sha256 "$binary" "$sha" || { echo "checksum of $binary does not match $sha" >&2; return 1; }

  if [[ "$apply" -eq 0 ]]; then
    echo "dry-run: preflight runs for real; no node will be restarted. Pass --apply to perform the cutover."
  else
    echo "apply: nodes will be restarted one at a time. Rollback is available only below height $activation (scripts/ops/rollback.sh)."
  fi

  local -a selected=()
  local i
  for i in "${!N_NAME[@]}"; do
    if [[ -n "$only" && "${N_NAME[$i]}" != "$only" ]]; then
      continue
    fi
    selected+=("$i")
  done
  [[ ${#selected[@]} -gt 0 ]] || { echo "no node matched --only $only" >&2; return 1; }

  local failed=0
  for i in "${selected[@]}"; do
    if ! preflight_node "$i" "$min_peers" "$chain_id" "$allow_down" "$sha" "$accept_halt"; then
      failed=1
    fi
  done
  if [[ "$failed" -ne 0 ]]; then
    echo "preflight failed; no node was changed" >&2
    return 1
  fi

  for i in "${selected[@]}"; do
    local name=${N_NAME[$i]} ssh=${N_SSH[$i]}
    if node_matches_sha "$ssh" "${N_UNIT[$i]}" "$sha"; then
      echo "$name is already running $sha; skipping"
      continue
    fi
    local stage="/tmp/aetherd.cutover.$$"
    local -a args=(
      --unit "${N_UNIT[$i]}" --stage "$stage" --suffix "$suffix" --sha256 "$sha"
      --activation-height "$activation" --rpc "${N_RPC[$i]}" --validator "${N_VAL[$i]}"
      --min-peers "$min_peers"
    )
    [[ -n "$chain_id" ]] && args+=(--chain-id "$chain_id")
    [[ "$allow_down" -eq 1 ]] && args+=(--allow-down)
    [[ "$accept_halt" -eq 1 ]] && args+=(--accept-halt)
    if [[ "$apply" -eq 0 ]]; then
      echo "dry-run [$ssh] copy $binary -> $stage"
      echo "dry-run [$ssh] bash remote-swap.sh ${args[*]}"
      echo "dry-run [$name] would wait up to ${wait_timeout}s for height to advance, catching_up=false, and ${N_VAL[$i]} to sign, with the chain still over 2/3"
      continue
    fi
    echo "== $name: re-checking, then swapping"
    preflight_node "$i" "$min_peers" "$chain_id" "$allow_down" "$sha" "$accept_halt" >/dev/null
    local before
    before=$(node_height "$ssh" "${N_RPC[$i]}")
    remote_copy "$binary" "$ssh" "$stage"
    if ! remote_script "$ssh" "$HERE/remote-swap.sh" "${args[@]}"; then
      echo "$name: swap failed. No further node will be touched. Below height $activation, rollback.sh restores ${suffix}." >&2
      return 1
    fi
    if ! wait_healthy "$i" "$before" "$wait_timeout" "$poll_every"; then
      echo "$name did not return to signing. No further node will be touched." >&2
      return 1
    fi
    if ! node_matches_sha "$ssh" "${N_UNIT[$i]}" "$sha"; then
      echo "$name is up but the running binary checksum does not match $sha" >&2
      return 1
    fi
    echo "== $name is signing on $sha"
  done
  echo "cutover plan complete"
}

# preflight_node INDEX MIN_PEERS CHAIN_ID ALLOW_DOWN SHA ACCEPT_HALT
preflight_node() {
  local i=$1 min_peers=$2 chain_id=$3 allow_down=$4 sha=$5 accept_halt=${6:-0}
  local name=${N_NAME[$i]} ssh=${N_SSH[$i]} rpc=${N_RPC[$i]} val=${N_VAL[$i]}
  local tmp uid height catching network peers total power signed
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN
  echo "-- preflight $name ($ssh)"
  uid=$(remote_exec "$ssh" id -u) || { echo "$name: ssh failed" >&2; return 1; }
  if [[ "$uid" != 0 ]]; then
    echo "$name: ssh user is uid $uid; connect as root so systemctl can restart aetherd" >&2
    return 1
  fi
  remote_exec "$ssh" curl -sf --max-time 20 "$rpc/status" >"$tmp/status" || { echo "$name: status failed" >&2; return 1; }
  remote_exec "$ssh" curl -sf --max-time 20 "$rpc/net_info" >"$tmp/net" || { echo "$name: net_info failed" >&2; return 1; }
  remote_exec "$ssh" curl -sf --max-time 20 "$rpc/block" >"$tmp/block" || { echo "$name: block failed" >&2; return 1; }
  fetch_validators "$ssh" "$rpc" "$tmp/vals" || { echo "$name: validators failed" >&2; return 1; }

  height=$(jq -r '.result.sync_info.latest_block_height' "$tmp/status")
  catching=$(jq -r '.result.sync_info.catching_up' "$tmp/status")
  network=$(jq -r '.result.node_info.network // empty' "$tmp/status")
  peers=$(jq -r '.result.n_peers' "$tmp/net")
  {
    read -r power
    read -r total
  } < <(validator_power "$tmp/vals" "$val")
  if [[ "$power" == missing ]]; then
    power=""
  fi
  signed=$(signed_power "$tmp/vals" "$tmp/block")

  echo "   height $height catching_up=$catching peers=$peers chain=${network:-unknown} power=${power:-missing}/$total signed=$signed/$total"
  local ok=0
  if [[ -n "$chain_id" && "$network" != "$chain_id" ]]; then
    echo "   FAIL chain id $network, want $chain_id" >&2
    ok=1
  fi
  if [[ "$allow_down" -eq 0 && "$catching" != false ]]; then
    echo "   FAIL catching_up" >&2
    ok=1
  fi
  if [[ "$allow_down" -eq 0 && "$peers" -lt "$min_peers" ]]; then
    echo "   FAIL peers $peers < $min_peers" >&2
    ok=1
  fi
  if [[ -z "$power" ]]; then
    echo "   FAIL $val is not in the validator set" >&2
    ok=1
  elif [[ "$allow_down" -eq 0 ]] && ! safe_to_stop "$total" "$power"; then
    if [[ "$accept_halt" -eq 1 ]]; then
      echo "   WARNING unsafe to stop $val (power $power of $total): blocks stop while it restarts (--accept-halt)" >&2
    else
      echo "   FAIL unsafe to stop $val (power $power of $total): the others would hold 2/3 or less (--accept-halt --only $name swaps it anyway, halting blocks while it restarts)" >&2
      ok=1
    fi
  fi
  if [[ "$allow_down" -eq 0 ]] && ! quorum_ok "$signed" "$total"; then
    echo "   FAIL latest commit signed $signed of $total, which is not more than 2/3" >&2
    ok=1
  fi
  if [[ "$allow_down" -eq 0 ]] && ! grep -qi "^${val}$" <(jq -r '.result.block.last_commit.signatures[]? | select(.block_id_flag==2) | .validator_address' "$tmp/block" | tr 'a-f' 'A-F'); then
    echo "   FAIL $val did not sign the latest block" >&2
    ok=1
  fi
  if [[ "$allow_down" -eq 0 ]] && ! unit_running "$ssh" "${N_UNIT[$i]}"; then
    echo "   FAIL aetherd is not running (pass --allow-down to replace a stopped node)" >&2
    ok=1
  fi
  if [[ "$ok" -ne 0 ]]; then
    return 1
  fi
  echo "   ok"
}

fetch_validators() {
  local ssh=$1 rpc=$2 out=$3 page=1 count total_n have
  echo '{"result":{"validators":[],"total":"0"}}' >"$out"
  while [[ "$page" -le 20 ]]; do
    remote_exec "$ssh" curl -sf --max-time 20 "$rpc/validators?per_page=100&page=$page" >"$out.page" || return 1
    count=$(jq -r '.result.validators | length' "$out.page")
    total_n=$(jq -r '.result.total' "$out.page")
    jq -s '.[0].result.validators += .[1].result.validators | .[0].result.total = .[1].result.total | .[0]' "$out" "$out.page" >"$out.merged"
    mv "$out.merged" "$out"
    have=$(jq '.result.validators | length' "$out")
    rm -f "$out.page"
    [[ "$count" -eq 0 ]] && return 0
    [[ "$have" -ge "$total_n" ]] && return 0
    page=$((page + 1))
  done
  echo "validators pagination did not finish" >&2
  return 1
}

# validator_power FILE ADDR writes two lines: power (or "missing"), then total.
validator_power() {
  local file=$1 addr=${2^^} total power
  total=$(jq '[.result.validators[].voting_power | tonumber] | add // 0' "$file")
  power=$(jq -r --arg a "$addr" '[.result.validators[] | select((.address | ascii_upcase) == $a) | .voting_power | tonumber] | add // empty' "$file")
  if [[ -z "$power" ]]; then
    printf 'missing\n%s\n' "$total"
  else
    printf '%s\n%s\n' "$power" "$total"
  fi
}

signed_power() {
  local vals=$1 block=$2
  jq -n --slurpfile v "$vals" --slurpfile b "$block" '
    ($v[0].result.validators // []) as $vals
    | ($b[0].result.block.last_commit.signatures // [])
    | map(select(.block_id_flag == 2) | .validator_address | ascii_upcase) as $signed
    | [$vals[] | select((.address | ascii_upcase) as $a | $signed | index($a)) | .voting_power | tonumber]
    | add // 0
  '
}

node_height() {
  local ssh=$1 rpc=$2
  remote_exec "$ssh" curl -sf --max-time 20 "$rpc/status" | jq -r '.result.sync_info.latest_block_height'
}

node_matches_sha() {
  local ssh=$1 unit=$2 sha=$3 got
  got=$(unit_exe_sha "$ssh" "$unit" 2>/dev/null || true)
  [[ -n "$got" && "$got" == "$(printf '%s' "$sha" | tr 'A-F' 'a-f')" ]]
}

wait_healthy() {
  local i=$1 before=$2 timeout=$3 every=$4
  local ssh=${N_SSH[$i]} rpc=${N_RPC[$i]} val=${N_VAL[$i]} name=${N_NAME[$i]}
  local start=$SECONDS tmp height catching signed total power
  tmp=$(mktemp -d)
  while (( SECONDS - start < timeout )); do
    if remote_exec "$ssh" curl -sf --max-time 20 "$rpc/status" >"$tmp/status" \
      && remote_exec "$ssh" curl -sf --max-time 20 "$rpc/block" >"$tmp/block" \
      && fetch_validators "$ssh" "$rpc" "$tmp/vals"; then
      height=$(jq -r '.result.sync_info.latest_block_height' "$tmp/status")
      catching=$(jq -r '.result.sync_info.catching_up' "$tmp/status")
      signed=$(signed_power "$tmp/vals" "$tmp/block")
      total=$(jq '[.result.validators[].voting_power | tonumber] | add // 0' "$tmp/vals")
      if [[ "$catching" == false && "$height" =~ ^[0-9]+$ && "$height" -gt "$before" ]] \
        && grep -qi "^${val}$" <(jq -r '.result.block.last_commit.signatures[]? | select(.block_id_flag==2) | .validator_address' "$tmp/block" | tr 'a-f' 'A-F') \
        && quorum_ok "$signed" "$total"; then
        rm -rf "$tmp"
        echo "$name signed at height $height ($signed of $total)"
        return 0
      fi
    fi
    sleep "$every"
  done
  rm -rf "$tmp"
  echo "$name: still not signing after ${timeout}s" >&2
  return 1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
