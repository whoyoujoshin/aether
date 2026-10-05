# IBC

Core IBC, ICS-20 (fungible token transfer), and ICS-27 (interchain
accounts) are built, wired into `app/app.go`, and covered by real
integration tests (`app/ibc_handshake_test.go`, `app/ibc_ica_test.go`).
See `app/ibc.go`'s `IBCActivationHeight` doc comment for why activation
is height-gated: like `x/authz`/`x/feegrant` before it
(`app/authz_feegrant.go`), adding new stores to an already-running chain
needs a coordinated, height-gated cutover, not a plain code deploy.
`IBCActivationHeight` was **122,000**, coordinated with the operator on
2026-09-27 with the live tip at 121,807 -- the same coordinated cutover
as `AccountAuthActivationHeight` (see `docs/ACCOUNT_ABSTRACTION.md`),
the same way `AuthzFeegrantActivationHeight` was finalized at 109,000.
**Live as of 2026-09-27**: all four testnet nodes (seed, sync3, sync4,
peer-1) restarted at 121,999 and passed the gate cleanly, and
`ibc client params` on the live node returns real params instead of
the pre-genesis "not set in store" error, confirming
`initIBCGenesisAtActivation` ran correctly, not just that consensus
didn't halt.

## What's wired

- **Capability** (`github.com/cosmos/ibc-go/modules/capability`) -- IBC's
  own object-capability system, a separate module since ibc-go v8.
- **Core IBC** (02-client/03-connection/04-channel/23-commitment), plus the
  **07-tendermint** light client explicitly registered alongside it
  (`ibctm.AppModuleBasic{}`) -- core IBC's own `RegisterInterfaces` does
  *not* register the Tendermint client's `ClientState`/`ConsensusState`
  types; skipping this makes every `MsgCreateClient` fail to decode. Caught
  by the handshake test, not by inspection.
- **ICS-20 transfer**, standard wiring, no fee middleware.
- **ICS-27 interchain accounts**, both controller and host. The controller
  has **no custom authentication module**
  (`icacontroller.NewIBCMiddleware(nil, keeper)`): every callback on that
  middleware checks `im.app != nil` before delegating, so any account can
  register and drive its own interchain account directly through the
  controller's own `Msg` service (`MsgRegisterInterchainAccount`,
  `MsgSendTx`) -- no bespoke on-chain integration needed on Aether's side
  for an agent to control an account on another chain, or vice versa.

## Two things this chain doesn't have that IBC usually assumes

Aether has no `x/staking` and no `x/upgrade`. ibc-go's core keeper wants
stand-ins for both:

- **Self-consensus tracking** (`app/ibc_self_consensus.go`) satisfies
  the staking-keeper interface ibc-go's keeper constructor requires: a
  small hand-rolled store records each block's header (what `x/staking`'s
  `HistoricalInfo` would do), and `UnbondingTime` is derived from
  `x/pow`'s `BondCooldown`/`TargetBlockTime`. **In ibc-go v8.8.0 nothing
  calls it**: the connection handshake only verifies connection state
  and no longer runs `ValidateSelfClient`/`GetSelfConsensusState`. Verified
  in the ibc-go source and by `TestIBCHandshakeAndTransfer`, whose client
  of Aether uses ibctesting's 21-day default and still opens. So Aether
  never checks or advertises an unbonding period; a client of Aether gets
  whatever the relayer that creates it chooses. The header writes are
  harmless but unused; removing them would change the AppHash, so it
  would need its own coordinated cutover.
- **No IBC software upgrades** (`app/ibc_upgrade_shim.go`): Aether's own
  height-gated activation pattern replaces `x/upgrade`-driven binary
  swaps, so IBC's `MsgIBCSoftwareUpgrade` route is never wired in. The
  stand-in keeper returns clear errors rather than silently no-oping.

## In-process tests (ibctesting)

`app/ibc_handshake_test.go` and `app/ibc_ica_test.go` build
`ibctesting.TestChain` by hand instead of using ibc-go's own
`ibctesting.NewTestChain`/`SetupWithGenesisValSet`: those hardcode an
`x/staking`-shaped genesis (delegations, a bonded pool, secp256k1 sender
keys) this chain doesn't have. `newIBCTestChain` (in
`ibc_handshake_test.go`) instead bootstraps a real `*App` the same way a
live node's `genesis.json` does -- one CometBFT validator via
`InitChainer -> PowKeeper.BootstrapValidator`, real ML-DSA sender keys,
since `PostQuantumDecorator` rejects anything else -- then hands it to
`ibctesting`'s normal `Coordinator`/`Path` machinery, which works
unmodified once the chain is built.

## Real relayer test: Aether <-> a separate chain over RPC

Off-the-shelf relayers can't drive Aether: both Hermes and
`cosmos/relayer` hardcode secp256k1 signing with no extension point,
and `PostQuantumDecorator` rejects every Aether tx that isn't ML-DSA-44
signed. So the repo carries its own:

- **`counterparty/` + `cmd/counterpartyd`** -- an ordinary Cosmos SDK
  chain (real `x/staking`, secp256k1 keys, no post-quantum ante
  decorator) whose only job is to be a genuinely separate IBC
  counterparty process.
