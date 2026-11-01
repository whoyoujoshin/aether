# Running an Aether validator

This guide takes an operator from nothing to a validator seat on a fresh
Aether chain: the `aether-rc-1` rehearsal first, then `aether-1`. It is
what to read before agreeing to run one, and what to follow on launch day.

Seats on Aether aren't handed out. A validator holds voting power only
while the miner account bound to it proves work: power is in proportion
to the difficulty its accepted submissions met over the last three
epochs, and a miner needs at least 1% of that work to qualify
(`docs/SEAT-RULE.md`). So an operator runs two things that belong
together: a **node** that signs blocks, and a **miner** that keeps its seat.

## What an operator is

The launch needs at least seven operators before mainnet's phase 2, none
above a third of the power (`docs/MAINNET.md` §3). An operator:

- **Is independent.** No other operator controls, pays for or can reach
  its validator or miner: no shared people, keys, cloud account or home
  network.
- **Is spread out.** No two operators in one data center, and no more than
  a third of the power on one cloud provider.
- **Mines its own seat,** from its own hash or a pool it runs: at least 1%
  of the window's work. On `aether-1` only native work counts toward seats
  for now (merged mining earns rewards but no seat; `docs/SEAT-RULE.md`).
- **Runs it properly:** a server, not a home PC (peer-1 on a home
  connection was voted out twice and halted the chain once); a sentry node
  in front; the consensus key in one place only.
- **Is reachable:** a contact who can swap a binary before an upgrade
  height, and who has done one on `aether-rc-1`.

## What you need

