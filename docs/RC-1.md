# `aether-rc-1`: run a validator for Aether's launch rehearsal

**Status: applications open.** Joshua set the dates and the GitHub form on
2026-11-05; the rest is settled in `docs/MAINNET.md` §3 and §8.

## The invitation

Aether is a proof-of-work Cosmos SDK chain with post-quantum (ML-DSA)
signatures. Before its mainnet (`aether-1`) it runs a full rehearsal,
`aether-rc-1`: the mainnet build, a genesis with outside operators, a
week of blocks, an upgrade drill, and then the chain is thrown away.
Nothing on it has value, and nothing carries over to mainnet.

**We're looking for operators.** An operator runs a validator node and a
miner together: on Aether, seats aren't handed out or staked for. A
validator holds voting power only while its own miner proves work, in
proportion to its share over the last three epochs (`docs/SEAT-RULE.md`).

**What you'd do:**

- build `aetherd` from the release tag with the reproducible build script,
  and check your binary's sha256 matches everyone else's;
- send your node's public consensus key and your miner's address for the
  genesis file;
- start your node and your miner at genesis, then register your key from
  your first mining reward;
- stay signing and mining for a week;
- take part in two drills: a rolling binary swap, and a planned halt and
  restart at an agreed height.

**What you'd need** (the full list is in `docs/OPERATOR.md`):

- a server for the validator (4 vCPUs, 8 GB RAM, 80 GB disk) and a
  smaller one for a sentry node in front (2 vCPUs, 4 GB), about $70 a
  month together at common cloud prices, so about $50 for the three
  weeks;
- spare CPU cores for the miner;
- about an hour on genesis day, and being reachable for the drills.

**Who qualifies** (the operator definition, `docs/MAINNET.md` §3):

- **independent:** no other operator controls, pays for or can reach your
  validator or miner (no shared people, keys, cloud account or network);
- **spread out:** no two operators in one data center, and no more than a
  third of the power on one cloud provider;
- **mines its own seat:** at least 1% of the window's work;
- **runs it properly:** a server, not a home PC, with a sentry in front
  and the consensus key in one place only;
- **reachable** for the drills.

**What you get:** credit by name or handle on the launch page and in the
mainnet release notes. Anything more will be decided later and announced;
nothing else is promised. Running `aether-rc-1` is also what makes an
operator eligible for `aether-1`: mainnet's operators must have done a
binary swap on it.

**Who else runs one:** Joshua and Gitty run one operator each, on servers
with their own provider and region, like everyone else's. The rehearsal
goes ahead with four operators in all, so two places are open.

**Apply:** open an "aether-rc-1 operator" issue on
`github.com/whoyoujoshin/aether` (the form asks only for public
information: a name or handle, a contact, the cloud provider and region).
Keys come later, and never a private one.

## Timeline

