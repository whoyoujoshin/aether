#!/usr/bin/env bash
# Makes a copy of the testnet's chain data that a new node can start from,
# instead of replaying every block since genesis (docs/JOIN-AS-VALIDATOR.md,
# step 4), and puts it where Caddy serves it:
#   https://explorer.157-245-252-221.sslip.io/snapshots/aether-testnet-1-latest.tar
#
# Run it on the seed. It copies sync4's data/ over ssh (a running LevelDB
# isn't a consistent copy, so the second pass runs with sync4 stopped for a
# minute or two), restarts sync4, waits until it signs again, and packs the
# copy into one .tar (no compression: LevelDB's files are already
# compressed) with config/genesis.json, a .sha256 and a .txt naming the
# height. The previous snapshot is removed; the staging copy is kept, so the
# next run sends only what changed.
#
#   SYNC4=root@<sync4 IP> bash scripts/snapshot/make-snapshot.sh
#
# It refuses to stop sync4 unless every validator signed the latest block
# and the others hold more than two thirds of the power without it. It never
# copies a key: only data/ (without priv_validator_state.json, which is each
# node's own, and the consensus WAL) and config/genesis.json.
#
# Environment:
#   SYNC4      ssh target of the node to copy (required)
#   VALIDATOR  that node's consensus address prefix (default BCEF78B5, sync4)
#   HOME_DIR   its node home (default /root/.aether)
#   OUT        where the snapshot goes, served by Caddy (default /srv/aether-snapshots)
#   STAGE      the staging copy, not served (default /srv/aether-snapshot-stage)
#   RPC        an RPC that sees every validator (default http://127.0.0.1:26657)
set -euo pipefail
: "${SYNC4:?set SYNC4 to the ssh target of the node to copy, e.g. root@<sync4 IP>}"
VALIDATOR=${VALIDATOR:-BCEF78B5}
HOME_DIR=${HOME_DIR:-/root/.aether}
OUT=${OUT:-/srv/aether-snapshots}
RPC=${RPC:-http://127.0.0.1:26657}
CHAIN=aether-testnet-1
STAGE=${STAGE:-/srv/aether-snapshot-stage}   # outside OUT, so Caddy never serves it
SLOW="nice -n 19 ionice -c3"

say() { echo "[$(date -u +%H:%M:%S)] $*"; }

# 1. Is it safe to stop that node?
safe=$(curl -s "$RPC/block" | python3 -c '
import json, sys, urllib.request
rpc, me = sys.argv[1], sys.argv[2].upper()
b = json.load(sys.stdin)["result"]["block"]
h = int(b["header"]["height"])
signed = {s["validator_address"] for s in b["last_commit"]["signatures"] if s.get("block_id_flag") == 2}
vals = json.load(urllib.request.urlopen(f"{rpc}/validators?height={h}&per_page=100"))["result"]["validators"]
total = sum(int(v["voting_power"]) for v in vals)
mine = sum(int(v["voting_power"]) for v in vals if v["address"].startswith(me))
missing = [v["address"][:8] for v in vals if v["address"] not in signed]
if not mine:
    print(f"no: {me} is not in the validator set at {h}")
elif missing:
    print(f"no: not every validator signed block {h - 1} (missing {missing})")
elif (total - mine) * 3 <= total * 2:
    print(f"no: without {me} the others hold {100*(total-mine)/total:.1f}%, not over two thirds")
else:
    print(f"yes: all {len(vals)} signed; without {me} the others hold {100*(total-mine)/total:.1f}%")
' "$RPC" "$VALIDATOR")
say "safe to stop $VALIDATOR? $safe"
case "$safe" in yes*) ;; *) exit 1;; esac

# 2. Room for the staging copy and the tar on this disk.
need=$(ssh "$SYNC4" "du -sb $HOME_DIR/data | cut -f1")
mkdir -p "$OUT"
free=$(df -B1 --output=avail "$OUT" | tail -1)
staged=$(du -sb "$STAGE" 2>/dev/null | cut -f1 || echo 0)
if [ $(( free + staged )) -lt $(( need * 5 / 2 )) ]; then
  say "not enough disk: data is $((need >> 30)) GiB, $((free >> 30)) GiB free; want 2.5x the data"
  exit 1
fi
mkdir -p "$STAGE/data" "$STAGE/config" "$OUT"
chmod 755 "$OUT"

EXCL=(--exclude priv_validator_state.json --exclude cs.wal)

# 3. First pass, with the node running: most of the data.
say "first pass from $SYNC4 (node running)"
$SLOW rsync -a --delete "${EXCL[@]}" "$SYNC4:$HOME_DIR/data/" "$STAGE/data/"
rsync -a "$SYNC4:$HOME_DIR/config/genesis.json" "$STAGE/config/genesis.json"

# 4. Second pass with the node stopped: a consistent copy.
height=$(ssh "$SYNC4" "curl -s localhost:26657/status" | python3 -c 'import json,sys;print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])')
say "stopping aetherd on $SYNC4 at about block $height"
ssh "$SYNC4" "systemctl stop aetherd"
restart() { ssh "$SYNC4" "systemctl start aetherd" && say "aetherd started on $SYNC4"; }
trap restart EXIT
$SLOW rsync -a --delete "${EXCL[@]}" "$SYNC4:$HOME_DIR/data/" "$STAGE/data/"
trap - EXIT
restart

# 5. Wait until it signs again.
for i in $(seq 1 60); do
  sleep 5
  if curl -s "$RPC/block" | python3 -c '
import json,sys
me=sys.argv[1].upper()
sigs=json.load(sys.stdin)["result"]["block"]["last_commit"]["signatures"]
sys.exit(0 if any(s["validator_address"].startswith(me) and s.get("block_id_flag")==2 for s in sigs) else 1)' "$VALIDATOR"; then
    say "$VALIDATOR is signing again"
    break
  fi
  [ "$i" = 60 ] && say "WARNING: $VALIDATOR not signing after 5 minutes; check it now"
done

# 6. Pack it.
name=$CHAIN-$height.tar
say "packing $name"
$SLOW tar -cf "$OUT/$name.part" -C "$STAGE" data config/genesis.json
mv "$OUT/$name.part" "$OUT/$name"
(cd "$OUT" && sha256sum "$name" > "$name.sha256")
cat > "$OUT/$name.txt" <<EOF
chain:   $CHAIN
height:  $height (about; the copy holds the blocks up to when $VALIDATOR stopped)
made:    $(date -u +%Y-%m-%dT%H:%M:%SZ)
from:    $VALIDATOR, running $(ssh "$SYNC4" "sha256sum /proc/\$(systemctl show -p MainPID --value aetherd)/exe | cut -c1-16")…
sha256:  $(cut -d' ' -f1 "$OUT/$name.sha256")
size:    $(du -h "$OUT/$name" | cut -f1)
EOF
for f in "" .sha256 .txt; do ln -sfn "$name$f" "$OUT/$CHAIN-latest.tar$f"; done
# The latest names point at this one; drop older snapshots.
find "$OUT" -maxdepth 1 -name "$CHAIN-*.tar*" ! -name "$name*" ! -name "$CHAIN-latest.tar*" -delete
chmod 644 "$OUT/$name" "$OUT/$name.sha256" "$OUT/$name.txt"
cat "$OUT/$name.txt"
say "served at https://explorer.157-245-252-221.sslip.io/snapshots/$CHAIN-latest.tar (once caddy.sh has run)"
