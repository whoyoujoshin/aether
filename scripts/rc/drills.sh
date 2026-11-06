#!/bin/bash
# The two aether-rc-1 drills (docs/RC-1.md) on a four-operator aether-rc
# devnet built by cmd/rcgenesis, before outside operators do them:
#
#   1. Rolling swap: each node in turn stops, gets a new aetherd binary
#      (same chain rules, different bytes) and starts again, while the
#      others keep signing. Blocks must never stop.
#   2. Planned halt and restart: every node sets halt-height = H in app.toml
#      and restarts once to read it (one at a time, as in 1). Every node
#      stops at H. Each then clears it and starts again: blocks resume once
#      more than two thirds of the power is back.
#
# Every operator mines (powminer --duty 0.15) and registers its consensus
# key from its first reward, as on genesis day, and every node checks the
# invariants each block (AETHER_INVARIANTS_EVERY=1, HALT=yes). For speed
# only: difficulty 20,000, 20-block epochs and 2 s blocks; the operators'
# validators have no sentries. Takes about 15 minutes on 4 cores and uses
# ports 26056-26358, 9090-9390 and 1017-1317.
#
#   scripts/rc/drills.sh            # work dir: $TMPDIR/aether-drills
#   WORK=/some/dir scripts/rc/drills.sh
#
# Results in docs/RC-1.md ("Rehearsed"). Stop leftovers with
#   pkill -f "aether-drills/"
set -u
REPO=$(cd "$(dirname "$0")/../.." && pwd)
S=${WORK:-${TMPDIR:-/tmp}/aether-drills}
pkill -f "$S/" 2>/dev/null; sleep 1
rm -rf "${S:?}"; mkdir -p $S/bin/v1 $S/bin/v2
cd "$REPO"
for c in aetherd rcgenesis powminer validatorkeygen; do go build -trimpath -o $S/bin/v1/$c ./cmd/$c || { echo "FAIL build $c"; exit 1; }; done
# The release the coordinator tags for the swap: the same code, so the same
# chain rules, but its own bytes (a version string), like a real point release.
go build -trimpath -ldflags "-X github.com/cosmos/cosmos-sdk/version.Version=rc-drill-v2" -o $S/bin/v2/aetherd ./cmd/aetherd || exit 1
echo "v1 aetherd $(sha256sum $S/bin/v1/aetherd | cut -c1-16)…  v2 aetherd $(sha256sum $S/bin/v2/aetherd | cut -c1-16)…"
B=$S/bin/v1/aetherd; CID=aether-rc-drills; PRICE=0.0001uaeth
OPS="a b c d"
off() { case $1 in a) echo 0;; b) echo 1;; c) echo 2;; d) echo 3;; esac; }
rpcp() { echo 26$(off $1)57; }
grpcp() { echo 9$(off $1)90; }

for op in $OPS; do
  H=$S/$op/.aether
  HOME=$S/$op $B init $op --chain-id $CID --home $H >/dev/null 2>&1
  HOME=$S/$op $B keys add miner --keyring-backend test --home $H >/dev/null 2>&1
  HOME=$S/$op $B keys show miner -a --keyring-backend test --home $H > $S/m$op
  cp $B $S/$op/aetherd # each operator runs its own copy, swapped in place
done
python3 - "$S" <<'PYEOF'
import json,subprocess,sys
S=sys.argv[1]; ops=[]
for op in "abcd":
    out=subprocess.run([S+"/bin/v1/aetherd","comet","show-validator","--home",f"{S}/{op}/.aether"],capture_output=True,text=True,env={"HOME":f"{S}/{op}"})
    ops.append({"name":"operator-"+op,"consensus_pubkey":json.loads(out.stdout or out.stderr),"miner":open(f"{S}/m{op}").read().strip()})
