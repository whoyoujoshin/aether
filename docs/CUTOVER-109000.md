# Cutover at block 109,000

What changes at 109,000: x/authz and x/feegrant go live (agents can spend
under a human's on-chain grant, fees can be sponsored), governance quorum
counts active validators, and every node runs CometBFT 0.38.26.

Nodes: seed, sync3, sync4, peer-1 (the four validators), plus any full
node the explorer, faucet or other services query.

Commands below assume each node's usual home directory; add `--home <dir>`
to `aetherd` commands wherever your nodes use a custom one.

## The one rule

**Every node must be running the new binary before block 109,000.**
Swapping early is safe: it behaves exactly like the old binary until
then. A node still on the old binary at 109,000 splits from the others,
and a node switched to the new binary only after 108,999 is committed
can't start. With four equal validators, two missing the swap stops the
chain.

## 1. Before (any time before 109,000; the sooner the better)

Check the tip first, and don't start if it's close to 109,000:

```bash
curl -s localhost:26657/status | jq -r .result.sync_info.latest_block_height
```

Build once from `main` (after PR #25 is merged), on a machine with Go 1.25:

```bash
git fetch origin && git checkout origin/main
go build -o aetherd ./cmd/aetherd
sha256sum aetherd        # note it: every node should get the same file
```

On each node, one at a time (wait for each to be back in sync before the
next, so at least three validators are always signing):

```bash
sudo systemctl stop aetherd
sudo cp ~/.aether ~/.aether-backup-pre109000 -a   # optional but cheap insurance; use your real home dir
sudo install -m 0755 aetherd /usr/local/bin/aetherd   # wherever the unit's ExecStart points
sudo systemctl start aetherd
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
```

`catching_up` should go back to `false` within a minute. Confirm the new
binary is the one running (`sha256sum $(which aetherd)` matches).

## 2. At 109,000

Every node stops on its own at 109,000. This is expected:

```
CONSENSUS FAILURE!!! ... x/authz and x/feegrant activate at height 109000: restart this node ...
```

On each node, once it shows that (order doesn't matter, but do all four):

```bash
sudo systemctl restart aetherd
```

The chain resumes once three of the four validators are back.

## 3. After

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'   # moving past 109,000
aetherd query bank balances <any address>     # queries work
```

Then restart one node once more and confirm it comes back (this is the
case the fix in PR #25 is for). Tell Claude the height and anything
unusual in `journalctl -u aetherd -n 100`.

## If something goes wrong

- **A node won't start after the swap, before 109,000:** put the old
  binary back and start it; nothing has changed on chain yet. Report the
  log.
- **The chain doesn't resume after 109,000:** check that at least three
  validators were restarted and are on the new binary (`sha256sum`).
- **A node was missed and reached 109,000 on the old binary:** stop it,
  restore `~/.aether-backup-pre109000` if its data went past 108,999 on
  the old binary, install the new binary, start it, and let it catch up.