- **`relayer/` + `cmd/relayer`** -- a minimal, purpose-built relayer, not
  a general one. It talks to each chain over real RPC/gRPC, signs Aether
  txs with ML-DSA-44 and counterparty txs with secp256k1, and uses
  ibc-go's own client/connection/channel query and proof helpers.

Since then, [Helicase](HELICASE.md) removes the need for a relayer to
sign anything on Aether after the handshake: the block proposer relays
packets, acknowledgements and timeouts in itself, unsigned and checked
by the light client. `cmd/relayer` still opens paths, and relays onto the
other chain.

One run of `cmd/relayer` does the whole path: a 07-tendermint client on
each side (trusting/unbonding periods read from each chain's live params
-- `x/pow` bond cooldown for Aether, `x/staking` unbonding time for the
counterparty), the connection handshake, an unordered `ics20-1` channel,
then 12345 uaeth Aether -> counterparty and the voucher back, checking
the escrow, the voucher (trace `transfer/channel-0/uaeth`), and that the
escrow returns to exactly 0.

**Verified 2026-09-27** against a local Aether devnet and a local
counterparty devnet, each its own process. The Aether side went through
the real height-gated activation halt and restart first, just with a
small activation height. (An earlier version of this note said the run
exercised `app/ibc_self_consensus.go` on `ConnOpenAck`. It didn't: see
the correction above.)

To reproduce locally: build `aetherd` with `ibcActivationHeight` in
`app/ibc.go` set to a small value (local builds only, never commit it),
start a single-validator devnet per `docs/DEVNET.md` with a funded
`relayer` key, restart it when it halts at the activation height, start
a `counterpartyd` devnet on non-default ports with a funded `relayer`
key, then run `cmd/relayer` pointed at both (see its flags).

**Live-verified on `aether-testnet-1`, 2026-09-27.** Gitty ran the same
`cmd/relayer` (built at `b87ecd1`) on sync3 against a throwaway
`counterpartyd` on the same box, signing Aether's side with an ML-DSA
relayer key funded on the live chain. It ended `round trip complete`:
`07-tendermint-0`, `connection-0` and `transfer/channel-0` on Aether, all
OPEN; 12345 uaeth out and back with the escrow at 0; the same voucher
denom as the local run (`ibc/0406...3D27`); and packet 1's receipt
recorded. The node's own queries confirmed each of these afterwards.
Those three IDs stay on Aether, idle; the counterparty chain is gone.

Still not done: a connection to a counterparty chain someone else
operates. That's an operational step, not a code one.

### Resolved: the live chain's bond cooldown and IBC unbonding period

The live run reported an unbonding period of 1h40m. The testnet still has
`x/pow`'s original 100-block placeholder `BondCooldown`;
`BondCooldownProduction` (4,320 blocks) is only the default for chains
started from genesis after it landed. Three facts make this worse than
it looks:

- **The real lockup is shorter than reported.** The 1h40m came from
  `cmd/relayer` converting `BondCooldown` to time with the 60s *target*
  block time, but live blocks run at about 5s. So 100 blocks is really
  about 8 minutes, and the client that run created trusted Aether about
  12x too long. Fixed in the relayer: it now multiplies by the average
  interval of the last 1,000 real blocks. (Aether itself never checks
  this value; see "Two things this chain doesn't have" above.)
- **Evidence stays valid longer than `x/pow/types.go` assumes.** CometBFT
  expires evidence only when it is older than *both*
  `max_age_num_blocks` (100,000) and `max_age_duration` (48h); the
  comment in `types.go` says either. At ~5s blocks that window is
  about 5.8 days, so a validator can withdraw long before evidence
  against them expires.
- **4,320 blocks doesn't close it.** At ~5s blocks it's about 6 hours.

**Decided 2026-09-27: two governance proposals.** Gitty measured live
blocks at 5.014s over the last 500 (steady; 6.6s before the 122,000
cutover). Joshua approved:

- `x/consensus` evidence `max_age_num_blocks`: 100,000 -> **2,880**.
  Evidence then stays valid for exactly 48h at any block time up to
  60s, since the time bound always governs.
- `x/pow` `bond_cooldown`: 100 -> **51,840**, which is 72h (48h plus a
  day of margin) at ~5s blocks.

Both passed a local governance rehearsal. The cooldown is still counted
in blocks, so re-check it if block time ever drops (below ~3.4s it
falls under 48h).

**Live on `aether-testnet-1`, 2026-10-04.** Proposals #3 and #4 both
closed `PROPOSAL_STATUS_PASSED` (#3 at 15:58:07 CT, #4 ten seconds
later). Gitty read the values back from the seed's public RPC at block
202,043 (16:05 CT):

- evidence `max_age_num_blocks` = **2,880**, `max_age_duration` =
  172,800s (48h);
- `x/pow` `bond_cooldown` = **51,840**.

**Osmosis testnet connected, 2026-10-04.** The first lasting connection
opened right after the October upgrade activated:

- **Aether's end:** `connection-1`, `transfer/channel-1` over client `07-tendermint-1`.
- **Osmosis's end:** `connection-4605`, `transfer/channel-11841` over client `07-tendermint-5277`.
- **Relaying:** Helicase relays onto Aether, and `cmd/outbound` on the seed relays onto Osmosis.

IDs, trusting periods and the end-to-end check are in
[OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md).