json.dump(ops,open(S+"/operators.json","w"),indent=1)
PYEOF
GT=$(date -u -d '+20 seconds' +%Y-%m-%dT%H:%M:%SZ)
$S/bin/v1/rcgenesis --base $S/a/.aether/config/genesis.json --operators $S/operators.json --chain-id $CID --genesis-time $GT --out $S/genesis.json || exit 1
python3 - "$S/genesis.json" <<'PYEOF'
import json,sys
p=sys.argv[1]; g=json.load(open(p))
g['app_state']['pow']['params'].update(initial_difficulty=20000,min_difficulty=1000,difficulty=20000,epoch_length=20,target_block_time=2)
json.dump(g,open(p,'w'),indent=1)
PYEOF
for op in $OPS; do cp $S/genesis.json $S/$op/.aether/config/genesis.json; done

# Config as RC-1.md's checklist has it (but 2 s blocks), on per-operator
# ports, every validator peering with the other three.
for op in $OPS; do
  o=$(off $op); C=$S/$op/.aether/config
  sed -i -e 's/^timeout_commit = .*/timeout_commit = "2s"/' -e 's/^pprof_laddr = .*/pprof_laddr = ""/' -e 's/^addr_book_strict = .*/addr_book_strict = false/' -e 's/^allow_duplicate_ip = .*/allow_duplicate_ip = true/' \
    -e '/^\[mempool\]/,/^\[/ s/^size = .*/size = 20000/' \
    -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:26${o}57\"#" -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:26${o}56\"#" -e "s#^proxy_app = .*#proxy_app = \"tcp://127.0.0.1:26${o}58\"#" $C/config.toml
  sed -i -e "s#address = \"localhost:9090\"#address = \"localhost:9${o}90\"#" -e "s#address = \"0.0.0.0:9090\"#address = \"0.0.0.0:9${o}90\"#" -e "s#address = \"tcp://localhost:1317\"#address = \"tcp://localhost:1${o}17\"#" -e "s#address = \"tcp://0.0.0.0:1317\"#address = \"tcp://0.0.0.0:1${o}17\"#" \
    -e "s/^minimum-gas-prices = .*/minimum-gas-prices = \"$PRICE\"/" -e '/^\[mempool\]/,/^\[/ s/^max-txs = .*/max-txs = 20000/' $C/app.toml
done
for op in $OPS; do echo "$(HOME=$S/$op $B comet show-node-id --home $S/$op/.aether 2>&1 | tail -1)@127.0.0.1:26$(off $op)56" > $S/id$op; done
for op in $OPS; do
  peers=$(for p in $OPS; do [ $p != $op ] && cat $S/id$p; done | paste -sd,)
  sed -i "s#^persistent_peers = .*#persistent_peers = \"$peers\"#" $S/$op/.aether/config/config.toml
  python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["address"])' $S/$op/.aether/config/priv_validator_key.json > $S/cons$op
done

