# IBC inflow cap for mainnet

**Status:** built, not active on the testnet. The code is `x/inflowcap`
(module, keeper, `MsgUpdateParams`, queries and the middleware) and
`app/inflowcap.go` (wiring and activation). For Cosmos SDK v0.50 with
ibc-go v8.8. It is on from block 1 on fresh chains; on the testnet its
height is 511,920 (see Activation).
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
   - If `bank.GetSupply(denom) + in flight + amount > cap`, return an
     error acknowledgement naming the cap. The comparison is `amount >
     room`, so no sum can overflow: an amount near 2^256 is refused
     `over_cap` like any other, with an acknowledgement, not a panic that
     leaves the sender waiting for the timeout.
   - Otherwise pass the packet on.

The supply is read when the packet arrives. The one thing kept is the
sends **in flight**: a voucher sent back where it came from is burned
when it leaves, but transfer re-mints it if the packet gets an error
acknowledgement or times out. Counting only the supply, that burn looks
like free room, and a fresh fill plus the refund went over the cap (red
team, PR #171: 2,000 on a cap of 1,000 with one timeout, 3,000 with two
error-ack cycles, and the same through a Ligase withdraw that times
out). So the transfer keeper sends through `inflowcap.ICS4Wrapper`,
which records each burned send by port, channel and sequence
(`InFlightSend`, with a running total per voucher), and the middleware
releases it once the packet settles: on a success acknowledgement the
burn is final, and on an error acknowledgement or a timeout the release
happens in the same step as transfer's refund. A refund is refused in
one case only, as a backstop: settling a recorded send may not leave
more of its voucher (supply plus in flight) than there was before. A
correct refund re-mints exactly what the send burned, so this stops only
a refund path that mints more, such as ibc-go's 2024 timeout reentrancy
(fixed in the ibc-go Aether runs; Aether has no CosmWasm): the message
fails with `ErrSettlementGrew`, nothing is refunded, and the packet waits
for a fixed binary. A send with no record (from before activation)
isn't checked. Sends that escrow (AETH, or a voucher forwarded over another
channel) don't move the supply and aren't recorded. Genesis exports and
imports the sends in flight.

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
returns each entry with its voucher denom, current supply, its sends in
flight and the room left (cap − supply − in flight), so wallets and agents can check before sending; `Params` returns
the list as set.

A refused packet's acknowledgement carries only the error's code
(`inflowcap` codes 3 not listed, 4 over the cap, 5 a bad amount). The
reason is in an `inflow_cap_refused` event with the channel, sender,
denom, voucher, amount, supply, in flight, cap and `reason` (`not_listed`,
`over_cap`, `bad_amount`). ibc-go keeps an error acknowledgement's events
but prefixes the event and every key with `ibccallbackerror-`, so search
for `ibccallbackerror-inflow_cap_refused.ibccallbackerror-reason`
(`inflowcap.RefusedEvent`, `inflowcap.RefusedKey`).

`aetherd q inflowcap caps` and `aetherd q inflowcap params` print them.

The mainnet USDC base denom is Injective mainnet's, pinned in `wallet` the
way `wallet.TestnetUSDC` is. The testnet one is
`erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d`; mainnet's differs.

### Each case

| Event | Effect |
|---|---|
| Received, under the cap | Voucher minted; supply rises. |
| Received, over the cap or not allowlisted | Error ack; nothing minted. Injective refunds the sender once the ack is relayed. |
| Voucher sent back to Injective | Burned on Aether; supply falls, but the send is in flight and still holds its room. |
| Injective acknowledges it | The burn is final; room frees up. |
| That send times out or gets an error ack | Transfer re-mints the refund and the send stops being in flight, in one step; supply plus in flight is where it was. |
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

- **Fresh chains** (`gates.Fresh`): on from block 1. A genesis without an
  `inflowcap` section starts from the default, which lists nothing. On
  `aether-1` that is what genesis should have: see Listing on aether-1.
