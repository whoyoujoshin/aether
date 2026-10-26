#!/usr/bin/env bash
# Tests for the cutover helpers, the on-node swap script, and a dry-run
# of cutover.sh / rollback.sh against a fake RPC. No network and no
# aetherd process.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

pad40() {
  local prefix=$1
  local n=$((40 - ${#prefix}))
  [[ "$n" -ge 0 ]] || fail "prefix too long: $prefix"
  printf '%s' "$prefix"
  printf 'A%.0s' $(seq 1 "$n")
}

# --- pure helpers (same cases as cmd/chainwatch/power_test.go) ---
safe_to_stop 100 25 || fail "25 of 100 should be safe"
safe_to_stop 100 33 || fail "33 of 100 should be safe"
if safe_to_stop 100 34; then fail "34 of 100 should be unsafe"; fi
if safe_to_stop 300 100; then fail "exactly 1/3 should be unsafe"; fi
if safe_to_stop 3 1; then fail "1 of 3 should be unsafe"; fi
safe_to_stop 100 0 || fail "0 power is safe"
if safe_to_stop 100 100; then fail "all the power is unsafe"; fi
if safe_to_stop 0 0; then fail "empty total is unsafe"; fi
if safe_to_stop 100 40; then fail "40 of 100 should be unsafe"; fi
safe_to_stop 100 20 || fail "20 of 100 should be safe"

quorum_ok 67 100 || fail "67 of 100 is a quorum"
if quorum_ok 66 100; then fail "66 of 100 is not a quorum"; fi
if quorum_ok 2 3; then fail "exactly 2/3 is not a quorum"; fi

rollback_allowed 100 200 || fail "100 < 200 may roll back"
if rollback_allowed 200 200; then fail "at activation, no rollback"; fi
if rollback_allowed 201 200; then fail "past activation, no rollback"; fi
if rollback_allowed 10 0; then fail "unknown activation, no rollback"; fi
if rollback_allowed abc 10; then fail "non-numeric height"; fi

go_version_ok "go version go1.26.10 linux/amd64" go1.26.10 || fail "go 1.26.10"
go_version_ok "go1.26.10" go1.26.10 || fail "bare go version"
if go_version_ok "go version go1.26.9 linux/amd64" go1.26.10; then fail "1.26.9 accepted"; fi
if go_version_ok "go version go1.26.100 linux/amd64" go1.26.10; then fail "1.26.100 accepted"; fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo hello >"$tmp/f"
sum=$(sha256sum "$tmp/f" | awk '{print $1}')
verify_sha256 "$tmp/f" "$sum" || fail "sha256"
verify_sha256 "$tmp/f" "${sum^^}" || fail "sha256 uppercase"
if verify_sha256 "$tmp/f" "deadbeef"; then fail "bad sha accepted"; fi

line=$(bin_from_execstart "{ path=/root/aetherd-5267ab3 ; argv[]=/root/aetherd-5267ab3 start --home /root/.aether ; }")
[[ "$line" == /root/aetherd-5267ab3 ]] || fail "execstart parse: $line"

printf 'sync3 local http://127.0.0.1:26657 %s aetherd\n# comment\n\n' "$(pad40 CCA35D28)" >"$tmp/nodes"
parse_nodes_file "$tmp/nodes"
[[ "${N_NAME[0]}" == sync3 && "${N_UNIT[0]}" == aetherd && ${#N_VAL[0]} -eq 40 ]] || fail "parse nodes"
if printf 'bad line\n' >"$tmp/bad" && parse_nodes_file "$tmp/bad"; then fail "bad line accepted"; fi
if printf 'sync3 local http://x %s aetherd;rm\n' "$(pad40 CCA35D28)" >"$tmp/bad" && parse_nodes_file "$tmp/bad" 2>/dev/null; then fail "bad unit accepted"; fi

# ssh joins its arguments with spaces for the remote shell: a stub ssh
# that does the same shows remote_exec's arguments arrive intact.
sshstub=$(mktemp -d)
cat >"$sshstub/ssh" <<'STUB'
#!/bin/bash
while [[ "$1" == -o ]]; do shift 2; done
shift # destination
exec bash -c "$*"
STUB
chmod +x "$sshstub/ssh"
got=$(PATH="$sshstub:$PATH" remote_exec root@node printf '%s\n' 'http://127.0.0.1:26657/validators?per_page=100&page=2')
[[ "$got" == 'http://127.0.0.1:26657/validators?per_page=100&page=2' ]] || fail "URL with & over ssh: $got"
# shellcheck disable=SC2016
got=$(PATH="$sshstub:$PATH" remote_exec root@node bash -c 'echo "[$1]"' _ 'a b')
[[ "$got" == '[a b]' ]] || fail "bash -c over ssh: $got"
# shellcheck disable=SC2016
echo 'echo "[$1|$2]"' >"$tmp/s.sh"
got=$(PATH="$sshstub:$PATH" remote_script root@node "$tmp/s.sh" --rpc 'http://x/?a=1&b=2')
[[ "$got" == '[--rpc|http://x/?a=1&b=2]' ]] || fail "remote_script args over ssh: $got"
rm -rf "$sshstub"

# A stub systemctl that claims the unit is active.
stub=$(mktemp -d)
cat >"$stub/systemctl" <<'EOF'
#!/bin/bash
if [[ "$1" == is-active ]]; then exit 0; fi
exit 1
EOF
chmod +x "$stub/systemctl"
if PATH="$stub:$PATH" refuse_build_on_validator; then fail "build was allowed while aetherd is active"; fi
rm -rf "$stub"

bash "$HERE/build-aetherd.sh" --help >/dev/null
bash "$HERE/cutover.sh" --help >/dev/null
bash "$HERE/rollback.sh" --help >/dev/null

# --- on-node swap script, with curl and systemctl stubbed ---
A1=$(pad40 CCA35D28)
A2=$(pad40 BCEF78B5)
A3=$(pad40 F94934BD)
A4=$(pad40 B7F08625)

write_chain_stubs() {
  local dir=$1 heavy=$2 height=$3
  local p1=25 p2=25 p3=25 p4=25
  if [[ "$heavy" -eq 1 ]]; then
    p1=40; p2=20; p3=20; p4=20
  fi
  jq -n --arg h "$height" --arg v "$A1" \
    '{result:{node_info:{network:"aether-testnet-1",moniker:"sync3"},sync_info:{latest_block_height:$h,catching_up:false},validator_info:{address:$v}}}' \
    >"$dir/status.json"
  jq -n --arg a1 "$A1" --arg a2 "$A2" --arg a3 "$A3" --arg a4 "$A4" \
    --arg p1 "$p1" --arg p2 "$p2" --arg p3 "$p3" --arg p4 "$p4" \
    '{result:{n_peers:"3"}}' >"$dir/net.json"
  jq -n --arg a1 "$A1" --arg a2 "$A2" --arg a3 "$A3" --arg a4 "$A4" \
    --arg p1 "$p1" --arg p2 "$p2" --arg p3 "$p3" --arg p4 "$p4" \
    '{result:{block_height:"100",total:"4",count:"4",validators:[
      {address:$a1,voting_power:$p1},{address:$a2,voting_power:$p2},
      {address:$a3,voting_power:$p3},{address:$a4,voting_power:$p4}
    ]}}' >"$dir/validators.json"
  jq -n --arg a1 "$A1" --arg a2 "$A2" --arg a3 "$A3" --arg a4 "$A4" \
    '{result:{block:{last_commit:{signatures:[
      {block_id_flag:2,validator_address:$a1},{block_id_flag:2,validator_address:$a2},
      {block_id_flag:2,validator_address:$a3},{block_id_flag:2,validator_address:$a4}
    ]}}}}' >"$dir/block.json"
  cat >"$dir/curl" <<EOF
