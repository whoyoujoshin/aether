# Plan: Aether ↔ Osmosis ↔ Noble, paying in USDC or AETH

Goal: an agent (or person) on Aether can hold and pay in **USDC** as easily as in
**AETH**, with AETH tradeable against USDC on Osmosis. Written 2026-09-29.
Everything here is **testnet ↔ testnet**: testnet USDC has no value, and a
connection to Noble's or Osmosis's mainnets waits for an audited Aether
mainnet (see [Phase 6](#phase-6-later-mainnet)).

## The shape of it

```
                 Circle CCTP (Sepolia, Base Sepolia, ...)
                              │  burn → mint
                              ▼
   Osmosis testnet ◄──IBC──► Noble testnet (grand-1) ◄──IBC──► Aether (aether-testnet-1)
   (osmo-test-5)                 issues USDC (uusdc)            pays in AETH or USDC
        ▲                                                             │
        └──────────────────────────IBC──────────────────────────────┘
              AETH liquidity: an AETH/USDC pool, price, swaps
```

- **Noble is where USDC comes from.** Circle issues USDC natively on Noble
  (`uusdc`); every Cosmos chain's canonical USDC is Noble's, sent over a
  *direct* Noble channel. USDC that reached Aether through Osmosis
  (`transfer/<aether-osmo>/transfer/<osmo-noble>/uusdc`) would be a different,
  non-interchangeable token. So Aether needs its **own channel to Noble**; Osmosis
  can't stand in for it.
- **Osmosis is where AETH gets a market.** AETH sent there can be pooled against
  USDC (Noble's, which Osmosis already treats as canonical), giving a price and
  a way to swap either direction.
- The two connections are independent. Osmosis comes first only because its
  runbook is already written.

## What we already have

- IBC core, ICS-20 transfers and ICS-27 interchain accounts: live on
  `aether-testnet-1` since block 122,000.
- `cmd/relayer`: the only relayer that can sign Aether's ML-DSA-44 transactions
  (Hermes and `cosmos/relayer` hardcode secp256k1). Verified end to end on the
  live chain against a throwaway counterparty. **It makes one pass and exits:**
  client, connection, channel, a test transfer out and back.
- `docs/OSMOSIS-TESTNET.md`: the runbook for Osmosis, blocked only on the
  governance gate below.
- Escrow, authz grants and the paywall already take any coin in principle; the
  agent tools and paywall are AETH-only in practice (see Phase 4).

## What we learned about the other side

| | Osmosis testnet | Noble testnet |
|--|--|--|
| Chain ID | `osmo-test-5` | `grand-1` |
| Stack | Cosmos SDK 0.50, ibc-go v8.7, CometBFT 0.38 | Cosmos SDK 0.50.14, ibc-go v8.7, CometBFT 0.38.21 (Noble `main`) |
| Fees | `uosmo` (Noble USDC also accepted as a fee token) | **`uusdc`** only: there's no separate gas token |
| Relayer fees | normal | `MsgRecvPacket`, `MsgAcknowledgement`, `MsgUpdateClient` and timeouts **pay no fee** (Noble's `globalfee` bypass list); the one-time handshake does |
| Unbonding | 24h: a client of it must be refreshed within ~16h | 21 days: its client is forgiving |
| Governance | token voting | none: a multisig "authority" sets parameters |
| Faucet | `faucet.testnet.osmosis.zone` (~100 OSMO/day) | **Circle's faucet** (`faucet.circle.com`): 10 testnet USDC per address per 24h |
| Channel to Aether | open it ourselves | open it ourselves: IBC is permissionless |
| Already connected | Noble ↔ Osmosis testnet: `channel-22` ↔ `channel-4280` | same |

Worth knowing about Noble:

- **Rate limits.** Noble runs the IBC rate-limiting module; its authority can cap
  USDC flowing over a channel. Nothing is capped until they set a limit.
- **Forwarding accounts.** A Noble address can be made to forward whatever it
  receives straight over IBC to a chosen channel and recipient. Combined with
  CCTP, that means **USDC burned on an EVM testnet lands on an Aether address in
  one step**, which is the onboarding path for anyone outside Cosmos.
- **Packet forwarding** (PFM) lets a transfer hop through Noble to another chain
  in one user action.
- **USDN** (Noble's yield-bearing dollar) pays yield to chains that integrate it.
  That's optional and not part of this plan.

Sources: the Cosmos chain registry (`testnets/nobletestnet`,
`testnets/osmosistestnet`, `testnets/_IBC/nobletestnet-osmosistestnet.json`),
Noble's `main` branch (`go.mod`, `app.go`), Noble's `globalfee` docs, and Circle's
faucet docs. Endpoints change: the registry now lists
`rpc.osmotest5.osmosis.zone` where the Osmosis runbook says
`rpc.testnet.osmosis.zone`. Confirm both chains' RPC endpoints from the seed
before step 1 of any phase. This planning session can't reach either chain.

## Phase 0: prerequisites (through Oct 4)

1. **Governance gate.** Proposals #3 (evidence age 2,880 blocks) and #4
   (`bond_cooldown` 51,840 blocks ≈ 72h) execute after voting ends on
   **2026-10-04 ~15:58 CT**. No long-lived connection before then (see
   `docs/IBC.md`). There's already a check-in scheduled for that.
2. **Weekend cutover** (escrow and the consensus-key guard) doesn't block IBC, but
   it's better not to open channels during a coordinated restart.
3. **Funding, starting now** (the faucets are slow):
   - an Aether relayer key (ML-DSA), from the Aether faucet;
   - an Osmosis relayer key: ~100 OSMO/day, a few days' worth;
   - a Noble relayer key: **10 USDC/day from Circle's faucet**, enough for the
     handshake (a client, 2 connection and 2 channel messages).

## Phase 1: make the relayer a service

> **Update, 2026-09-29: [Helicase](HELICASE.md) shrinks this phase.** The
> block proposer now relays everything *onto Aether* (client updates,
> packets, acknowledgements, timeouts) with no relayer key, and keeps
> Aether's clients of Noble and Osmosis fresh. What's left for a relayer
> service is the other direction, onto Noble and Osmosis, and
> `cmd/outbound` now does it: unattended, signing only there with an
> ordinary key, with a health endpoint. Decision 1 below is moot. The
> table is kept for the record.

Both connections need packets relayed continuously and clients refreshed
before they expire. The Osmosis client is the tight one: 24h unbonding. Our
relayer can't do that yet. That's the one real build before either connection
is useful. Two ways, which is **decision 1**:

| | A. Extend `cmd/relayer` into a daemon | B. Fork `cosmos/relayer` (`rly`) and add ML-DSA signing |
|--|--|--|
| Work | A loop over each path: scan both chains for `send_packet` / `write_acknowledgement`, relay `RecvPacket`, `Acknowledgement` and timeouts with proofs, refresh clients at ⅓ of the trusting period, clear backlogs after downtime, persist progress, a health endpoint | Register the ML-DSA key type in `rly`'s keyring and codec, sign Aether's transactions with it, and keep the fork current |
| Gain | Small, and every line is ours; the handshake and packet proofs already work live | Years of production relaying: packet clearing, multi-path, metrics, fee handling, known behavior against Noble and Osmosis |
| Risk | We rediscover edge cases others solved (reorged heights, stuck acks, out-of-order sequences) | We carry a fork of a large codebase; `rly`'s own abstractions may assume secp256k1 in places |
| Estimate | ~1–2 weeks to something we'd leave unattended | ~1 week to a spike showing whether the key plug-in is clean; then it's mostly config |

**Recommendation: spike B for two or three days first.** If ML-DSA plugs into
`rly`'s keyring cleanly, it's the less risky thing to run unattended against
chains other people operate. If it doesn't, fall back to A, which we know
works.

Either way:
- it runs on the seed under systemd with `Restart=always`;
- it pages when a client is within a day of expiring;
- it pages when packets wait longer than N blocks.

`minerwatch`'s signed-webhook pattern fits here.

## Phase 2: Aether ↔ Osmosis testnet

Follow `docs/OSMOSIS-TESTNET.md` with the service from Phase 1 instead of the
one-shot. Then:

1. **AETH on Osmosis.** Send AETH over. On Osmosis it's
   `ibc/<hash of transfer/<osmo-side channel>/uaeth>`.
2. **An AETH/USDC pool** (optional, needs liquidity: **decision 3**). Creating a
   pool costs a fee in OSMO or USDC on testnet. Seed it with testnet USDC from
   Noble and AETH from our faucet. That gives AETH a price and makes swaps
   possible.
3. **Get listed.** Open a PR to the Cosmos chain registry adding
   `testnets/aethertestnet` (chain info, `uaeth` asset) and
   `testnets/_IBC/aethertestnet-osmosistestnet.json`. Wallets and explorers read
   these. Note that Keplr and similar wallets can *send to* Aether addresses but
   can't sign *for* them (ML-DSA), so Aether-side signing stays with our tools.

**Done when:** AETH goes to Osmosis and back unattended for a week, the Osmosis
client never expires, and the channel shows on both chains' explorers.

## Phase 3: Aether ↔ Noble testnet

1. Open the path with the Phase 1 service, paying Noble's handshake fees in testnet USDC.
2. **Bring testnet USDC over:** from Circle's faucet on Noble, then an IBC
   transfer to an Aether address.
3. **Pin the canonical USDC denom** on Aether:
   `ibc/<SHA-256 of transfer/<aether-side channel to Noble>/uusdc>`. Every tool
   that says "USDC" must mean exactly this denom. Any other token called USDC
   (for example, USDC that took a detour via Osmosis) must not count.
4. **Tell Noble.** Nothing requires permission, but letting Noble's team know the
   channel exists means they won't be surprised by it and can set sensible rate
   limits instead of none.
5. Add `testnets/_IBC/aethertestnet-nobletestnet.json` to the chain registry.

**Done when:** USDC round-trips Noble → Aether → Noble unattended, and Noble's
explorer shows the channel.

## Phase 4: USDC as a first-class payment asset on Aether

> **Progress, 2026-09-29.** Built:
> - `wallet.Assets`, with USDC pinned to `transfer/<channel>/uusdc`;
> - agentmcp spending, escrowing, invoicing and receiving USDC, under the
>   USDC caps described below, which are off until set.
>
> Also built: the paywall and `cmd/paywall` charge one asset each, USDC
> included, for every scheme, and state it in the 402, the manifest and
> the receipts. `fetch_paid` pays a USDC price per request, by prepaid
> deposit or by pull allowance, and `withdraw_prepaid` takes a USDC
> balance back. `find_services` shows each service's asset. The
> TypeScript and Python clients send, receive and buy in USDC the same
> way (`usdcChannel` / `usdc_channel`). The desktop wallet shows USDC as
> "USDC (Noble)", with the denom on hover, and sends it. The explorer
> labels it on addresses, transactions and services. Both
> take `--usdc-channel` (the wallet also reads `AETHER_USDC_CHANNEL`),
> and both show any other token by its bare denom, never as USDC,
> whatever it calls itself.
>
> Still to do:
> - the clients' seller kits charging USDC (`cmd/paywall` does);
> - setting `--usdc-channel` for the desktop wallet, the public explorer
>   and the Claude Desktop bundle once the Noble channel exists (the
>   bundle's settings can't be blank, so it waits for a real channel
>   number).

The chain already moves any coin. The tools around it assume AETH: about 60
references in `agentmcp`, 40 in the paywall, and 180 across the TypeScript and
Python clients. The work:

- **One asset registry** (in `wallet`): `AETH` = `uaeth`; `USDC` = the pinned
  denom from Phase 3, 6 decimals, source "Noble". Amounts parse as `"5 USDC"`
  exactly like `"5 AETH"` today, and every output states the asset.
- **agentmcp:** an `asset` on `send_aeth` (or a `send` alias), `create_escrow`,
  `create_invoice` / `wait_for_payment` and `fetch_paid`. Spending limits are
  **per asset** (`--per-tx-limit "1 AETH,5 USDC"`), since adding AETH to USDC
  needs a price we don't have. `get_balance` lists both.
- **Paywall (x402):** the price's `asset` is already a field in our 402
  responses (`paywall.Asset`, hardcoded `uaeth`); make it a flag, so a seller can
  charge `0.05 USDC`. Prepaid and pull payments follow the same asset.
- **TypeScript and Python clients, the desktop wallet and the explorer:** show and
  send USDC, labeled as "USDC (Noble)" with the denom visible on hover or detail.
- **Fees:** an agent holding only USDC can't pay a fee in AETH. Options, which is
  **decision 2**: keep fees at zero on testnet (as now); have operators also
  accept the USDC denom in `minimum-gas-prices`, a config change with no code;
  or cover agents' fees with feegrant.

**Done when:** an agent with only USDC pays a paywalled API in USDC, escrows USDC
for another agent, and the receipt, limits and balances all say USDC.

## Phase 5: moving between the two

- **Hire an Aether agent from anywhere.** [Ligase](LIGASE.md) is built: a
  USDC transfer from Noble with an escrow instruction in its memo opens an
  escrow for an Aether payee, and later transfers release it or bring
  refunds home. With CCTP and Noble forwarding in front, that can start on
  an EVM chain (whether forwarding passes the memo along still needs
  checking).

- **Swap AETH ↔ USDC from Aether.** Either use Osmosis's IBC hooks (send AETH
  with a memo naming a swap, and get USDC back on Aether in one action) or use
  an ICS-27 interchain account that Aether already supports. Both are testnet
  experiments first; hooks are simpler if Osmosis testnet's swap contract is
  deployed.
- **USDC in from EVM chains.** Use CCTP on Sepolia or Base Sepolia to mint on a
  Noble forwarding address set to Aether's channel and an Aether recipient. The
  money lands on Aether without the user ever touching Noble. An `agentmcp` tool
  or a page in the desktop wallet can generate the forwarding address.

## Phase 6 (later): mainnet

Not before Aether has a mainnet and an independent security audit. At that
point: real USDC, Noble's mainnet (whose authority may set rate limits for a new
channel), and a relayer service with real on-call. None of the testnet channels
carry over; mainnet gets its own.

## Decisions for you

1. **Relayer:** fork `cosmos/relayer` and add ML-DSA signing (recommended,
   after a 2–3 day spike), or grow our own relayer into a daemon.
2. **Fees for USDC-only agents:** stay at zero, accept USDC for gas, or feegrant.
3. **Osmosis pool:** do we seed an AETH/USDC pool on Osmosis testnet, and with
   how much?
4. **Order:** keep Osmosis → Noble, or run them in parallel. Nothing ties them
   together, and Noble is the one that makes "pay in USDC" real.

## Rough timeline, if we start after Oct 4

| When | What |
|--|--|
| Now → Oct 4 | Phase 0: fund keys; relayer spike (decision 1) |
| Week 1–2 | Phase 1: relayer service on the seed |
| Week 2 | Phase 2: Osmosis channel; AETH on Osmosis; chain-registry PR |
| Week 2–3 | Phase 3: Noble channel; USDC on Aether; denom pinned |
| Week 3–5 | Phase 4: USDC through wallet, agentmcp, paywall, clients, desktop |
| After | Phase 5 experiments; Phase 6 when mainnet is real |
