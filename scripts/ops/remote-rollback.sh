#!/usr/bin/env bash
# Runs on a Linux validator, as root. Restores the binary saved by
# remote-swap.sh. Refuses at or after the activation height: from that
# block an older binary can disagree with the chain.
set -euo pipefail

rollback_allowed() {
  awk -v h="$1" -v a="$2" 'BEGIN {
    if (h !~ /^[0-9]+$/ || a !~ /^[0-9]+$/) exit 1
    if ((a + 0) > 0 && (h + 0) < (a + 0)) exit 0
    exit 1
  }'
}

unit="" suffix=".pre-cutover" activation="" rpc="" given=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --unit) unit=$2; shift 2 ;;
    --suffix) suffix=$2; shift 2 ;;
    --activation-height) activation=$2; shift 2 ;;
    --rpc) rpc=$2; shift 2 ;;
    # The chain's height as rollback.sh read it from every node: this
    # node's own RPC is down when its new binary won't start.
    --height) given=$2; shift 2 ;;
    *) echo "remote-rollback: unknown argument $1" >&2; exit 2 ;;
  esac
done
[[ -n "$unit" && -n "$activation" && -n "$rpc" ]] || { echo "remote-rollback: missing required arguments" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "remote-rollback: jq is required on the node" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "remote-rollback: curl is required on the node" >&2; exit 1; }

own=$(curl -sf --max-time 20 "$rpc/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || true)
height=""
for h in "$given" "$own"; do
  if [[ "$h" =~ ^[0-9]+$ ]] && [[ -z "$height" || "$h" -gt "$height" ]]; then
    height=$h
  fi
done
if [[ -z "$height" ]]; then
  echo "remote-rollback: refusing. No height: this node's RPC doesn't answer and no --height was given." >&2
  exit 2
fi
if ! rollback_allowed "$height" "$activation"; then
  echo "remote-rollback: refusing. Height is $height and activation is $activation. Roll back only while the height is still below the activation height." >&2
  exit 2
fi

exec_line=$(systemctl show -p ExecStart --value "$unit")
bin=$(printf '%s\n' "$exec_line" | grep -oE '/[^ ;]*aetherd[^ ;]*' | head -1 || true)
[[ -n "$bin" ]] || { echo "remote-rollback: cannot find the aetherd binary for unit $unit" >&2; exit 1; }
backup="${bin}${suffix}"
[[ -f "$backup" ]] || { echo "remote-rollback: no backup at $backup" >&2; exit 1; }

echo "remote-rollback: height $height, restoring $backup onto $bin"
systemctl stop "$unit"
install -m 0755 "$backup" "$bin"
systemctl start "$unit"
echo "remote-rollback: started $bin from $backup"