#!/bin/bash
url=""
for a in "\$@"; do url="\$a"; done
case "\$url" in
  */status) cat "$dir/status.json" ;;
  */net_info) cat "$dir/net.json" ;;
  */block) cat "$dir/block.json" ;;
  */validators*) cat "$dir/validators.json" ;;
  *) echo "stub curl: \$url" >&2; exit 1 ;;
esac
EOF
  cat >"$dir/systemctl" <<EOF
#!/bin/bash
if [[ "\$1" == show ]]; then
  echo "$dir/aetherd start --home /root/.aether"
  exit 0
fi
echo "\$*" >> "$dir/systemctl.log"
if [[ "\$1" == start && -n "\${FAIL_START:-}" ]]; then
  exit 1
fi
exit 0
EOF
  chmod +x "$dir/curl" "$dir/systemctl"
  echo old >"$dir/aetherd"
  echo new >"$dir/new-aetherd"
}

run_swap() {
  local dir=$1
  shift
  PATH="$dir:$PATH" bash "$HERE/remote-swap.sh" \
    --unit aetherd --stage "$dir/new-aetherd" --suffix .pre-cutover \
    --sha256 "$(sha256sum "$dir/new-aetherd" | awk '{print $1}')" \
    --rpc "http://127.0.0.1:26657" --validator "$A1" --min-peers 1 \
    --chain-id aether-testnet-1 "$@"
}