start() { # op
  HOME=$S/$1 AETHER_INVARIANTS_EVERY=1 AETHER_INVARIANTS_HALT=yes nohup $S/$1/aetherd start --home $S/$1/.aether >> $S/$1.log 2>&1 &
  echo $! > $S/pid$1
}
stop() { # op
  local p; p=$(cat $S/pid$1); kill $p 2>/dev/null
  for i in $(seq 1 30); do kill -0 $p 2>/dev/null || return 0; sleep 1; done
  kill -9 $p 2>/dev/null
}
alive() { kill -0 $(cat $S/pid$1) 2>/dev/null && echo up || echo exited; }
tipof() { curl -s -m 3 localhost:$(rpcp $1)/status | python3 -c 'import json,sys;print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])' 2>/dev/null || echo -; }
tip() { for op in $OPS; do t=$(tipof $op); [ "$t" != - ] && { echo $t; return; }; done; echo 0; }
anyrpc() { for op in $OPS; do [ "$(tipof $op)" != - ] && { rpcp $op; return; }; done; rpcp a; }
vals() { python3 - "$S" "$(anyrpc)" <<'PYEOF' 2>/dev/null
import json,sys,urllib.request
S,port=sys.argv[1:]
name={open(f"{S}/cons{o}").read().strip():o for o in "abcd"}
v=json.load(urllib.request.urlopen(f"http://localhost:{port}/validators?per_page=50"))["result"]
t=sum(int(x["voting_power"]) for x in v["validators"])
print("powers at", v["block_height"]+":", " ".join("%s %.1f%%"%(name.get(x["address"],x["address"][:6]),100*int(x["voting_power"])/t) for x in v["validators"]))
PYEOF
}
# signers H: which operators signed block H (the commit in block H+1).
signers() { python3 - "$S" "$(anyrpc)" "$1" <<'PYEOF' 2>/dev/null
import json,sys,urllib.request
S,port,h=sys.argv[1:]
name={open(f"{S}/cons{o}").read().strip():o for o in "abcd"}
c=json.load(urllib.request.urlopen(f"http://localhost:{port}/commit?height={h}"))["result"]["signed_header"]["commit"]["signatures"]
print("".join(sorted(name.get(s["validator_address"],"?") for s in c if s.get("signature"))))
PYEOF
}
waitsigns() { # op [from]: until op signs a block made after it restarted (the tip when this is called), up to 120 s
  local t0=${2:-$(tip)}
  for i in $(seq 1 60); do t=$(tip); [ "$t" -gt $((t0+1)) ] && signers $((t-1)) | grep -q $1 && { echo $((t-1)); return 0; }; sleep 2; done; return 1
}
powerof() { vals | grep -o "$1 [0-9.]*%" | grep -o '[0-9.]*'; }
# Block intervals from the headers of blocks $1..$2: the longest gap.
gaps() { python3 - "$(anyrpc)" "$1" "$2" <<'PYEOF'
import json,sys,urllib.request
from datetime import datetime
port,a,b=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
ts=[]
for lo in range(a,b+1,20):
    r=json.load(urllib.request.urlopen(f"http://localhost:{port}/blockchain?minHeight={lo}&maxHeight={min(lo+19,b)}"))["result"]["block_metas"]
    ts+= [(int(m["header"]["height"]),m["header"]["time"]) for m in r]
ts.sort()
f=lambda s: datetime.fromisoformat(s[:26].rstrip("Z")+"+00:00" if "." in s else s.replace("Z","+00:00"))
d=[((f(ts[i+1][1])-f(ts[i][1])).total_seconds(),ts[i+1][0]) for i in range(len(ts)-1)]
m=max(d); print("blocks %d-%d: %d blocks, mean %.1f s, longest gap %.1f s (before block %d)"%(a,b,len(ts),sum(x for x,_ in d)/len(d),m[0],m[1]))
PYEOF
}
bal() { HOME=$S/a $B q bank balances $1 --node tcp://localhost:$(anyrpc) -o json 2>/dev/null | python3 -c 'import json,sys;b=json.load(sys.stdin)["balances"];print(next((x["amount"] for x in b if x["denom"]=="uaeth"),"0"))' 2>/dev/null || echo 0; }
mine() { # op
  HOME=$S/$1 nohup $S/bin/v1/powminer --miner $(cat $S/m$1) --from miner --keyring-backend test --chain-id $CID \
    --rpc http://127.0.0.1:$(rpcp $1) --grpc localhost:$(grpcp $1) --auto-submit --loop --fees 0uaeth --duty 0.15 >> $S/miner-$1.log 2>&1 &
}
register() { # op
  local cmd; cmd=$($S/bin/v1/validatorkeygen --miner $(cat $S/m$1) --home $S/$1/.aether --chain-id $CID --fees 200uaeth | grep '^aetherd tx pow register')
  cmd=${cmd#aetherd }
  HOME=$S/$1 $B ${cmd} --node tcp://127.0.0.1:$(rpcp $1) --gas 400000 -o json 2>&1 | python3 -c 'import json,sys
try: r=json.loads(sys.stdin.read()); print("register code", r.get("code"), r.get("raw_log","")[:120])
except Exception as e: print("register: not json")'
}
apphash() { # height: each node's app hash for the block after it (agreement check)
  for op in $OPS; do printf "%s %s  " $op "$(curl -s -m 3 "localhost:$(rpcp $op)/block?height=$1" | python3 -c 'import json,sys;print(json.load(sys.stdin)["result"]["block"]["header"]["app_hash"][:12])' 2>/dev/null || echo -)"; done; echo
}

echo "=== genesis at $GT"
for op in $OPS; do start $op; done
for i in $(seq 1 120); do [ "$(tip)" -ge 3 ] && break; sleep 1; done
echo "tip $(tip); block 2 signed by: $(signers 2)"; vals

echo "=== every operator mines, registers its key from its first reward"
for op in $OPS; do mine $op; done
for op in $OPS; do
  for i in $(seq 1 150); do [ "$(bal $(cat $S/m$op))" -gt 1000 ] && break; sleep 2; done
  echo "$op balance $(bal $(cat $S/m$op))"; register $op
done
# Let the seat rule settle for a few epochs.
for i in $(seq 1 6); do sleep 20; echo "tip $(tip): $(vals)"; done

echo "=== drill 1: rolling swap to v2"
D1=$(tip)
for op in $OPS; do
  echo "--- $op (tip $(tip); $(vals))"
  p=$(powerof $op); python3 -c "import sys; sys.exit(0 if float('$p')<=100/3 else 1)" || echo "  NOTE: $op holds $p%, over a third: blocks wait while it is down"
  t0=$(tip); stop $op
  sleep 6; echo "  $op down: tip $(tip), block $(( $(tip)-1 )) signed by $(signers $(( $(tip)-1 )))"
  cp $S/bin/v2/aetherd $S/$op/aetherd
  start $op
  if h=$(waitsigns $op $t0); then echo "  $op on v2, signed block $h ($(( h - t0 )) blocks after it stopped)"; else echo "  FAIL: $op not signing after 120 s"; fi
done
D1E=$(tip)
gaps $D1 $D1E
echo "version: $(HOME=$S/a $S/a/aetherd version 2>&1)"
apphash $((D1E-1))

echo "=== drill 2: planned halt at H"
HH=$(( $(tip) + 90 ))
echo "H = $HH (tip $(tip))"
for op in $OPS; do
  sed -i "s/^halt-height = .*/halt-height = $HH/" $S/$op/.aether/config/app.toml
  t0=$(tip); stop $op; start $op
  if h=$(waitsigns $op $t0); then echo "  $op restarted with halt-height $HH, signed block $h"; else echo "  FAIL: $op not signing"; fi
done
echo "waiting for H"
for i in $(seq 1 150); do
  t=$(tip); [ "$t" -ge $((HH-1)) ] && break; sleep 2
done
sleep 20
for op in $OPS; do echo "  $op: process $(alive $op), last block $(tipof $op)"; done
for op in $OPS; do echo "  $op log: $(grep -m1 -o 'halt per configuration[^"]*' $S/$op.log || echo 'no halt message'); $(grep -c 'CONSENSUS FAILURE' $S/$op.log) CONSENSUS FAILURE line(s)"; done
echo "  block $HH stored on a: $(curl -s -m 3 "localhost:$(rpcp a)/block?height=$HH" | python3 -c 'import json,sys;print(json.load(sys.stdin)["result"]["block"]["header"]["height"])' 2>/dev/null || echo no)"
for op in $OPS; do [ "$(alive $op)" = up ] && stop $op; done
echo "every node stopped; clearing halt-height and starting one at a time"
for op in $OPS; do
  sed -i "s/^halt-height = .*/halt-height = 0/" $S/$op/.aether/config/app.toml
  start $op; sleep 25
  echo "  after starting $op ($(powerof $op)% of the power): tip $(tip)"
done
for i in $(seq 1 30); do [ "$(tip)" -ge $((HH+10)) ] && break; sleep 2; done
for op in $OPS; do h=$(waitsigns $op $HH) && echo "  $op signed block $h" || echo "  FAIL: $op not signing"; done
echo "block $HH signed by $(signers $HH); block $((HH+5)) by $(signers $((HH+5)))"
apphash $((HH+5))
gaps $((HH-5)) $((HH+10))
for i in $(seq 1 3); do sleep 20; echo "tip $(tip): $(vals)"; done
echo "=== invariant breaks logged across all nodes: $(cat $S/?.log | grep -c 'invariant broken')"
echo "The devnet keeps running; stop it with: pkill -f \"$S/\""
