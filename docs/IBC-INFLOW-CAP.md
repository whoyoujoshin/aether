# IBC inflow cap for mainnet

**Status:** design only. No code. For Cosmos SDK v0.50 with ibc-go v8.8.
**Related:** `docs/MAINNET.md` §2 (one USDC path, inflow cap).

## Why

A takeover or a bug on Aether can lose, at most, the value that IBC
counterparties hold in escrow for Aether's channels. An inflow cap on
Aether's side refuses a received packet that would take a denom over
the cap. The counterparty refunds the refused packet, so the
counterparty's escrow never grows past the cap. That bounds what a
forged header (trust level 1/3) could release.

## Options surveyed

| Option | What it is | Fits ibc-go v8.8? | Notes |
|---|---|---|---|
| Stride `x/ratelimit` (now in `cosmos/ibc-apps` `modules/rate-limiting`) | Native Go IBC middleware. Per channel+denom, fixed window, net flow. | Yes. The `ibc-apps` line tracks ibc-go v8. | Governance messages to add/update/remove limits. Undoes on timeout and ack error. |
| Osmosis `cw-ibc-rate-limit` | Middleware that calls a CosmWasm contract for the check. | Yes, if wasmd is wired. | Extra moving part (contract + middleware). Aether does not run wasmd on the chain itself today. |
| ibc-go's own rate-limiting middleware | Moved into ibc-go at v11 (`modules/apps/rate-limiting`). | No. Aether is on v8.8. | The migration guide is v10 → v11. Not available without an ibc-go upgrade. |

## Proposal

Use the **ibc-apps rate-limiting middleware** (Stride's native Go
module) on the ICS-20 transfer stack. Do not pull in wasmd for this.
Do not upgrade ibc-go just for the middleware.

### Limits

- **Granularity:** per channel and per denom. The first limit is on
  Injective's USDC (`erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a`
  as the bank denom on Injective, then the IBC trace on Aether) on the
  Aether↔Injective channel.
- **Direction:** inflow only at first (`MaxPercentRecv` or an absolute
  quota). Outflow can be added later. Net flow (in minus out) so a
  ping-pong does not exhaust the cap.
- **Window:** 24 hours, fixed, reset by governance or by the module's
  own timer. A shorter window (1 hour) is an alternative if the first
  weeks show bursts.
- **Starting size:** 10,000 USDC, as `docs/MAINNET.md` suggests. Raised
  by governance after the seat-rule evidence and the two-week trial.
- **One path:** only the Injective USDC channel is open at launch.
  Other denoms and channels have no limit configured, which means they
  are refused until a limit is added (fail closed), or the middleware
  is wired only on that channel. Prefer fail closed.

### How it works with Aether's relayers

- **Helicase** (on the validators) relays packets *onto* Aether. The
  rate-limit middleware sits in the transfer stack's `OnRecvPacket`.
  Helicase submits the same `MsgRecvPacket` any relayer would, so the
  middleware sees it. A packet over the cap returns an error
  acknowledgement; Helicase does not special-case it.
- **cmd/outbound** relays the other way (Aether → counterparty). It
  signs on the counterparty, not on Aether. The cap on Aether does not
  inspect those sends. If an outflow cap is added later, it belongs in
  the `SendPacket` / ICS4 wrapper path, which outbound does not touch.
- **No change to either relayer** for the inflow cap. The middleware
  is consensus code.

### Stuck or refunded packets

| Event | What the limit must do |
|---|---|
| Packet received, under cap | Quota increases. Tokens minted/unlocked. |
| Packet received, over cap | Error ack. Quota does **not** increase. Counterparty refunds. |
| Packet times out on Aether before recv | No recv happened, so no quota change. Counterparty refunds on timeout. |
| Packet acked with error (middleware refused, or app refused) | If the middleware incremented on the way in, it must undo on the error ack. The ibc-apps middleware does this in `OnAcknowledgementPacket` for sends, and refuses before incrementing on recv. Confirm the recv path never increments on a refused packet. |
| Packet stuck (relayer down, client expired) | Quota is unchanged until a recv or a timeout. A stuck packet does not consume the cap. It does delay the refund on the sender chain. |
| Successful recv, then a later refund via a different packet | The refund is a new transfer the other way. Net-flow accounting reduces the inflow counter. Absolute-quota accounting does not, unless the refund is explicitly subtracted. Prefer net flow. |

### Risks

- **Fail open vs fail closed.** If the middleware is not wired, or a
  channel has no limit, inflows are unlimited. Wire it before the
  channel opens, and refuse unknown denoms.
- **Undo on error.** A bug that increments on a refused recv, or fails
  to undo on a timeout of a send, either locks the cap or lets it be
  bypassed. Test both with the middleware's own tests and an Aether
  devnet.
- **Helicase per-block limit.** Helicase already caps how many packets
  a block can relay. The rate-limit cap is a second, longer window.
  They do not conflict, but a burst that fits in one Helicase block
  can still hit the rate-limit cap.
- **Client expiry.** A light client that expires stops the path
  (`docs/MAINNET.md`). The cap does not help there. `ibcwatch` paging
  a human stays required.
- **Governance of the cap.** Raising the cap is a governance proposal.
  The seat-rule hardening (460,800) should be live and observed before
  the first raise.
- **ibc-go upgrade later.** When Aether moves to ibc-go v11, the
  middleware moves into `ibc-go/modules/apps/rate-limiting`. The
  migration is a code change in `app.go`, not a state migration of the
  limits, if the store layout matches. Confirm before the upgrade.

## Not doing

- No code in this note.
- No wasmd dependency.
- No ibc-go upgrade for this feature.
- No outflow cap at launch.