d=$(mktemp -d)
write_chain_stubs "$d" 0 100
run_swap "$d" --activation-height 500
[[ "$(cat "$d/aetherd")" == new ]] || fail "binary was not replaced"
[[ "$(cat "$d/aetherd.pre-cutover")" == old ]] || fail "backup missing"
grep -q stop "$d/systemctl.log" || fail "did not stop"
grep -q start "$d/systemctl.log" || fail "did not start"
rm -rf "$d"

d=$(mktemp -d)
write_chain_stubs "$d" 1 100
if PATH="$d:$PATH" run_swap "$d" --activation-height 500; then
  fail "unsafe validator was stopped"
fi
[[ ! -f "$d/systemctl.log" ]] || fail "systemctl ran for an unsafe stop: $(cat "$d/systemctl.log")"
[[ "$(cat "$d/aetherd")" == old ]] || fail "binary changed on unsafe stop"
rm -rf "$d"

d=$(mktemp -d)
write_chain_stubs "$d" 1 100
run_swap "$d" --activation-height 500 --accept-halt 2>/dev/null
[[ "$(cat "$d/aetherd")" == new ]] || fail "--accept-halt did not swap the heavy validator"
rm -rf "$d"

d=$(mktemp -d)
write_chain_stubs "$d" 0 100
# Only the heavy-looking failure mode: drop signers down to one.
jq -n --arg a1 "$A1" '{result:{block:{last_commit:{signatures:[{block_id_flag:2,validator_address:$a1}]}}}}' >"$d/block.json"
if run_swap "$d" --activation-height 500; then
  fail "swap proceeded without a 2/3 commit"
fi
[[ ! -f "$d/systemctl.log" ]] || fail "systemctl ran without quorum"
rm -rf "$d"

d=$(mktemp -d)
write_chain_stubs "$d" 0 100
if FAIL_START=1 run_swap "$d" --activation-height 500; then
  fail "failed start returned success"
fi
[[ "$(cat "$d/aetherd")" == old ]] || fail "failed start below activation left the new binary: $(cat "$d/aetherd")"
[[ -f "$d/aetherd.pre-cutover" ]] || fail "backup missing after failed start"
rm -rf "$d"

d=$(mktemp -d)
write_chain_stubs "$d" 0 500
if FAIL_START=1 run_swap "$d" --activation-height 500; then
  fail "failed start at activation returned success"
fi
[[ "$(cat "$d/aetherd")" == new ]] || fail "at activation the new binary should stay in place"
rm -rf "$d"

