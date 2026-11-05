#!/bin/bash
# A staged takeover on an aether-rc devnet, to check that the alerts fire:
# two honest operators (A and B, genesis validators from cmd/rcgenesis)
# register their keys and mine lightly; then an attacker C, a node that
# wasn't in genesis, opens a miner account, registers its own consensus key
# and mines flat out. minerwatch watches node A. We expect validator_new
# when C first gains power and power_concentrated once C holds over a
# third, and the seat rule to move power at most 10 points per epoch.
# For speed only: difficulty 20,000 and 20-block epochs (2 s blocks).
# Takes about 15 minutes on 4 cores, and uses ports 26157-26258,
# 26656-26658, 9090, 9190, 9290 and 1317-1417.
#
#   scripts/rc/staged-takeover.sh            # work dir: $TMPDIR/aether-takeover
#   WORK=/some/dir scripts/rc/staged-takeover.sh
#
# Results in docs/BUG-HUNT.md ("Alerts"). Stop leftovers with
#   pkill -f "aether-takeover/bin/"
set -u
REPO=$(cd "$(dirname "$0")/../.." && pwd)
S=${WORK:-${TMPDIR:-/tmp}/aether-takeover}
pkill -f "$S/bin/" 2>/dev/null; sleep 1
rm -rf "${S:?}"; mkdir -p $S/bin
cd "$REPO"
for c in aetherd rcgenesis powminer validatorkeygen minerwatch; do go build -o $S/bin/$c ./cmd/$c || { echo "FAIL build $c"; exit 1; }; done
B=$S/bin/aetherd; CID=aether-rc-takeover; PRICE=0.0005uaeth

for op in a b c; do
  H=$S/$op/.aether
  HOME=$S/$op $B init $op --chain-id $CID --home $H >/dev/null 2>&1
  HOME=$S/$op $B keys add miner --keyring-backend test --home $H >/dev/null 2>&1
  HOME=$S/$op $B keys show miner -a --keyring-backend test --home $H > $S/m$op
done
MA=$(cat $S/ma); MB=$(cat $S/mb); MC=$(cat $S/mc)
python3 - "$S" "$MA" "$MB" <<'PYEOF'
import json,subprocess,sys
S,ma,mb=sys.argv[1:]
ops=[]
for op,m in (("a",ma),("b",mb)):
    out=subprocess.run([S+"/bin/aetherd","comet","show-validator","--home",f"{S}/{op}/.aether"],capture_output=True,text=True,env={"HOME":f"{S}/{op}"})
    ops.append({"name":"operator-"+op,"consensus_pubkey":json.loads(out.stdout or out.stderr),"miner":m})
json.dump(ops,open(S+"/operators.json","w"),indent=1)
PYEOF
GT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
$S/bin/rcgenesis --base $S/a/.aether/config/genesis.json --operators $S/operators.json --chain-id $CID --genesis-time $GT --out $S/genesis.json >/dev/null || exit 1
python3 - "$S/genesis.json" <<'PYEOF'
import json,sys
p=sys.argv[1]; g=json.load(open(p))
g['app_state']['pow']['params'].update(initial_difficulty=20000,min_difficulty=1000,difficulty=20000,epoch_length=20,target_block_time=2)
json.dump(g,open(p,'w'),indent=1)
PYEOF
for op in a b c; do cp $S/genesis.json $S/$op/.aether/config/genesis.json; done

# Ports: A on the defaults, B +100, C +200.
for op in a b c; do sed -i -e 's/^timeout_commit = .*/timeout_commit = "2s"/' -e 's/^pprof_laddr = .*/pprof_laddr = ""/' -e 's/^addr_book_strict = .*/addr_book_strict = false/' -e 's/^allow_duplicate_ip = .*/allow_duplicate_ip = true/' $S/$op/.aether/config/config.toml; done
IDA=$(HOME=$S/a $B comet show-node-id --home $S/a/.aether 2>&1 | tail -1)
for op in b c; do
  case $op in b) o=1;; c) o=2;; esac
  C=$S/$op/.aether/config
  sed -i -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:26${o}57\"#" -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:26${o}56\"#" -e "s#^proxy_app = .*#proxy_app = \"tcp://127.0.0.1:26${o}58\"#" \
    -e "s#^persistent_peers = .*#persistent_peers = \"$IDA@127.0.0.1:26656\"#" $C/config.toml
  sed -i -e "s#address = \"localhost:9090\"#address = \"localhost:9${o}90\"#" -e "s#address = \"0.0.0.0:9090\"#address = \"0.0.0.0:9${o}90\"#" -e "s#address = \"tcp://localhost:1317\"#address = \"tcp://localhost:1${o}17\"#" -e "s#address = \"tcp://0.0.0.0:1317\"#address = \"tcp://0.0.0.0:1${o}17\"#" $C/app.toml
