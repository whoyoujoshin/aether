# Aether primary identity

**These are the only logos and marks to use going forward.** Do not invent
alternate Æ marks, wordmarks, or colorways without updating this sheet.

Source of truth for construction: `aether-primary-identity.pdf`.
Ready-to-use cuts of the mark and lockups live in `icons/` and `lockups/`.

## Sheet (canonical)

| File | Use |
|---|---|
| `aether-primary-identity.pdf` | Full brand board (construction, lockups, sizes, in-use) |
| `aether-primary-identity.png` | Raster preview of the board |
| `aether-primary-identity.svg` | Vector export of the board |

## Icons (`icons/`)

Rounded app tile with bone Æ + signal middle bar on ink (except 16px favicon, which is one-ink per the sheet).

| File | Size | Use |
|---|---|---|
| `app-icon.svg` | 1024 | Master app icon (vector) |
| `app-icon-1024.png` | 1024 | App Store / high-res |
| `app-icon-1024-master.png` | 1024 | Alternate 1024 master raster |
| `app-icon-512.png` | 512 | General app / PWA |
| `app-icon-192.png` | 192 | Android / PWA |
| `app-icon-180.png` | 180 | Apple touch icon |
| `favicon-32.svg` | 32 | Favicon (color) |
| `favicon-16.svg` | 16 | Favicon (one-ink) |
| `favicon-16.png` | 16 | Favicon raster |

## Lockups (`lockups/`)

| File | Use |
|---|---|
| `lockup-horizontal-on-dark.svg` | Æ \| Aether on dark / ink backgrounds |
| `lockup-horizontal-on-bone.svg` | Æ \| Aether on bone / light backgrounds |
| `lockup-stacked.svg` | Æ over AETHER |

## Palette

| Token | Approx hex (from cuts) | OKLCH (from sheet) | Role |
|---|---|---|---|
| ink | `#1e1a16` | `oklch(0.22 0.01 60)` | Background / one-ink glyph |
| bone | `#f2eee6` | `oklch(0.95 0.012 85)` | Glyph on dark |
| signal | `#c74d38` | `oklch(0.58 0.16 32)` | Middle bar (color mark only) |

## Rules

- Æ is a five-part grid: two diagonals + three bars; diagonals share one apex bar.
- At 16px, thicken bars and drop signal to full ink.
- Æ doubles as the currency symbol (e.g. Æ248.10).
- Prefer assets from this folder over redraws.

## Brand usage and trademarks

The Aether name, Æ ligature mark, wordmarks, lockups, icons, and palette in
this folder are trademarks and brand assets of the Aether project (see the
copyright holder in the root `LICENSE`).

**They are not licensed under the MIT License** that covers the software in
this repository. Keeping the code open does not grant a right to brand a fork
or alternate network as Aether.

### Allowed without asking

- Using these assets when operating or documenting the **canonical** Aether
  networks and official endpoints published by this project (today:
  `aether-testnet-1`, seed / RPC / faucet / explorer listed in the root README).
- Fair reference in articles, reviews, integration guides, or agent prompts
  that clearly identify this project, with attribution and a link to
  https://github.com/whoyoujoshin/aether.

### Not allowed without written permission

- Using the Aether name, Æ mark, or these assets to brand a fork, alternate
  chain, wallet, explorer, MCP server, or service that is not the canonical
  network.
- Implying endorsement, partnership, or that a third-party deployment *is*
  Aether.
- Altering the mark (new colors, proportions, or invented Æ variants) and
  presenting it as official.

When in doubt, open a GitHub issue before shipping branded materials.
