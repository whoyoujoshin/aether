# IBC inflow cap for mainnet

**Status:** design only. No code yet. For Cosmos SDK v0.50 with ibc-go v8.8.
**Related:** `docs/MAINNET.md` §2 (one USDC path, an inflow cap).

## Why

A takeover of Aether, or a bug in it, can lose at most what the
counterparties hold in escrow for Aether's channels. For a token that
comes from the other chain (USDC from Injective), that escrow is the
voucher supply on Aether: every voucher minted on Aether locked the same
amount on Injective, and every voucher sent back burned one and unlocked
it. A forged header on Aether's client of Injective can mint vouchers
with nothing behind them, but only through the same receive path.

So the cap is on **stock, not flow**: refuse a received packet that would
take a voucher's total supply on Aether over a fixed amount. The refused
packet gets an error acknowledgement and Injective refunds the sender.
That bounds the counterparty's escrow, so it bounds what a takeover could
release, and it bounds what a forged header could mint.

## Why not the ibc-apps rate limiter on its own

`cosmos/ibc-apps` `modules/rate-limiting` (Stride's module) supports
ibc-go v8, but it can't do this job at launch:

- **Its quotas are percentages, not amounts.** `MaxPercentRecv` and
  `MaxPercentSend` are shares of the denom's total supply on Aether, read
  at the start of each window, and a limit can't be created while that
  supply is 0. USDC on a fresh `aether-1` starts at 0, so "10,000 USDC"
  can't be expressed with it.
- **A path with no limit is open.** `CheckRateLimitAndUpdateFlow` returns
  `false, nil` when no limit exists for the denom and channel, and the
  packet passes. Closing every other path needs separate wiring.
- **It limits flow per window.** That bounds how fast escrow grows, not
  how large it gets.

It is a good second layer once there is supply to take a percentage of
(see Later). Osmosis's limiter would need wasmd on Aether, and moving
ibc-go for its own rate-limiting app is a much larger change; neither is
worth it for this.

## Proposal: an Aether inflow-cap middleware

A small IBC middleware, outermost on the transfer route in `app/app.go`:

```go
ibcRouter.AddRoute(ibctransfertypes.ModuleName,
    inflowcap.NewIBCModule(
        ligase.NewIBCModule(transfer.NewIBCModule(app.TransferKeeper), ligaseHost{app}),
        app.InflowCapKeeper, app.BankKeeper))
```

Outermost, so it sees every received transfer before Ligase acts on a
memo, and an escrow funded through Ligase counts like any other transfer.

### On receive

1. Decode the ICS-20 packet data. If it doesn't decode, pass it on
   (transfer refuses it itself).
2. **Tokens coming home** (`ReceiverChainIsSource`: AETH, or anything
   that left Aether and is returning) pass. They unlock Aether's own
   escrow and mint nothing.
3. **A voucher** (a token from the other side): compute its `ibc/...`
   denom from the destination port and channel, as
   `ligase.receivedCoin` does.
   - If (channel, denom) isn't in the allowlist, return an error
     acknowledgement: **fail closed**.
   - If `bank.GetSupply(denom) + amount > cap`, return an error
     acknowledgement naming the cap.
   - Otherwise pass the packet on.

The supply is read when the packet arrives, so there is no counter to
keep, no window to reset and nothing to undo: every case below follows
from the supply itself.

### Parameters

A list of entries `{channel, denom, cap}`, for example the Injective
channel, `ibc/<hash of transfer/<channel>/<Injective USDC base denom>>`
and `10000000000` (10,000 USDC at 6 decimals). They are set in genesis
and changed by an `x/governance` param-change proposal: governance
executes any routed Msg, so the module needs only a `MsgUpdateParams`
whose authority is the governance module. An empty list closes every
inbound voucher path. A query returns each entry with its current supply,
so wallets and agents can check the room left before sending.

The mainnet USDC base denom is Injective mainnet's, pinned in `wallet` the
way `wallet.TestnetUSDC` is. The testnet one is
`erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d`; mainnet's differs.

### Each case

| Event | Effect |
|---|---|
| Received, under the cap | Voucher minted; supply rises. |
| Received, over the cap or not allowlisted | Error ack; nothing minted. Injective refunds the sender once the ack is relayed. |
| Voucher sent back to Injective | Burned on Aether; supply falls, so room frees up. |
| That send times out or gets an error ack | Transfer re-mints the refund; supply is back where it was. |
| Packet stuck (relayer down, client expired) | Nothing minted, supply unchanged; the funds wait on Injective until the packet times out. |
| A Ligase escrow funded by a transfer | Counted on the way in like any voucher; moving vouchers into escrow doesn't change supply. |

### Relayers

- **Helicase** (on the validators) submits `MsgRecvPacket` onto Aether
  like any relayer; the middleware runs inside it. No change.
- **`cmd/outbound`** relays Aether's acknowledgements to the other chain.
  A refused packet is refunded only once its error ack is relayed, so
  outbound must be running. No change either.

### Activation

It is consensus code: it changes which packets succeed. On a fresh chain
(`gates.Fresh`) it is on from block 1, with the entries in genesis. On the
testnet it sits behind its own height like every other gate, and is tried
first with a low cap on the Injective channel (`channel-2`).

## Tests before it ships

- **Unit:** a voucher under, at and over the cap; a denom not in the
  allowlist; AETH coming home over the same channel; a multi-hop denom (a
  different `ibc/` hash) refused; a cap of 0.
- **App (`app/`, with the `counterparty` test chain):** a refused
  transfer's error ack refunds the sender; sending vouchers back frees
  room; a timed-out send restores the supply; a Ligase escrow memo over
  the cap is refused as a whole.
- **Devnet:** the round trip with Helicase and outbound, before a testnet
  height is set.

## Risks

- **Over the cap means refused, not queued.** The transfer comes back
  only after the ack is relayed. Clients should read the room left first.
- **Refunds depend on outbound.** If `cmd/outbound` is down, refused
  packets wait for it, or for their timeout.
- **A forged header can still mint up to the cap.** The cap bounds the
  loss; it doesn't prevent it. `ibcwatch` and a low starting cap stay
  required.
- **Governance raises the cap.** The first raise should wait until the
  seat hardening has run on the testnet (from 460,800) and the USDC path
  has run at its starting cap.

## Later

- **A flow limit as a second layer:** the ibc-apps rate limiter with a
  24-hour net-flow percentage, once the voucher supply is well above 0,
  to slow a drain inside the cap.
- **An outflow cap**, if AETH leaving Aether needs one.

## Not doing

- No wasmd on Aether for this.
- No ibc-go upgrade for this.
- No outflow cap at launch.
