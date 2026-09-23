# Aether Pay Desktop

An installable desktop wrapper around the existing wallet UI
(`web/aether-pay-desktop.html`) and its backend (`cmd/walletapi`). The
Electron shell here adds no wallet logic of its own -- it just spawns
the real `walletapi` Go binary as a child process and opens the same
HTML file a developer would otherwise run with `go run ./cmd/walletapi`
and open by hand. Keys never leave the machine `walletapi` runs on.

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

## Known gaps / next steps

- No app icon yet -- electron-builder falls back to its default. Drop
  a real icon into `build/` and reference it under `build.win.icon`
  / `build.mac.icon` / `build.linux.icon` in `package.json` when one
  exists.
- Not code-signed. Unsigned installers will trigger OS security
  warnings (Windows SmartScreen, macOS Gatekeeper) on first run --
  expected for a devnet-stage app, but something to revisit before
  wider distribution.
- The wallet's default keyring backend (`test`) stores keys unencrypted
  on disk -- fine for the current testnet, not something to ship
  as-is for real value. See `cmd/walletapi`'s `-keyring-backend` flag.
