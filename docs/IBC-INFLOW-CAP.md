# IBC inflow cap for mainnet

**Status:** built, not active on the testnet. The code is `x/inflowcap`
(module, keeper, `MsgUpdateParams`, queries and the middleware) and
`app/inflowcap.go` (wiring and activation). For Cosmos SDK v0.50 with
ibc-go v8.8. It is on from block 1 on fresh chains; on the testnet its
height is a placeholder (see Activation).
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
  supply is 0. USDC on a fresh `aether-1` starts at 0, so "5,000 USDC"
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
ibcRouter.AddRoute(ibctransfertypes.ModuleName, inflowcap.NewIBCModule(
    ligase.NewIBCModule(transfer.NewIBCModule(app.TransferKeeper), ligaseHost{app}),
    inflowCapHost{app}))
```

`inflowCapHost` reads the keeper and the bank supply when a packet
arrives, as `ligaseHost` does, since the module is wired only from its
activation height.

Outermost, so it sees every received transfer before Ligase acts on a
memo, and an escrow funded through Ligase counts like any other transfer.

### On receive

1. Decode the ICS-20 packet data. If it doesn't decode, pass it on
   (transfer refuses it itself). An amount that isn't a positive integer
   is refused here.
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

A list of entries `{channel_id, denom, cap}` (`aether.inflowcap.v1.Params`).
`denom` is the token as the other chain sends it, so an entry can't name
a voucher of a different channel: the voucher is computed from the two,
`ibc/<hash of transfer/<channel_id>/<denom>>`. For example `channel-2`,
Injective's USDC base denom and `5000000000` (5,000 USDC at 6 decimals,
the launch cap decided on 2026-10-22). At most 100 entries, no voucher
twice. They are set in genesis
and changed by an `x/governance` param-change proposal: governance
executes any routed Msg, so the module needs only a `MsgUpdateParams`
whose authority is the governance module. An empty list closes every
inbound voucher path. The `Caps` query (`aether.inflowcap.v1.Query`)
returns each entry with its voucher denom, current supply and the room
left, so wallets and agents can check before sending; `Params` returns
the list as set.

A refused packet's acknowledgement carries only the error's code
(`inflowcap` codes 3 not listed, 4 over the cap, 5 a bad amount). The
reason is in an `inflow_cap_refused` event with the channel, sender,
denom, voucher, amount, supply, cap and `reason` (`not_listed`,
`over_cap`, `bad_amount`). ibc-go keeps an error acknowledgement's events
but prefixes the event and every key with `ibccallbackerror-`, so search
for `ibccallbackerror-inflow_cap_refused.ibccallbackerror-reason`
(`inflowcap.RefusedEvent`, `inflowcap.RefusedKey`).

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

It is consensus code: it changes which packets succeed, and it adds a
store, so it uses the restart-driven activation x/escrow does: a node
that reaches the height without restarting halts there and resumes from
that block once restarted.

- **Fresh chains** (`gates.Fresh`): on from block 1, with the entries in
  genesis. A genesis without an `inflowcap` section starts from the
  default, which lists nothing, so the mainnet genesis must list the
  Injective USDC entry.
- **The testnet:** `InflowCapActivationHeight` is a placeholder
  (30,000,000), chosen after the devnet run below. No genesis sets a list
  there, so the activation block sets `testnetInflowEntries`: USDC from
  Injective over `channel-2` capped at 100 USDC, and OSMO from Osmosis
  over `channel-3` capped at 1,000 OSMO. Every other voucher (INJ, the
  dead `channel-1`) is refused from then on. Both caps and the list are
  set with the height, and governance changes them afterwards.

## Tests

- **Unit (`x/inflowcap`), done:** a voucher under, at and over the cap; a
  cap of 0; a denom not listed, over another channel, or routed through
  a third chain (a different `ibc/` hash); an empty list; AETH and
  Aether's own vouchers coming home; bad amounts; inactive; params
  validation.
- **App (`app/inflowcap_test.go`, two real apps over ibctesting), done:**
  a refused transfer's error ack refunds the sender; sending vouchers
  back frees room; a timed-out send restores the supply; a Ligase escrow
  memo over the cap is refused as a whole; AETH coming home with nothing
  listed; `MsgUpdateParams` only from the governance account and only
  valid; the restarts across the activation and the testnet entries.
  `app/fresh_chain_test.go` checks it is wired from block 1 with nothing
  listed.
- **Devnet, done (2026-10-26):** an `aether-rc-dev` node (the cap on from
  block 1, genesis listing the counterparty's `stake` over `channel-0` at
  100,000) and a `counterpartyd` node, with Helicase relaying onto Aether
  and `cmd/outbound` onto the counterparty, nothing relayed by hand
  (`cmd/helicasetest --outbound-key outbound --ligase --inflowcap
  --inflowcap-unlisted-denom ufoo`). After the usual in, out and Ligase
  steps the supply was 12,001, room 87,999:
  - 88,000 (one over) was refused with reason `over_cap`, and the
    counterparty refunded it once outbound relayed the error ack;
  - 87,999 filled the cap exactly (room 0);
  - an Aether user sent 1,000 back, the room became 1,000, and 1,000 came
    in again;
  - 100 `ufoo`, not listed, was refused with `not_listed` and refunded.

  The first attempt found a Helicase bug, not a cap one: with 1 s blocks
  the proposer put the same relay transaction in two blocks, the second
  copy's empty result replaced the first's in CometBFT's index, and
  outbound could never find Aether's acknowledgement. Fixed in the same
  change (see `docs/HELICASE.md`).

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

## Still to do

- The testnet height, with `testnetInflowEntries` checked against what
  is on Aether then.
- Injective mainnet's USDC denom pinned in `wallet` and listed in the
  `aether-1` genesis.
- Clients: read `Caps` before a transfer onto Aether (agentmcp and the
  published clients), and name a refusal's reason from its event.

## Later

- **A flow limit as a second layer:** the ibc-apps rate limiter with a
  24-hour net-flow percentage, once the voucher supply is well above 0,
  to slow a drain inside the cap.
- **An outflow cap**, if AETH leaving Aether needs one.

## Not doing

- No wasmd on Aether for this.
- No ibc-go upgrade for this.
- No outflow cap at launch.
