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

## Testing this without a live relayer

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

## Activating this for real

The height-gated cutover (see above) is done and verified live. What's
still outstanding: point a relayer (e.g. Hermes) at the chain and open
a real client/connection/channel -- nothing here has been run against
a live, independently-operated counterparty chain yet, only the
in-process `ibctesting` harness.
