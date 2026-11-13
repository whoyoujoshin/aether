> **Design reference, with placeholder data.** This file is the designer's handoff and is left as written, apart from the globe filename below. The UI and these screens are what to build. The figures were taken from older binaries and are stale; wire them to the live chain as described in [NOTES.md](NOTES.md), and keep the design.

# Handoff: Aether Explorer redesign

## Overview
A redesign of the Aether block explorer (live: https://explorer.157-245-252-221.sslip.io/). Aether is a dual-strand chain: an **Aether** proof-of-work strand (6s blocks, epochs of 1,440 blocks, `uaeth`/AETH, bech32 prefix `aeth`) and a second BFT strand (4s blocks, shown as "IBC") linked by IBC packets. The redesign covers every top-nav page plus Block, Transaction, and Address detail.

## About the design files
The files here are **design references built in HTML**. They show the intended look and behavior; they are not production code to copy. Rebuild them inside the existing explorer codebase using its framework and patterns (or pick an appropriate one if starting fresh). Wire them to the real chain APIs (CometBFT RPC, Cosmos SDK REST/LCD, IBC client/channel queries).

Open `aether-explorer-mockups.html` in a browser. It's a pan/zoom canvas of numbered options. **The ones to build are 2b, 3a, 3b, 4a–4e, 5a, 5b, 6a.** 1a–1c and 2a are earlier rejected directions; skip them.

## Fidelity
**High-fidelity.** Colors, type, spacing, radii, and interactions are final. Match them closely. All data is simulated placeholder data, including addresses, monikers, numbers, endpoint URLs, API paths, and IP locations.

## Global shell (all pages)
- Page bg `#0a0908`; content padding 28px 32px 36px; designed at 1280px wide.
- Top bar 60–64px tall, bottom border `1px #1c1916`. Left side: Æ mark (`brand/aether-mark-reversed.svg`, 26px), then "AETHER" (Space Grotesk 600 13px, letter-spacing .32em), then "Explorer" (13px `#9a9186`).
- Nav order: **Overview · Blocks · Validators · Governance · Services · For agents · Faucet.** Inactive `#9a9186` 14px; active `#f3efe6`.
- Search: 36px tall, radius 10, bg `#12100e`, border `#26221e`, placeholder "Search address, hash or height".
- Cards: bg `#12100e`, border `1px #26221e`, radius 14–18. A hero/"featured" card adds border `rgba(192,80,58,.35)` and bg `radial-gradient(… rgba(192,80,58,.16) → 0) , #12100e`.
- Table rows: padding 12–14px 20–22px, separator `#1c1916`, hover bg `#1a1714`. Column headers: JetBrains Mono 500 11px, letter-spacing .08em, `#6b645b`, uppercase.
- Section labels: JetBrains Mono 500 11px, letter-spacing .12em, uppercase, `#8f877c` (or `#c0503a` on hero cards).
- Every hash, address, and number uses JetBrains Mono. Links are `#c0503a`.
- Amounts show AETH first, with raw `uaeth` as secondary text (1 AETH = 1,000,000 uaeth).

## Screens

### 2b — Overview (single chain; superseded by 5a once dual-strand ships)
- Hero (gradient band at top of page): headline "Explore Aether Chain", a 680×56 search with a red Search button, and an inline stat line (height, epoch, difficulty, reward, treasury).
- Live block strip: a dashed "mining #next · Ns" tile, then the last 7 blocks as 128×124 tiles joined by 18px connectors. The newest tile glows red. Each tile shows height, short hash, a 16-cell tx grid, tx count, and age.
- Below: on the left (320px), the "Activity mix · 24h" stacked bar by msg type, and an epoch progress grid (48 cells; current cell glows). On the right, a Transactions list showing type icon, type, hash, block, status, and age.

### 3a — Block detail
- Breadcrumb "Blocks / 120,449". Title "Block **120,449**" (the number is in red mono with glow), prev/next buttons, and a green pill "FINALIZED · N CONFIRMATIONS". Full hash below with a copy icon.
- 2-column grid (1fr | 400px):
  - **Left, key/value list** (label column 180px): Timestamp, Epoch (with progress bar), Mined by, Block reward, Transactions, Gas used (bar), Size, Parent hash, App hash.
  - **Right, Proof of work card**: the block hash with its leading zeros highlighted in red, compared against the target; Difficulty; Nonce; a green "Valid — N× below target" line.
  - **Right, Neighbours card**: prev / current (red, glowing) / next block chips.
- Below: a "Transactions in this block" table (Hash, Type pill, From, To, Amount, Status).

### 3b — Transaction detail
- Title "Transaction", a SUCCESS pill, and the msg-type pill. Full hash below.
- Summary card (red glow) with a plain-English sentence ("X used a grant to send 12.5 AETH from Y to Z") and the amount on the right.
- Tabs: **Overview / Events / Raw JSON** (active tab has a 2px red underline).
  - **Overview, left column**: a key/value list (Block, Timestamp, Signer, Fee, Gas used/wanted with bar, Sequence, Memo), then a Messages card. Nested messages are indented with a red left border (e.g. MsgExec → #0.0 MsgSend).
  - **Overview, right column (360px)**: Lifecycle timeline (Broadcast → Included in block, with a red glowing dot → Finalized, green) and Balance changes (− amounts red, + amounts green).
  - **Events**: type/attributes rows.
  - **Raw JSON**: a mono `<pre>`.

### 4a — Validators
- Header grid (1fr | 340px).
  - **Left**: title and subtitle; stats (Active set 24/32, Bonded, Network uptime in green); a "Voting power by region" stacked bar with legend ("N countries · largest X%").
  - **Right**: a **globe card** (300px tall) that embeds `validator-globe.html`. It's an orthographic d3 globe that auto-rotates and can be dragged. Pins are sized by voting power (or block count); jailed validators get hollow pins. Footer reads "approx. · from IP".
- Tabs: **Signers / PoW miners.**
  - **Signers table**: rank, validator (initials tile, moniker, valoper address · city), voting power with bar, commission, a last-50-blocks uptime grid (25 cells; missed cells `#3a2a24`), and status (ACTIVE green / MISSING amber / JAILED red).
  - **PoW miners**: a table (miner · city, blocks this epoch with bar, rewards, last block) plus a "Block share" card.
- **Interaction**: hovering a row centers the globe on that node, enlarges its pin, tints the row `rgba(192,80,58,.07)`, and shows the node's name and city in the globe footer. Switching tabs swaps the pin set: signers are red pins, miners are bone `#e4d9c6`.
- **Data**: geolocate node IPs (from peer info/sentry) to **city level only**, server-side, using a GeoIP database such as MaxMind GeoLite2. Never expose raw IPs.

### 4b — Governance
- Header stats: Quorum, Pass threshold, Voting period.
- Live proposal hero card: id, a pulsing "VOTING · time left" pill, type, title (26px), description, proposer/deposit/end date, a turnout line, a tally stacked bar (Yes green, No red, Abstain `#9a9186`, Veto `#6b645b`) with legend, and a red "Vote with wallet" button.
- "All proposals" table with All/Voting/Passed/Rejected filters: id, title + type, a yes/no mini bar, date, and a status pill.

### 4c — Services
- "All systems operational" status pill.
- 2×2 cards (RPC, REST, gRPC, Faucet). Each has an icon tile, name, description, and status; a copyable endpoint URL; a 40-bar latency sparkline (degraded bars amber); and p50, 30-day uptime, and an extra line.
- Footer strip: Chain ID, Denom, Bech32 prefix, and an "Add to wallet" button.

### 4d — For agents
- Left doc nav (230px): Guide, Endpoints, Machine-readable (`/llms.txt`, `/openapi.json`).
- Main area: headline; 3 fact cards (Base URL, Auth, Rate limit); a code sample with **curl / python / js** tabs next to a response panel ("200 OK · 42ms"); and an endpoint list (GET in green, POST in red).

### 4e — Address
- Address heading with copy icon and an "Account" tag.
- Balance hero: total in AETH (48px mono) with uaeth below; a stacked bar for Available (red) / Delegated (bone) / Rewards (green).
- Side cards: tx count / first seen / last active; Authz grants given.
- History list: direction icon (↓ green, ↑ red, ◆/✓ neutral), label, hash, counterparty, amount, age. "Export CSV" link.

### 5a — Overview, dual-strand helix (primary Overview)
- The header adds a **chain switch: Both / Aether / IBC** (segmented control; the dot for Both is half red, half bone). It filters everything on the page.
- **Helix hero** (SVG, ~1168×250): two sine strands, 180° out of phase, across a time axis (last 96s; newest on the right at the "now" marker).
  - Strand colors: Aether `#c0503a` (with glow), second strand `#e4d9c6`.
  - Depth: the front half of each strand is a 3px solid line; the back half is 2px at 25–30% opacity. Faint vertical "rungs" (`#2a2521`) every 14px.
  - Blocks are dots on their strand, positioned by timestamp: filled when on the front of the helix, hollow when behind. The head block has a halo.
  - **IBC packets are glowing rungs** (`#f3efe6`, red drop-shadow) linking the strands at the packet's send time.
  - Blocks and packets glide left continuously (1s linear transitions). A dashed flow animation runs along the strands.
  - Selecting one chain dims the other strand to 12% opacity.
- Stat cards: Aether (Height, Epoch %, Difficulty, Reward) · IBC bridge (Packets 24h, In flight, Avg relay, AETH bridged) · second strand (Height, Validators, Txs 24h, Finality). A single-chain selection shows only that chain's card plus the bridge card.
- Merged "Latest blocks" list (chain badge per row) and "Recent transactions" (chain dot, type pill including MsgTransfer / MsgRecvPacket / MsgAcknowledgement / MsgUpdateClient, amount, age).

- **Helix rendering (latest)**: last 60s, ~2 turns, 196px tall. Strands are "tubes": dark 7px outline, a 4.5px vertical gradient body (Aether `#e0735b→#9c3b28`, second strand `#fbf6ec→#b9ad99`), and a 1.1px highlight offset −1.3px. The Aether strand also gets a blurred glow. Base pairs every 8px, colored half per strand, with opacity scaled by strand separation. The left 16% fades out via a mask. Front nodes use radial-gradient spheres; back nodes are hollow.
- **Bridge animations (per IBC packet)**:
  - **Helicase (in flight, send → recv)**:
    - Translucent "replication bubble" ellipse between the strands at the send point.
    - Spinning dashed ring (1.1s/rev) plus a gradient capsule on the *source* strand.
    - A dashed marching rung, and a dashed waiting ring on the *destination* strand.
    - A glowing value orb with its amount label travels source → destination, `progress = (now − sendTs) / relayTime`.
  - **Ligase (on recv)**: a ring contracts from 2.8× onto the destination strand, then a white flash (1.3s, once). After that, a permanent bone diamond "knot" marks the sealed packet, and the rung becomes a solid red→bone gradient bar with glow.
  - **Event line** (row under the legend): shows the latest bridge event for 3.5s ("Helicase · packet #N unwinding · X AETH Aether → IBC" in `#e98a72`, or "Ligase · packet #N sealed on IBC at #height" in bone); otherwise "bridge idle · N in flight".
  - **Data**: send = IBC `send_packet` event; seal = `recv_packet` on the counterparty; ack can drive the final state.

### 5b — Blocks, dual-strand
- Same chain switch in the header; head heights for both strands at the top right; a compact helix (120px tall).
- **Both**: one timeline table, newest first. Aether blocks are on the left, the second strand's blocks on the right, and a center 120px column draws the helix continuing vertically (each row is a segment, with the block's node on its strand). IBC packet rows draw a glowing rung across the center, with "MsgTransfer N AETH →" on the sending side, "recv #height · Ns" on the receiving side, and an IN FLIGHT (amber) or ACKED (green) pill.
- **Single chain**: a normal table (Height, Hash, Miner/Proposer, Txs, IBC packets, Age) with a single-strand helix spine at the left.

### 6a — Faucet
- **Request card** (left, red glow):
  - "Get 10 AETH". Address input: 50px tall, mono. The border turns green when valid, red when invalid, with an inline VALID / NOT AN AETH ADDRESS label.
  - A "Use a demo address" link; a "Receive on: Aether / IBC" segmented control; a full-width button.
- **Request flow**: idle → `pow` (the browser solves a small PoW instead of a captcha; shows a hash counter and progress bar, ~1.6s) → `bc` (broadcasting) → `done` (green "10 AETH sent", tx hash, NEW WALLET pill). The button then disables with "Sent. Request again in 24h". The new drip is prepended to Recent drips.
- **KPIs**: Drips all time · Unique wallets funded · New wallets created (green) · Created by agents %.
- **"Who requests · 30d"**: Agents (signed agent key) / People (browser PoW) / Unverified API (no key, tighter limits).
- **"New wallets created via the faucet"**: a 14-day stacked bar (agents red, people bone, unverified `#4a443d`). Also a Top agents list (name, key prefix, bar, wallet count).
- **Recent drips table**: recipient + NEW WALLET pill, strand, source pill (AGENT · name / WEB / API), tx, amount, age.
- **Tracking logic to implement**:
  - Tag every drip with its source: browser-PoW → `web`, request signed with an agent key → `agent:<key id>`, keyless API → `api`.
  - A **new wallet** is an address whose first-ever transaction is that faucet drip; check the account sequence/history at drip time.

## Interactions summary
- **Chain switch** (5a, 5b): `both | A | B`, a separate state per page; strand opacity transitions in .4s.
- **Live data**: the mock simulates a new Aether block every 6s and a second-strand block every 4s. Use websocket/RPC subscriptions (`NewBlock`, IBC `send_packet` / `recv_packet` / `acknowledge_packet` events).
- **Tabs**: 3b (Overview/Events/Raw), 4a (Signers/Miners), 4d (curl/python/js).
- **Globe** (4a) ↔ row hover sync, described under 4a.
- **Faucet** state machine, described under 6a.

## Design tokens
- **Ink / surfaces**: `#0a0908` page · `#0e0c0b` deep panel · `#12100e` card · `#1a1714` hover · `#1c1916` row line · `#221e1a` / `#26221e` borders & tracks · `#2a2521` / `#3a342e` / `#4a443d` dim lines.
- **Text**: `#f3efe6` primary (bone) · `#d8d1c5` secondary · `#9a9186` muted · `#8f877c` labels · `#6b645b` faint.
- **Brand accent (signal red)**: `#c0503a`; tints `rgba(192,80,58,.06–.35)`.
- **Second strand**: `#e4d9c6`.
- **Semantic**: success `#16c784` (success / finalized / + amounts only), error `#f0506e`, warning `oklch(0.8 0.14 80)`.
- **Msg types**: MsgSend `#c0503a`, MsgVote `oklch(0.76 0.14 300)`, MsgExec/MsgGrant `oklch(0.76 0.13 235)`, MsgSubmitPoW `oklch(0.8 0.14 80)`, MsgSubmitParamChangeProposal `oklch(0.76 0.14 350)`. Pill bg is the type color at 13% opacity.
- **Type**:
  - Space Grotesk 400/500/600/700 for UI.
  - JetBrains Mono 400/500/600/700 for data.
  - Scale: 11 (labels), 12, 13, 14 (body), 15–17 (card titles), 20–26 (stats), 34 (page titles), 48–72 (hero numbers).
- **Radius**: 5–7 pills · 8–10 inputs/tiles · 12 buttons · 14–18 cards · 999 status pills.
- **Glow**: `box-shadow: 0 0 8–24px <accent>` and `text-shadow: 0 0 18–28px rgba(accent,.5)`, used only on live or "newest" elements.

## Assets
- `brand/`: Aether logo SVGs supplied by the owner. The UI uses `aether-mark-reversed.svg`; the favicon is `aether-favicon-32.svg`.
- Globe geometry: Natural Earth via `world-atlas@2.0.2/countries-110m.json`, rendered with d3-geo orthographic.

## Files
- `aether-explorer-mockups.html`: self-contained viewer of all screens. Keep `validator-globe.html` beside it so the 4a globe loads.
- `validator-globe.html`: the globe used in 4a. It receives `{type:'aether-globe', points:[{lat,lon,label,weight,status}], hl, color}` via `postMessage` and replies with `aether-globe-ready`.
- `source/`: editable design source (`Explorer Mockups.dc.html` plus its runtime and assets).