# Rollback script refuses at the activation height and restores below it.
d=$(mktemp -d)
write_chain_stubs "$d" 0 500
echo previous >"$d/aetherd.pre-cutover"
echo current >"$d/aetherd"
if PATH="$d:$PATH" bash "$HERE/remote-rollback.sh" --unit aetherd --suffix .pre-cutover --activation-height 500 --rpc "http://127.0.0.1:26657"; then
  fail "rollback at activation was allowed"
fi
[[ "$(cat "$d/aetherd")" == current ]] || fail "rollback changed the binary at activation"
rm -rf "$d"

d=$(mktemp -d)
write_chain_stubs "$d" 0 100
echo previous >"$d/aetherd.pre-cutover"
echo current >"$d/aetherd"
PATH="$d:$PATH" bash "$HERE/remote-rollback.sh" --unit aetherd --suffix .pre-cutover --activation-height 500 --rpc "http://127.0.0.1:26657"
[[ "$(cat "$d/aetherd")" == previous ]] || fail "rollback did not restore"
grep -q stop "$d/systemctl.log" || fail "rollback did not stop"
rm -rf "$d"

# The node's own RPC is down (its new binary won't start): the height
# rollback.sh read from the other nodes decides.
d=$(mktemp -d)
write_chain_stubs "$d" 0 100
printf '#!/bin/bash\nexit 7\n' >"$d/curl"
echo previous >"$d/aetherd.pre-cutover"
echo current >"$d/aetherd"
if PATH="$d:$PATH" bash "$HERE/remote-rollback.sh" --unit aetherd --activation-height 500 --rpc "http://127.0.0.1:26657" 2>/dev/null; then
  fail "rollback with no height at all was allowed"
fi
[[ "$(cat "$d/aetherd")" == current ]] || fail "rollback without a height changed the binary"
PATH="$d:$PATH" bash "$HERE/remote-rollback.sh" --unit aetherd --activation-height 500 --rpc "http://127.0.0.1:26657" --height 100
[[ "$(cat "$d/aetherd")" == previous ]] || fail "rollback of a down node did not restore"
echo current >"$d/aetherd"
if PATH="$d:$PATH" bash "$HERE/remote-rollback.sh" --unit aetherd --activation-height 500 --rpc "http://127.0.0.1:26657" --height 500 2>/dev/null; then
  fail "rollback at activation (height given) was allowed"
fi
rm -rf "$d"

# --- cutover.sh and rollback.sh against a fake remote ---
A1=$(pad40 CCA35D28)
NEW=$(mktemp)
echo 'binary-bytes' >"$NEW"
NEWSHA=$(sha256sum "$NEW" | awk '{print $1}')
NODES=$(mktemp)
printf 'sync3 local http://127.0.0.1:26657 %s aetherd\n' "$A1" >"$NODES"
SEEN=$(mktemp -u)
export NEWSHA A1 SEEN
export VALJSON ADDRS FAKE_HEIGHT
VALJSON=$(jq -n --arg a1 "$A1" --arg a2 "$(pad40 BCEF78B5)" --arg a3 "$(pad40 F94934BD)" --arg a4 "$(pad40 B7F08625)" \
  '[{address:$a1,voting_power:"25"},{address:$a2,voting_power:"25"},{address:$a3,voting_power:"25"},{address:$a4,voting_power:"25"}]')
ADDRS=$(jq -n --arg a1 "$A1" --arg a2 "$(pad40 BCEF78B5)" --arg a3 "$(pad40 F94934BD)" --arg a4 "$(pad40 B7F08625)" '[$a1,$a2,$a3,$a4]')
FAKE_HEIGHT=100

