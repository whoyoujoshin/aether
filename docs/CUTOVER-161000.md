# Cutover at block 161,000

**Done, 2026-09-30.** See [What happened](#what-happened) for how it went
and what to do differently next time.

What changes at 161,000:

- **`x/escrow` goes live**: payments held until the payee is paid, the payer
  is refunded, or the deadline decides ([ESCROW.md](ESCROW.md)). It adds a
  store, so every node halts at 161,000 and needs one restart there.
- **Helicase**: the block proposer may bring IBC packets in from the other
  chain without a relayer key on Aether ([HELICASE.md](HELICASE.md)).
- **Ligase**: a transfer from another chain can open or settle an escrow
  from its memo, moving only IBC tokens, never AETH ([LIGASE.md](LIGASE.md)).
- **Consensus-key guard**: rotating a consensus key or re-announcing a
  key another validator uses no longer confuses the validator set
  (`ConsensusKeyGuardActivationHeight` in `x/pow/types.go`).

Nodes: seed, sync3, sync4, peer-1 (the four validators), plus any full
node the explorer, faucet or other services query.

Commands below assume each node's usual home directory; add `--home <dir>`
to `aetherd` commands wherever your nodes use a custom one.

## The one rule

**Every node must be running the new binary before block 161,000.**
Swapping early is safe: it behaves exactly like the old binary until
then. A node still on the old binary at 161,000 splits from the others,
and a node switched to the new binary only after 160,999 is committed
can't start. With four equal validators, two missing the swap stops the
chain.

At ~5 s blocks, 600 blocks is under an hour: swap first, then do anything
else.

## 1. Before (now; stop if the tip is within ~50 blocks of 161,000)

```bash
curl -s localhost:26657/status | jq -r .result.sync_info.latest_block_height
```

Build once from `main` (after the cutover PR is merged), on a machine with
Go 1.25:

```bash
git fetch origin && git checkout origin/main
grep -n "161_000" app/escrow.go app/helicase.go app/ligase.go x/pow/types.go   # all four
go build -o aetherd ./cmd/aetherd
sha256sum aetherd        # note it: every node should get the same file
```

On each node, one at a time (wait for each to be back in sync before the
next, so at least three validators are always signing):

```bash
sudo systemctl stop aetherd
sudo cp ~/.aether ~/.aether-backup-pre161000 -a   # optional but cheap insurance; use your real home dir
sudo install -m 0755 aetherd /usr/local/bin/aetherd   # wherever the unit's ExecStart points
sudo systemctl start aetherd
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
```

`catching_up` should go back to `false` within a minute. Confirm the new
binary is the one running (`sha256sum $(which aetherd)` matches).

## 2. At 161,000

Every node stops on its own at 161,000. This is expected:

```
CONSENSUS FAILURE!!! ... x/escrow activates at height 161000: restart this node ...
```

On each node, once it shows that (order doesn't matter, but do all four):

```bash
sudo systemctl restart aetherd
```

The chain resumes once three of the four validators are back.

## 3. After

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'   # moving past 161,000
aetherd query escrow list <any address>          # the module answers (an empty list is fine)
aetherd query auth module-account escrow        # created at 161,000
```

Then restart one node once more and confirm it comes back. Tell Claude the
height and anything unusual in `journalctl -u aetherd -n 100`.

Helicase does nothing until a node runs its worker
(`--helicase.counterparty-rpc` and friends) and the counterparty channel
exists; Ligase does nothing until someone sends a transfer to its address.
Neither needs any action at the cutover.

## If something goes wrong

- **A node won't start after the swap, before 161,000:** put the old
  binary back and start it; nothing has changed on chain yet. Report the
  log.
- **The chain doesn't resume after 161,000:** check that at least three
  validators were restarted and are on the new binary (`sha256sum`).
- **A node was missed and reached 161,000 on the old binary:** stop it,
  restore `~/.aether-backup-pre161000` if its data went past 160,999 on
  the old binary, install the new binary, start it, and let it catch up.
- **Not enough time:** if the swap can't reach at least three validators
  well before 161,000, don't cut over with a partial fleet. Tell Claude,
  and the heights move to a later block in a new PR.

## What happened

The chain passed 161,000 and runs on the new binary. After the cutover,
sync3 and sync4 had the same app hash at 161,006 (seed matched once it
caught up), block 161,003 was signed by all three active validators, and the escrow module
account exists (`aether14pphss726thpwws3yc458hggufynm9x7hnt4kw`, account 40).

- **Seed missed the swap** (its build was still running at 161,000). It
  ran 161,000 and a few blocks after on the old binary, so the new binary
  refused its data ("escrow ... expected 161005 got 0").
- **The chain stalled at 161,006** while seed was down. peer-1 (behind a
  home router) reached sync3 and sync4 only through seed, and without it
  the two left couldn't reach two-thirds. It moved again once seed came back.
- **Seed was recovered** by restoring its pre-161,000 data, keeping its
  current `priv_validator_state.json`, and starting the new binary: it
  caught up, halted once at 161,000 as planned, was restarted, and
  replayed forward.
- **Seed isn't a validator.** It has had voting power 0 since about
  122,400 (its `priv_validator_state.json` stopped there). The active set
  is peer-1, sync3 and sync4, 1,000,000 each.

## Lessons for the next cutover

- **Three equal validators have no spare.** More than two-thirds of the
  power must sign, and two of three is exactly two-thirds, so all three
  must be up for every block. Don't restart a validator unless the other
  two are healthy, and get a fourth into the active set.
- **No validator's only network path may run through one node.** Give
  every validator a direct peer to at least one other validator, and make
  sure it can actually connect (a node behind a home router dials out; the
  others need an open inbound P2P port). Check `net_info` before the swap.
- **`aetherd rollback` undoes exactly one height.** Running it again does
  nothing more. A node that ran more than one block on the old binary
  needs its pre-cutover backup instead.
- **Restoring a backup: keep the current `priv_validator_state.json`.**
  Copy it aside first and put it back over the backup's copy, so the node
  never signs a height it already signed.
- **Swap every node, the seed included, well before the height**, and
  check the tip with two readings a minute apart to know the block rate.
  At ~6 s blocks, 500 blocks is under an hour.
- **The halt check message and restart worked as designed** on every node
  that had swapped in time.