- **The testnet:** `InflowCapActivationHeight` is 511,920 (chosen
  2026-10-28, about 16:40 UTC on 2026-10-29; runbook
  `docs/UPGRADE-INFLOWCAP-FEES.md`). No genesis sets a list
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
  back frees room once acknowledged; a timed-out send restores the
  supply; a send in flight holds its room against a fresh fill (error
  ack, timeout and Ligase withdraw, PR #171's red team); a Ligase escrow
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

- **Devnet, sends in flight (2026-10-27):** the same setup from `main`
  after PR #172, with `--outbound-pid` so `cmd/helicasetest` pauses
  `cmd/outbound` to keep a send back in flight. With the cap full
  (100,000):
  - an Aether user sent 1,000 back with a 20 s timeout: supply 99,000,
    in flight 1,000, room 0; 1,000 sent in meanwhile was refused
    `over_cap`; Helicase timed the send out (an unsigned relay
    transaction) and the refund brought supply back to 100,000, in
    flight 0;
  - 1,000 sent back to a receiver the counterparty can't decode: in
    flight 1,000; 1 sent in meanwhile was refused `over_cap`; once
    `cmd/outbound` resumed, the counterparty's error ack came back
    through Helicase and the refund brought supply back to 100,000;
  - both refused transfers were refunded on the counterparty;
  - a send back that succeeded freed its 1,000 of room only once its
    ack came back.

  Two runs, both clean; no panics or rejected proposals.

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
- **A send that never settles holds its room.** If a send back can't be
  acknowledged or timed out (the counterparty's client of Aether
  expired), its record stays and keeps its amount off the room. That
  matches what the other chain still holds in escrow for it; governance
  can raise the cap if it matters.
- **Sends from before activation aren't counted.** The wrapper records
  only from the activation height. A send back still in flight then, if
  it fails, is refunded on top of the cap once. At the testnet
  activation, check that no packets are outstanding on the listed
  channels first (`aetherd q ibc channel packet-commitments transfer
  channel-2`, and `channel-3`). Fresh chains are on from block 1.
- **A refund-path bug stops refunds.** The backstop above fails closed:
  if a settlement ever tried to mint more than its send burned, that
  packet's acknowledgement or timeout would fail until a fixed binary
  ships, and its sender would wait for the refund. The whole transaction
  fails with it, refund, record and commitment removal alike, so nothing
  is lost and the packet can be relayed again. Helicase sends each relay
  message as its own transaction, so only that packet's settlement fails;
  the client update and every other packet on the path still go through,
  and the failing one is proposed again every ~10 blocks. A relayer that
  bundles several messages in one transaction (Hermes, the Go relayer)
  has the others in that transaction fail with it and must retry them.
  A genesis in-flight record whose amount or voucher doesn't match its
  packet could trip the check falsely; genesis carries only exported
  records, and the testnet activation starts with none in flight.
- **A forged header can still mint up to the cap.** The cap bounds the
  loss; it doesn't prevent it. `ibcwatch` and a low starting cap stay
  required.
- **Governance raises the cap.** The first raise should wait until the
  seat hardening has run on the testnet (from 460,800) and the USDC path
  has run at its starting cap.

## Clients

- **Reading the room:** `get_inflow_caps` on agentmcp (local, the public
  `/mcp`, the hosted test wallet and A2A), `aether_get_inflow_caps` in the
  TypeScript and Python toolkits, `get_inflow_caps` in `aether-mcp`,
  `AETHER_GET_INFLOW_CAPS` in the ElizaOS plugin, and
  `wallet.Client.InflowCaps`, `AetherClient.inflowCaps()` and
  `AetherClient.inflow_caps()` in code. Each lists the tokens other chains
  may send onto Aether, with channel, cap, supply, sends in flight (`inFlight`) and room, and answers
  `active: false` on a chain without the cap yet (the node answers the
  query path "unknown").
- **Swaps:** `swap_aeth` and `swap_from_test_wallet` bring OSMO back onto
  Aether over `channel-3`. With the cap on, they refuse with
  `INFLOW_CAPPED` before sending anything if OSMO isn't listed or the
  quoted OSMO is over its room. Otherwise the return transfer would be
  refused, and the OSMO would wait in the swap contract as owed until
  someone re-sends it.
- **Shared vectors:** `clients/testdata/inflowcaps.json` holds the Caps
  answer as a devnet node sent it, one encoded by the Go types, and the
  node's answer without the module. The Go, TypeScript and Python tests all
  use it.

## Listing on aether-1

**Genesis lists nothing.** The Injective channel doesn't exist at
genesis, and IBC channels are permissionless and numbered first come,
first served: if genesis listed `channel-0` for Injective's USDC, whoever
opened `channel-0` first, to a chain of their own, could send a token
named `erc20:0xa00C…235a` and mint, up to the cap, vouchers with the
same `ibc/` hash wallets treat as USDC. Client IDs and chain IDs can be
claimed the same way, so nothing but a checked, open channel can anchor
an entry. That also fits `docs/MAINNET.md`'s phased launch with IBC
closed at first.

**Then, once the Injective path is open:**

1. Check it on both chains: Aether's client of Injective follows
   `injective-1` from a header taken from Injective itself, and
   Injective's end of the channel names Aether's.
2. Draft the listing. The command reads the live list, checks the
   channel is an open transfer channel whose client follows the chain ID
   you name, and prints the client and the other end for voters:

   ```bash
   aetherd tx inflowcap draft-update-params usdc.json \
     --set channel-<n>,erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a,5000000000 \
     --counterparty-chain-id channel-<n>=injective-1
   aetherd tx governance submit-param-change-proposal usdc.json <deposit> --from <key> ...
   ```

   A bad list is refused when proposed (`MsgUpdateParams.ValidateBasic`),
   not when it would execute.
3. Validators vote. It executes when the voting period ends with 2/3 of
   the non-abstain power yes and 60% of the active validators voting.
4. Point the tools at it: `wallet.MainnetUSDC("channel-<n>")`, or
   `--usdc-path transfer/channel-<n> --usdc-base-denom
   erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a --usdc-issuer Injective`.

The voting period is 7 days by default (`x/governance` genesis
`voting_period`), so USDC opens about a week after the path does unless
`aether-1`'s genesis sets a shorter one. Tenure weights votes during the
first 30 days, but the 2/3 is a share of the power cast, so new
validators can still pass it.

`wallet.InjectiveMainnetUSDCBaseDenom` is Circle's mainnet USDC on
Injective, `erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a` (6
decimals), as Circle lists it and as Injective's verified token list
(`InjectiveLabs/injective-lists`) names it, checked 2026-10-26.

**Tested** (`TestInflowCap_ListedByGovernance`, on an `aether-rc-` chain
ID so every gate is open as on `aether-1`): a transfer is refused with
nothing listed; a duplicate list is refused at submission; the real
proposal, votes and execution list the channel; then a transfer up to
the cap comes in and one more is refused. On the devnet the draft
command refused `channel-0` named as `injective-1` (its client follows
`counterparty`), refused a channel that doesn't exist, and wrote a file
`submit-param-change-proposal` accepts.

## Still to do

- The testnet height, with `testnetInflowEntries` checked against what
  is on Aether then.
- Decide `aether-1`'s governance voting period (USDC opens one period
  after the path does).

## Later

- **A flow limit as a second layer:** the ibc-apps rate limiter with a
  24-hour net-flow percentage, once the voucher supply is well above 0,
  to slow a drain inside the cap.
- **An outflow cap**, if AETH leaving Aether needs one.

## Not doing

- No wasmd on Aether for this.
- No ibc-go upgrade for this.
- No outflow cap at launch.