done
for op in a b c; do HOME=$S/$op $B start --home $S/$op/.aether --minimum-gas-prices $PRICE > $S/$op.log 2>&1 & done

tip() { curl -s localhost:26657/status | python3 -c 'import json,sys;print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])' 2>/dev/null || echo 0; }
vals() { python3 - <<'PYEOF' 2>/dev/null
import json,urllib.request
v=json.load(urllib.request.urlopen("http://localhost:26657/validators?per_page=50"))["result"]
t=sum(int(x["voting_power"]) for x in v["validators"])
print(v["block_height"], " ".join("%s=%.1f%%"%(x["address"][:6],100*int(x["voting_power"])/t) for x in v["validators"]))
PYEOF
}
for i in $(seq 1 90); do [ "$(tip)" -ge 3 ] && break; sleep 1; done
CA=$(HOME=$S/a $B comet show-address --home $S/a/.aether 2>&1 | tail -1); CB=$(HOME=$S/b $B comet show-address --home $S/b/.aether 2>&1 | tail -1); CC=$(HOME=$S/c $B comet show-address --home $S/c/.aether 2>&1 | tail -1)
echo "consensus: A $CA  B $CB  C(attacker) $CC"
echo "tip $(tip): $(vals)"

rpcp() { case $1 in a) echo 26657;; b) echo 26157;; c) echo 26257;; esac; }
grpcp() { case $1 in a) echo 9090;; b) echo 9190;; c) echo 9290;; esac; }
mine() { # op duty
  local op=$1
  HOME=$S/$op nohup $S/bin/powminer --miner $(cat $S/m$op) --from miner --keyring-backend test --chain-id $CID \
    --rpc http://127.0.0.1:$(rpcp $op) --grpc localhost:$(grpcp $op) --auto-submit --loop --fees 0uaeth --duty $2 > $S/miner-$op.log 2>&1 &
}
register() { # op
  local op=$1
  local cmd; cmd=$($S/bin/validatorkeygen --miner $(cat $S/m$op) --home $S/$op/.aether --chain-id $CID --fees 200uaeth | grep '^aetherd tx pow register')
  cmd=${cmd#aetherd }
  HOME=$S/$op $B ${cmd} --node tcp://127.0.0.1:$(rpcp $op) --gas 400000 -o json 2>&1 | python3 -c 'import json,sys
try: r=json.loads(sys.stdin.read()); print("register code", r.get("code"), r.get("raw_log","")[:120])
except Exception as e: print("register: not json")'
}
bal() { HOME=$S/a $B q bank balances $1 --node tcp://localhost:26657 -o json 2>/dev/null | python3 -c 'import json,sys;b=json.load(sys.stdin)["balances"];print(next((x["amount"] for x in b if x["denom"]=="uaeth"),"0"))'; }

# Honest operators: mine lightly, register once they have a reward.
mine a 0.25; mine b 0.25
for op in a b; do
  for i in $(seq 1 120); do [ "$(bal $(cat $S/m$op))" -gt 1000 ] && break; sleep 2; done
  echo "$op balance $(bal $(cat $S/m$op))"; register $op
done
sleep 30
echo "tip $(tip) after honest registration: $(vals)"

# Watch from node A from here on.
nohup $S/bin/minerwatch --grpc localhost:9090 --address $MA --interval 2s --concentration-above 0.3334 --new-validators > $S/minerwatch.jsonl 2> $S/minerwatch.err &
sleep 6

# Attacker: open the miner account (A sends 1 AETH), register C's key, mine flat out.
HOME=$S/a $B tx bank send miner $MC 1000000uaeth --chain-id $CID --keyring-backend test --home $S/a/.aether --node tcp://localhost:26657 --fees 200uaeth --gas 400000 -y -o json >/dev/null 2>&1
sleep 6; echo "attacker account opened: $(bal $MC) uaeth"
register c
mine c 1
echo "attacker mining from tip $(tip)"
for i in $(seq 1 26); do sleep 20; echo "tip $(tip): $(vals)"; done
echo "--- minerwatch events"
cat $S/minerwatch.jsonl | python3 -c '
import json,sys
for l in sys.stdin:
    try: e=json.loads(l)
    except: continue
    print(e.get("height"), e.get("event") or e.get("type"), (e.get("message") or "")[:140])'
echo "The devnet keeps running; stop it with: pkill -f \"$S/bin/\""