fake_remote() {
  local dest=$1
  shift
  local line="$*"
  case "$line" in
    "id -u") echo 0 ;;
    # The checksum command also names MainPID; match it first so the
    # running check (MainPID alone) can still succeed.
    *sha256sum*)
      if [[ -f "$SEEN" ]]; then
        echo "$NEWSHA  /proc/1/exe"
      else
        echo "0000000000000000000000000000000000000000000000000000000000000000  /proc/1/exe"
      fi
      ;;
    *MainPID*) return 0 ;;
    *curl*)
      local url="${!#}" h
      h=${FAKE_HEIGHT:-100}
      if [[ -f "$SEEN" ]]; then h=101; fi
      case "$url" in
        */status)
          jq -n --arg h "$h" --arg v "$A1" '{result:{node_info:{network:"aether-testnet-1",moniker:"sync3"},sync_info:{latest_block_height:$h,catching_up:false},validator_info:{address:$v}}}'
          ;;
        */net_info) jq -n '{result:{n_peers:"3"}}' ;;
        */block)
          jq -n --argjson addrs "$ADDRS" '{result:{block:{last_commit:{signatures:[ $addrs[] | {block_id_flag:2, validator_address:.} ]}}}}'
          ;;
        */validators*)
          jq -n --argjson vals "$VALJSON" '{result:{block_height:"100",validators:$vals,count:($vals|length|tostring),total:($vals|length|tostring)}}'
          ;;
        *) echo "unexpected url $url" >&2; return 1 ;;
      esac
      ;;
    *) echo "unexpected remote: $line" >&2; return 1 ;;
  esac
}
fake_copy() { echo "copy $*" >>"$SEEN.copy"; }
fake_script() { echo "$*" >"$SEEN"; }

export -f fake_remote fake_copy fake_script
export REMOTE_EXEC_OVERRIDE=fake_remote
export REMOTE_COPY_OVERRIDE=fake_copy
export REMOTE_SCRIPT_OVERRIDE=fake_script

out=$(bash "$HERE/cutover.sh" --nodes "$NODES" --activation-height 500 \
  --binary "$NEW" --sha256 "$NEWSHA" --built-with go1.26.10 \
  --chain-id aether-testnet-1)
[[ "$out" == *dry-run* && "$out" == *remote-swap.sh* ]] || fail "dry-run output: $out"
[[ ! -f "$SEEN" ]] || fail "dry-run invoked the swap"
echo "dry-run ok"

rm -f "$SEEN" "$SEEN.copy"
out=$(bash "$HERE/cutover.sh" --apply --nodes "$NODES" --activation-height 500 \
  --binary "$NEW" --sha256 "$NEWSHA" --built-with go1.26.10 \
  --chain-id aether-testnet-1 --wait-timeout 5 --poll-every 1)
[[ -f "$SEEN" ]] || fail "apply did not run remote-swap"
[[ "$out" == *"is signing"* ]] || fail "apply output: $out"
echo "apply ok"
rm -f "$SEEN" "$SEEN.copy"

VALJSON=$(jq -n --arg a1 "$A1" --arg a2 "$(pad40 BCEF78B5)" --arg a3 "$(pad40 F94934BD)" --arg a4 "$(pad40 B7F08625)" \
  '[{address:$a1,voting_power:"40"},{address:$a2,voting_power:"20"},{address:$a3,voting_power:"20"},{address:$a4,voting_power:"20"}]')
export VALJSON
rc=0
out=$(bash "$HERE/cutover.sh" --nodes "$NODES" --activation-height 500 \
  --binary "$NEW" --sha256 "$NEWSHA" --built-with go1.26.10 --chain-id aether-testnet-1 2>&1) || rc=$?
[[ "$rc" -eq 1 ]] || fail "unsafe cutover exit $rc: $out"
[[ "$out" == *unsafe* ]] || fail "unsafe cutover text: $out"
[[ ! -f "$SEEN" ]] || fail "unsafe cutover invoked the swap"
echo "unsafe preflight ok"

rc=0
out=$(bash "$HERE/cutover.sh" --nodes "$NODES" --activation-height 500 --accept-halt \
  --binary "$NEW" --sha256 "$NEWSHA" --built-with go1.26.10 --chain-id aether-testnet-1 2>&1) || rc=$?
