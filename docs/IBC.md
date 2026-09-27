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

- **Self-consensus tracking** (`app/ibc_self_consensus.go`) answers "what
  did Aether's own consensus state look like at block H" -- needed when a
  counterparty chain opens a connection to Aether and Aether has to
  validate the counterparty's record of it. A small hand-rolled store
  records each block's header (the same thing `x/staking`'s
  `HistoricalInfo` would do), and `UnbondingTime` is derived from
  `x/pow`'s real `BondCooldown`/`TargetBlockTime` -- the window a PoW
  validator's escrow stays slashable for equivocation discovered after the
  fact is this chain's actual analog of a bonded chain's unbonding period,
  not an arbitrary constant.
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
small activation height. That run is the first real exercise of
`app/ibc_self_consensus.go`: on `ConnOpenAck`, Aether checked the
counterparty's client of Aether against its own recorded headers and
bond-cooldown unbonding period, and accepted it.

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

### The live chain's IBC unbonding period is 1h40m, not 72h

The live run reported a bond cooldown of 1h40m. The testnet still has
`x/pow`'s original 100-block placeholder `BondCooldown` (100 blocks x 60s
target). `BondCooldownProduction` (4,320 blocks, 72h) is only the default
for chains started from genesis after it landed; see `x/pow/types.go`
and the whitepaper. Two consequences:

- **Short-lived light clients.** A client of Aether on another chain
  gets a trusting period of about 67 minutes (2/3 of 1h40m, the usual
  relayer default). A standing connection would need a client update at
  least that often, or the client expires and needs a governance
  recovery on the other chain.
- **The economic-security gap `types.go` describes.** 1h40m is far
  shorter than the 48h evidence window, so a validator could equivocate
  and withdraw before the evidence stops being valid.

One governance proposal fixes both:
`aetherd tx pow draft-update-params params.json --bond-cooldown 4320`,
then submit and pass it. Do this before opening any IBC connection
that's meant to stay up.
