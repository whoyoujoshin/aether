# Notes for whoever implements this

This folder is a **design reference**. Colors, type, and layout are the
designer's, and this is the UI to build. Every number, address, moniker,
endpoint, and location on the screens is placeholder data (the handoff says
so), taken from older binaries. Keep the screens and wire those figures to
the live chain. The designer's [README.md](README.md) is unchanged apart
from the globe's filename and the banner at the top.

## Wire the placeholders to the live chain

- **Second strand.** Draw it as Aether's IBC counterparties, Osmosis
  (`osmo-test-5`) and Injective (`injective-888`), from the real client,
  channel and packet data (`GET /api/helix`, `GET /api/ibc`,
  `cmd/explorer/helix.go`). The Both / Aether / counterparty switch stays.
  Aether stays the proof-of-work strand. Heights, validator counts and
  block intervals come from each chain's RPC (`height`, `validators`,
  `blockTimeSecs`). The mock's 6s and 4s timers stand in for those
  readings. With `--ibc-rpc` listing both chains, the picker chooses which
  counterparty is the second strand.
- **Addresses.** Render accounts as `aether1…`. `app.Bech32MainPrefix` in
  `app/app.go` is `aether`, and the faucet accepts that prefix
  (`cmd/faucet` `bech32Pattern`). Validator operator addresses use
  `aethervaloper`. The mock's `aeth1…` strings stand in for real accounts.
- **Faucet.** "Get 10 AETH" and "request again in 24h" are the layout. The
  amount, cooldown and proof-of-work difficulty come from the faucet's
  `/stats`: `amount_uaeth` (the `cmd/faucet` default is 1,000,000 uaeth,
  1 AETH; `--opener` without `--amount` sends 1,000 uaeth, 0.001 AETH),
  `address_cooldown_secs` (default `--cooldown-minutes` is 60) and
  `pow_bits` (default 18). The browser proof is that challenge: SHA-256 of
  `challenge + ":" + nonce` with that many leading zero bits
  (`cmd/faucet/pow.go`, `explorer-web/src/faucetwork.ts`). Mining on the
  chain is scrypt; the faucet challenge stays this SHA-256 puzzle. The
  who-requests chart, new-wallet bars and top-agents list read the drip
  log. When it has no agent-signed drips, those panels stay empty. A
  request with `"strand":"ibc"` follows `ibc_enabled` on `/stats`.
- **Validators.** The active-set line reads the live set against
  `TopKSize` (21 in `x/pow` `DefaultGenesisState`, in `testnet/genesis.json`,
  and in the seat rule, `docs/SEAT-RULE.md`). The "24/32" on the mock is
  that same stat filled with sample numbers. Globe pins are city-level
  places an operator published (`GET /api/locations`). That response has
  no IP. A node with no published place reads "location not shared".
- **Block ticker.** Drive the motion from the heights and `blockTimeSecs`
  the node returns. Genesis `target_block_time` is 60 seconds; validators
  run `timeout_commit = "5s"`, and the measured interval after that was
  5.6 seconds (`docs/NUMBERS.md`). Epoch length is 1,440 blocks. 1 AETH is
  1,000,000 uaeth.

## Brand files

The only logos to use are in [`docs/brand/`](../../brand/README.md). The
zip's `brand/` SVGs and `uploads/Aether Logo.pdf` were compared byte for
byte with that folder. Identical files were not copied again:

| Zip file | Already in `docs/brand/` |
|---|---|
| `uploads/Aether Logo.pdf` | `aether-primary-identity.pdf` |
| `aether-app-icon.svg` | `icons/app-icon.svg` |
| `aether-favicon-16.svg` | `icons/favicon-16.svg` |
| `aether-favicon-32.svg` | `icons/favicon-32.svg` |
| `aether-lockup-horizontal-dark.svg` | `lockups/lockup-horizontal-on-dark.svg` |
| `aether-lockup-horizontal-light.svg` | `lockups/lockup-horizontal-on-bone.svg` |
| `aether-lockup-stacked-dark.svg` | `lockups/lockup-stacked.svg` (bone on ink) |

These SVGs were not in `docs/brand/` and are added there:

| Added | Zip name | What it is |
|---|---|---|
| `marks/mark-color.svg` | `aether-mark-color.svg` | Ink Æ, signal middle bar |
| `marks/mark-black.svg` | `aether-mark-black.svg` | Æ in ink only |
| `marks/mark-reversed.svg` | `aether-mark-reversed.svg` | Bone Æ, signal middle bar |
| `marks/mark-white.svg` | `aether-mark-white.svg` | Æ in bone only |
| `lockups/lockup-stacked-on-bone.svg` | `aether-lockup-stacked-light.svg` | Stacked lockup, ink on bone |

`source/assets/` keeps the two files the editable source loads by relative
path. `aether-favicon-32.svg` matches `docs/brand/icons/favicon-32.svg`.
`aether-mark-reversed.svg` matches `docs/brand/marks/mark-reversed.svg`.
The handoff's top bar uses that reversed mark. The mock's UI accent
`#c0503a` is the screen color in the handoff; the mark's signal bar is the
sheet's `#c74d38`. Use the mark file rather than recoloring it.

The zip's `uploads/` PNGs (`screens-*.png`, `logo-p1.png`) are not
referenced by the HTML and are not in this folder.

## Opening the HTML offline

`aether-explorer-mockups.html` is the viewer to open. It inlines its
fonts (Space Grotesk and JetBrains Mono as woff2), the Æ mark, the favicon,
React, ReactDOM, and the globe page, so it does not need a sibling file for
the screens. Country shapes on the 4a globe still request:

- `https://cdn.jsdelivr.net/npm/world-atlas@2.0.2/countries-110m.json`

That request fails offline, and the globe then draws without land. d3 and
topojson-client are inlined in this viewer. The `unpkg.com` React URLs in
its manifest are the ids of those inlined copies, not loads.

`source/Explorer Mockups.dc.html` is the editable source. Checked relative
links, all present:

- `support.js`
- `assets/aether-favicon-32.svg`
- `assets/aether-mark-reversed.svg`
- `../validator-globe.html` (the iframe was `Validator Globe.html`; renamed
  so the filename has no spaces, and pointed at the single copy beside the
  viewer)

That source then needs the network for:

- `https://fonts.googleapis.com/css2?family=Space+Grotesk…&family=JetBrains+Mono…`
- `https://unpkg.com/react@18.3.1/umd/react.production.min.js`
- `https://unpkg.com/react-dom@18.3.1/umd/react-dom.production.min.js`
- `https://unpkg.com/@babel/standalone@7.29.0/babel.min.js` (from `support.js`)

`validator-globe.html` has no relative file links. It loads:

- `https://unpkg.com/d3@7.9.0/dist/d3.min.js`
- `https://unpkg.com/topojson-client@3.1.0/dist/topojson-client.min.js`
- `https://cdn.jsdelivr.net/npm/world-atlas@2.0.2/countries-110m.json`

The self-contained viewer does not use this file; the editable source's
globe iframe does. It stays beside `aether-explorer-mockups.html` so that
path resolves.
