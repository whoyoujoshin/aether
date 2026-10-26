#!/usr/bin/env bash
# Restore one Linux node's pre-cutover aetherd binary.
# Dry-run unless --apply. Refuses when the node's height is at or past
# the activation height. See docs/OPS-RUNBOOK.md.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

usage() {
  cat <<'EOF'
Usage: rollback.sh --nodes nodes.txt --name NODE --activation-height HEIGHT [--apply]

Restores BIN + --backup-suffix (default .pre-cutover) over the unit's
aetherd and restarts that one node. Rolls back only while the height is
still below the activation height. The height is the highest any node in
the file reports, so a node whose new binary won't start (no RPC) can
still be rolled back; if no node answers, it refuses.
EOF
}

main() {
  local apply=0 nodes="" name="" activation="" suffix=".pre-cutover"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --apply) apply=1; shift ;;
      --nodes) nodes=$2; shift 2 ;;
      --name) name=$2; shift 2 ;;
      --activation-height) activation=$2; shift 2 ;;
      --backup-suffix) suffix=$2; shift 2 ;;
      -h|--help) usage; return 0 ;;
      *) echo "unknown argument: $1" >&2; usage >&2; return 2 ;;
    esac
  done
  [[ -n "$nodes" && -n "$name" && -n "$activation" ]] || { usage >&2; return 2; }
  command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; return 1; }
  parse_nodes_file "$nodes"
  local i=-1 n
  for n in "${!N_NAME[@]}"; do
    if [[ "${N_NAME[$n]}" == "$name" ]]; then
      i=$n
    fi
  done
  [[ "$i" -ge 0 ]] || { echo "no node named $name" >&2; return 1; }
  local ssh=${N_SSH[$i]} rpc=${N_RPC[$i]} unit=${N_UNIT[$i]}
  local height="" h n2
  # The highest height any node reports: the one being rolled back is
  # often down. Taking the highest never allows a rollback the chain is
  # already past.
  for n2 in "${!N_NAME[@]}"; do
    h=$(remote_exec "${N_SSH[$n2]}" curl -sf --max-time 20 "${N_RPC[$n2]}/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || true)
    if [[ "$h" =~ ^[0-9]+$ ]]; then
      echo "   ${N_NAME[$n2]} reports height $h"
      if [[ -z "$height" || "$h" -gt "$height" ]]; then
        height=$h
      fi
    else
      echo "   ${N_NAME[$n2]}: no height (RPC down?)"
    fi
  done
  if [[ -z "$height" ]]; then
    echo "refusing rollback of $name: no node in $nodes reported a height" >&2
    return 2
  fi
  if ! rollback_allowed "$height" "$activation"; then
    echo "refusing rollback of $name: height $height, activation $activation. Roll back only while the height is still below the activation height." >&2
    return 2
  fi
  echo "$name is at height $height, below activation $activation. Backup suffix $suffix."
  if [[ "$apply" -eq 0 ]]; then
    echo "dry-run [$ssh] bash remote-rollback.sh --unit $unit --suffix $suffix --activation-height $activation --rpc $rpc --height $height"
    echo "dry-run: no node was restarted. Pass --apply to restore the backup."
    return 0
  fi
  if ! remote_script "$ssh" "$HERE/remote-rollback.sh" --unit "$unit" --suffix "$suffix" --activation-height "$activation" --rpc "$rpc" --height "$height"; then
    echo "rollback of $name failed" >&2
    return 1
  fi
  echo "$name restored from $suffix"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
