# Aether Pay Desktop

An installable desktop wrapper around the existing wallet UI
(`web/aether-pay-desktop.html`) and its backend (`cmd/walletapi`). The
Electron shell here adds no wallet logic of its own -- it just spawns
the real `walletapi` Go binary as a child process and opens the same
HTML file a developer would otherwise run with `go run ./cmd/walletapi`
and open by hand. Keys never leave the machine `walletapi` runs on.

`walletapi` signs with those keys, so it only answers requests carrying
a secret token: the desktop app makes a fresh one per launch and gives
it only to the backend and the wallet window. Run by hand, `walletapi`
prints the page URL to open, token included. Without the token, any web
page open in a browser could ask the local API to spend.

The **Agents** tab gives an agent (any account) a chain-enforced
allowance: a spend limit, an expiry, optionally only certain recipients,
and optionally its transaction fees paid. It lists what each agent may
still spend, and revokes with one click.

## Install on Windows

Download `Aether-Pay-Setup-<version>.exe` from either:

- a [GitHub release](https://github.com/whoyoujoshin/aether/releases): every
  `v*` tag attaches the installer (`.github/workflows/release.yml`), or
- the **Desktop installer** workflow in the repo's Actions tab: it runs
  whenever the wallet changes, and "Run workflow" builds one on demand.
  The installer is inside the run's `aether-pay-windows` artifact (a zip;
  needs a GitHub login, kept 90 days).

Run it. It installs for your user only (no admin prompt) and adds a
Start menu entry. It isn't code-signed yet, so SmartScreen shows
"Windows protected your PC" the first time: choose **More info**, then
**Run anyway**.

The app talks to the public testnet (`aether-testnet-1`) over TLS, so no
local node is needed. It keeps its own keys in `%APPDATA%\Aether Pay\keyring`
(macOS: `~/Library/Application Support/Aether Pay/keyring`), so it lists
only the accounts you set up in it, never every key the command-line
tools made in `~/.aether`. The first launch offers to create an account
(with a 24-word recovery phrase), recover one from its phrase, or bring
over one key from `~/.aether`; the CLI keeps its copy. The account menu
also lists the agents you've given an allowance to, read-only. It listens
on `localhost:8090`: quit any `walletapi` you started by hand first.

## Run in dev mode

From this directory:

```
npm install
npm run build-backend    # cross-compiles walletapi into resources/<os>-<arch>/
npm start
```

`build-backend` builds all four targets (win-x64, mac-arm64, mac-x64,
linux-x64) by default. Pass specific targets to build fewer, e.g.:

```
node scripts/build-backend.js linux-x64
```

## Package an installer

```
npm run dist
```

Runs `build-backend` for all targets, then `electron-builder`, which
packages for whichever platform you're building on (cross-building a
real installer for a *different* OS than the host, e.g. producing a
signed .exe from Linux, is unreliable for anything beyond the NSIS
installer itself -- code signing and some platform-specific
installer steps need to happen on that OS). Output lands in `dist/`.

## Icon

`build/icon.png` (1024x1024 master), `build/icon.ico` (Windows), and
`build/icon.icns` (macOS) are generated from the Aether "Æ" mark.
`package.json`'s `build.win.icon` / `build.mac.icon` /
`build.linux.icon` reference them, and `main.js` sets the same PNG as
the `BrowserWindow`'s icon for dev-mode/Linux (Windows and macOS pick
their icon up from the packaged executable/app bundle instead). To
regenerate from a new source image, see
`desktop/scripts/build-backend.js`'s sibling icon-generation approach
(crop to the mark's bounding box, pad to a square, export at 1024px,
then re-save as `.ico`/`.icns`) -- there's no checked-in script for
this since it only needs to run once per logo change.

## Known gaps / next steps

- Not code-signed. Unsigned installers will trigger OS security
  warnings (Windows SmartScreen, macOS Gatekeeper) on first run --
  expected for a devnet-stage app, but something to revisit before
  wider distribution.
- The wallet's default keyring backend (`test`) stores keys unencrypted
  on disk -- fine for the current testnet, not something to ship
  as-is for real value. See `cmd/walletapi`'s `-keyring-backend` flag.