- **A server for the validator**, with 4 vCPUs, 8 GB of RAM and 80 GB of
  disk. The testnet's busiest node used about 2 GB with its services
  beside it; give aetherd room (`GOMEMLIMIT` below the box's memory).
- **A second, smaller server for the sentry** (2 vCPUs, 4 GB), in another
  data center if you can.
- **CPU for mining.** Native mining is scrypt; the desktop miner does
  about 3,300 hashes a second per core. Run it on the validator box only
  if the box has cores to spare: a miner starving the node of CPU makes it
  miss blocks.
- **Go is not needed to run anything,** only to build. Build with the
  script below on any machine and copy the binary over.

## 1. Build `aetherd` and compare it with everyone else's

Every operator builds the same commit and gets the same file, byte for
byte, so the hashes prove everyone runs the same code:

```bash
git clone https://github.com/whoyoujoshin/aether && cd aether
git checkout <the tag the launch names>
bash scripts/rc/build-aetherd.sh          # Linux; GOOS=windows for Windows
```

It prints the binary's sha256. Post it where the operators coordinate and
compare: every hash for the same commit and platform must match. The
script fetches the Go version `go.mod` names, strips the build machine's
paths, and refuses a checkout with uncommitted changes. Checked
2026-11-01: two clones at different paths, with separate build caches,
gave identical binaries for Linux and for Windows.

## 2. Make the node and the miner key

On the validator server:

```bash
aetherd init <your-name> --chain-id aether-rc-1
aetherd keys add miner --keyring-backend file      # the miner's ML-DSA-44 key
aetherd comet show-validator                       # the node's consensus public key
aetherd keys show miner -a --keyring-backend file  # the miner's address
```

- `config/priv_validator_key.json` is the **consensus key**: it signs
  blocks. Back it up encrypted and offline. It must only ever run in one
  place: two nodes signing with one key is double-signing, which bans the
  validator and burns its escrow. When you move a node, stop the old one
  before starting the new one.
- Write down the miner key's recovery phrase and keep it off the server.
  Services that sign unattended open a `file` keyring from a passphrase
  file (`docs/SERVICE-KEYS.md`).

**Send the coordinator three things, nothing else:** your name, the JSON
`aetherd comet show-validator` printed, and your miner's address. Never
send a private key or a recovery phrase.

## 3. The genesis file

The coordinator builds it with `cmd/rcgenesis` (see "For the coordinator")
and shares it with its sha256. Check the hash before you use it:

```bash
sha256sum genesis.json     # must match the hash the coordinator posted
cp genesis.json ~/.aether/config/genesis.json
```

It lists every operator's consensus key as a genesis validator, all with
the same power, and every operator's miner account **with a balance of
zero**. Nobody starts with AETH (the fair launch). Your miner account
exists from block 1, so it can mine straight away: a mining submission
needs no fee.

## 4. Configure the node

In `config/config.toml`:

- `timeout_commit = "5s"` on every validator. One node at 60 s once made
  every block wait for it.
- **Behind a sentry:** set `pex = false` and `persistent_peers =
  "<sentry-id>@<sentry-ip>:26656"`, and don't open the validator's P2P port
  to the world. On the sentry, set `pex = true`, list the validator in
  `persistent_peers` and in `private_peer_ids` (so the sentry never gossips
  its address), and add the other operators' sentries.

In `app.toml`, or with the flag:

- `minimum-gas-prices` is the price the launch announces (still to be
  decided; the rehearsals used `0.0005uaeth`), plus the USDC voucher once
  a USDC path opens. Every validator asks the same price. A lone mining
  submission passes at any price.
- `iavl-cache-size` can come down from 781,250 if memory is tight; the
  testnet seed runs 200,000.

**A remote signer** (tmkms, or horcrux for a threshold key) keeps the
consensus key off the validator: CometBFT's `priv_validator_laddr` points
at it. Aether's consensus keys are ordinary ed25519, so both should work,
but **neither has been tried on Aether yet**: try it on `aether-rc-1`
before relying on it.

## 5. Launch day

Start the node before the genesis time; it waits for it, and blocks start
once more than two thirds of the power is online. Then **start the miner
at once**:

```bash
powminer --miner <your miner address> --from miner --keyring-backend file \
  --chain-id aether-rc-1 --rpc http://127.0.0.1:26657 --grpc localhost:9090 \
  --auto-submit --loop --fees 0uaeth
```

A native submission needs no fee. `powminer` reads the keyring in
`~/.aether`; `--duty` below 1 makes it rest between rounds.

**Why at once:** your genesis seat starts at equal power, but the seat
rule moves power toward the miners who do the work, by up to 10 points of
the total each epoch (1,440 blocks, about 2.4 hours). A seat whose miner
does nothing loses its share to those who mine.

## 6. Register your consensus key to your miner

Your genesis seat starts out under a placeholder account derived from the
consensus key. Registering the key to your miner account takes the seat
over, so your miner's work counts for it. It costs one ordinary fee, which
your first reward (4.25 AETH) pays:

```bash
validatorkeygen --miner <your miner address> --home ~/.aether \
  --chain-id aether-rc-1 --fees 200uaeth --keyring-backend file
# then run the aetherd tx pow register-validator-pubkey command it prints,
# adding --gas 400000
```

Run it on the validator node: it reads the node's own
`priv_validator_key.json`, so the key registered is the one that signs.
Register a different key and your miner becomes a validator nothing signs
for. One consensus key, one miner account: never register the same key
under two.

Check where you stand at any time:

```bash
aetherd q pow seat-work <your miner address>   # window work, the 1% bar, whether you meet it
aetherd q pow active-validators
```

## 7. Staying in the set

- **Keep mining.** Power follows the last three epochs' work.
- **Keep signing.** A validator missing most of the liveness window is
  removed until the next boundary. Removals between boundaries are capped
  at 10% of the power, so a big validator going offline slows blocks
  instead; but a validator above a third of the power stops the chain
  whenever it's offline. Watch your share.
- **Alerts:** `minerwatch` (your miner's work, selection, removal,
  concentration) and `chainwatch` (blocks stopping) post to a webhook.

## 8. Upgrades

A consensus change ships with an activation height. Before it:

1. Build the release commit with `scripts/rc/build-aetherd.sh` and compare
   hashes with the other operators.
2. Swap the binary one node at a time, only while the others are signing:
   stop the node, replace the binary, start it, and check it signs again
   before the next operator goes.
3. Each upgrade's runbook names an abort line: if not enough of the power
   has swapped by then, everyone goes back to the old binary and the
   height moves.

A change that adds a store halts every node at its height; each restarts
once on the new binary, and blocks resume when more than two thirds of the
power is back (`docs/UPGRADE-INFLOWCAP-FEES.md` is a worked example).

## For the coordinator

1. Collect each operator's name, `aetherd comet show-validator` JSON and
   miner address into `operators.json`:

   ```json
   [
     {"name": "alice", "consensus_pubkey": {"@type": "/cosmos.crypto.ed25519.PubKey", "key": "o/ZeCt..."}, "miner": "aether1..."},
     {"name": "bob",   "consensus_pubkey": "VhtqpK...", "miner": "aether1..."}
   ]
   ```

2. Build the genesis:

   ```bash
   aetherd init coordinator --chain-id aether-rc-1 --home /tmp/base
   go run ./cmd/rcgenesis --base /tmp/base/config/genesis.json \
     --operators operators.json --chain-id aether-rc-1 \
     --genesis-time 2026-12-01T16:00:00Z --out genesis.json
   ```

   It refuses a chain ID that isn't fresh (`aether-1` or `aether-rc-*`), a
   key or miner listed twice, and a base file that gives anyone a balance.
   It writes validators and accounts in a fixed order, so the same input
   gives the same file, and prints its sha256.

3. Share `genesis.json` and its hash; every operator checks it.

4. Fund the opener (`faucet --opener`) once the founders have mined, so
   newcomers can open accounts (`docs/MAINNET.md`).

## Rehearsed (2026-11-01)

Two operators on an `aether-rc-ops` devnet, each with its own node and
miner key, both nodes asking 0.0005uaeth, genesis from `rcgenesis` (pow
params patched afterwards to difficulty 20,000 and 20-block epochs, for
speed):

- Both nodes held the same genesis (one hash), and both genesis
  validators signed from block 3.
- Operator A's miner account started with nothing; its first fee-free
  find paid it 4.25 AETH.
- A registered its consensus key from that reward (200 uaeth fee, 163,632
  gas). At the next boundaries A's miner replaced its genesis placeholder
  in the active set, and power moved from 50/50 to 70/30, the seat rule's
  10 points an epoch, toward the operator who mined. Operator B, which
  didn't mine, was fading: the reason to start the miner at once.