[[ "$rc" -eq 2 && "$out" == *"needs --only"* ]] || fail "--accept-halt without --only ($rc): $out"
out=$(bash "$HERE/cutover.sh" --apply --nodes "$NODES" --only sync3 --accept-halt --activation-height 500 \
  --binary "$NEW" --sha256 "$NEWSHA" --built-with go1.26.10 --chain-id aether-testnet-1 --wait-timeout 5 --poll-every 1 2>&1)
[[ -f "$SEEN" && "$(cat "$SEEN")" == *--accept-halt* ]] || fail "--accept-halt did not reach remote-swap: $(cat "$SEEN" 2>/dev/null)"
[[ "$out" == *WARNING* && "$out" == *"is signing"* ]] || fail "--accept-halt output: $out"
rm -f "$SEEN" "$SEEN.copy"
echo "accept-halt ok"

# Restore an even set for rollback, which only gates on height.
VALJSON=$(jq -n --arg a1 "$A1" '[{address:$a1,voting_power:"25"}]')
export VALJSON
unset FAKE_HEIGHT
export FAKE_HEIGHT=100
rc=0
out=$(bash "$HERE/rollback.sh" --nodes "$NODES" --name sync3 --activation-height 500) || rc=$?
[[ "$rc" -eq 0 && "$out" == *dry-run* && "$out" == *remote-rollback.sh* ]] || fail "rollback dry-run ($rc): $out"
[[ ! -f "$SEEN" ]] || fail "rollback dry-run ran the script"
echo "rollback dry-run ok"

export FAKE_HEIGHT=500
rc=0
out=$(bash "$HERE/rollback.sh" --apply --nodes "$NODES" --name sync3 --activation-height 500 2>&1) || rc=$?
[[ "$rc" -eq 2 ]] || fail "rollback at activation exit $rc: $out"
[[ ! -f "$SEEN" ]] || fail "rollback at activation ran the script"
echo "rollback refusal ok"

# The node being rolled back is down; another node in the file gives the
# height. With every node down there is no height and no rollback.
export FAKE_HEIGHT=100
NODES2=$(mktemp)
printf 'sync3 down@node http://127.0.0.1:26657 %s aetherd\nsync4 local http://127.0.0.1:26657 %s aetherd\n' "$A1" "$(pad40 BCEF78B5)" >"$NODES2"
fake_remote_down() {
  if [[ "$1" == down@node && "$*" == *curl* ]]; then return 7; fi
  fake_remote "$@"
}
export -f fake_remote_down
rc=0
out=$(REMOTE_EXEC_OVERRIDE=fake_remote_down bash "$HERE/rollback.sh" --nodes "$NODES2" --name sync3 --activation-height 500 2>&1) || rc=$?
[[ "$rc" -eq 0 && "$out" == *"--height 100"* ]] || fail "rollback of a down node ($rc): $out"
printf 'sync3 down@node http://127.0.0.1:26657 %s aetherd\n' "$A1" >"$NODES2"
rc=0
out=$(REMOTE_EXEC_OVERRIDE=fake_remote_down bash "$HERE/rollback.sh" --apply --nodes "$NODES2" --name sync3 --activation-height 500 2>&1) || rc=$?
[[ "$rc" -eq 2 && "$out" == *"no node"* ]] || fail "rollback with every node down ($rc): $out"
[[ ! -f "$SEEN" ]] || fail "rollback with no height ran the script"
rm -f "$NODES2"
echo "rollback of a down node ok"

# Wrong Go version is refused before any remote call.
rc=0
bash "$HERE/cutover.sh" --nodes "$NODES" --activation-height 500 \
  --binary "$NEW" --sha256 "$NEWSHA" --built-with go1.25.8 >/dev/null 2>&1 || rc=$?
[[ "$rc" -ne 0 ]] || fail "wrong Go version was accepted"

echo "all ops script tests passed"