| When | What |
|---|---|
| now to Fri 2026-11-27 | applications; the testnet bug hunt runs to 11-20 |
| Fri 11-27 | operators chosen: four in all, Joshua's and Gitty's and two from the applications (mainnet's phase 2 needs seven) |
| by Mon 11-30 | the release tag; every operator builds it and posts the sha256 |
| by Mon 11-30 | operators send `aetherd comet show-validator` and their miner address |
| Tue 12-01 | the coordinator builds and posts `genesis.json` and its sha256 |
| **Wed 12-02, 16:00 UTC** | **genesis** |
| 12-02 to 12-09 | a week of running; the two drills in it |
| Wed 12-09 | the exit check; the chain is retired |

## Exit criteria

`aether-rc-1` passes, and phase 2 can be planned, when:

- every operator's binary had the same sha256 as everyone else's;
- every genesis validator signed from the first blocks;
- every operator registered its key and kept a seat through the week from
  its own miner's work;
- the chain didn't stop except at the planned halt;
- every operator did both drills.

A failure is a finding, not a disaster: fix it, and rehearse again with
`aether-rc-2`.

## Genesis-day checklist

### Operators

**A week before**

- [ ] Servers up: validator and sentry in different data centers.
- [ ] `bash scripts/rc/build-aetherd.sh` at the release tag; post the
      sha256; it matches everyone else's.
- [ ] `aetherd init <name> --chain-id aether-rc-1`; keep
      `priv_validator_key.json` on the validator only.
- [ ] Make the miner key (`aetherd keys add miner --keyring-backend file`)
      and back up its phrase offline.
- [ ] Send the coordinator `aetherd comet show-validator` and the miner
      address.

**The day before**

- [ ] Download `genesis.json`; `sha256sum` matches the posted hash; copy it
      into `config/`.
- [ ] `config.toml`: `timeout_commit = "5s"`; the sentry setup (validator
      `pex = false`, `persistent_peers` = its sentry; the sentry lists the
      validator in `private_peer_ids` and the other operators' sentries in
      `persistent_peers`); `[mempool] size = 20000`.
- [ ] `app.toml`: `minimum-gas-prices = "0.0001uaeth"`, `[mempool]
      max-txs = 20000`.
- [ ] Alerts: `minerwatch --address <miner>` and `chainwatch` posting to
      your webhook.

**Genesis**

- [ ] Start the sentry, then the validator, before 16:00 UTC: they wait for
      the genesis time.
- [ ] Blocks start once more than two thirds of the power is online: check
      your validator signs (`aetherd q block` or the explorer).
- [ ] Start the miner at once (`powminer ... --auto-submit --loop`): power
      moves toward the operators who mine.
- [ ] After the first accepted find (about 4.25 AETH), register the
      consensus key: `validatorkeygen --miner <address> --chain-id
      aether-rc-1 --fees 200uaeth --keyring-backend file`, then the
      `register-validator-pubkey` command it prints, with `--gas 400000`.
- [ ] Post "signing, mining, registered" in the operators' channel.

### Coordinator

- [ ] Collect `operators.json` and check it: one entry per operator,
      providers and regions spread out.
- [ ] `go run ./cmd/rcgenesis ... --chain-id aether-rc-1 --genesis-time
      2026-12-02T16:00:00Z --out genesis.json`; post the file and the
      sha256 it prints.
- [ ] At genesis: count signatures on the first blocks (every validator
      should sign), and watch the first epoch boundary (block 1,439): every
      operator's miner should be seated.
- [ ] Run `minerwatch` with `--new-validators` and `chainwatch` against a
      node of your own, to the operators' channel.
- [ ] Start the opener (`faucet --opener`) once the founders have mined,
      and check a brand-new account can open and mine (`docs/MAINNET.md`).

### The drills (during the week)

1. **Rolling swap.** The coordinator tags a release with no consensus
   change. Each operator in turn, only while the others sign: build it,
   compare the sha256, stop the node, swap the binary, start it, and
   confirm it signs before the next one goes.
2. **Planned halt and restart.** The coordinator names a height a day
   ahead. Each operator sets `halt-height = <H>` in `app.toml` and restarts
   once to pick it up, the same way. Every node stops at H. Each operator
   then clears it (`halt-height = 0`) and starts again; blocks resume once
   more than two thirds of the power is back. This is how an upgrade that
   adds a store activates.

Try a remote signer (tmkms or horcrux) on `aether-rc-1` if you plan to use
one on mainnet: neither has been tried on Aether yet.

## Decisions for Joshua

- ~~The dates~~ (decided 2026-11-05: the timeline above).
- ~~Where applications go~~ (decided 2026-11-05: the public GitHub issue
  form).
- ~~Whether Joshua and Gitty run operators~~ (decided 2026-11-06: one
  each, each on its own provider and region; on a server, not a home PC,
  as the operator definition asks).
- ~~The smallest number to go ahead with~~ (decided 2026-11-06: four in
  all; mainnet's phase 2 still needs seven).
- ~~What operators get~~ (decided 2026-11-06: credit only for now; more is
  to be decided later).
