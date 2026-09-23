#!/usr/bin/env node
// Cross-compiles the real cmd/walletapi Go binary for one or more
// desktop targets, into resources/<os>-<arch>/walletapi[.exe] --
// matching electron-builder's ${os}/${arch} extraResources macros
// exactly, so `electron-builder` can pick the right binary per target
// without any per-platform config duplication.
//
// Pure Go, CGO_ENABLED=0: the wallet's default "test" keyring backend
// has no native/OS-keychain dependency, so this cross-compiles cleanly
// from any host with no C toolchain required.

const { execFileSync } = require("child_process");
const path = require("path");
const fs = require("fs");

const REPO_ROOT = path.resolve(__dirname, "..", "..");
const OUT_ROOT = path.resolve(__dirname, "..", "resources");

// electron-builder's os/arch macro values -> Go's GOOS/GOARCH.
const TARGETS = [
  { os: "win", arch: "x64", GOOS: "windows", GOARCH: "amd64", bin: "walletapi.exe" },
  { os: "mac", arch: "arm64", GOOS: "darwin", GOARCH: "arm64", bin: "walletapi" },
  { os: "mac", arch: "x64", GOOS: "darwin", GOARCH: "amd64", bin: "walletapi" },
  { os: "linux", arch: "x64", GOOS: "linux", GOARCH: "amd64", bin: "walletapi" },
];

const requested = process.argv.slice(2);
const targets = requested.length
  ? TARGETS.filter((t) => requested.includes(`${t.os}-${t.arch}`))
  : TARGETS;

if (!targets.length) {
  console.error(
    `No matching build targets for: ${requested.join(", ")}\n` +
      `Available: ${TARGETS.map((t) => `${t.os}-${t.arch}`).join(", ")}`
  );
  process.exit(1);
}

for (const t of targets) {
  const outDir = path.join(OUT_ROOT, `${t.os}-${t.arch}`);
  fs.mkdirSync(outDir, { recursive: true });
  const outFile = path.join(outDir, t.bin);

  console.log(`Building walletapi for ${t.os}-${t.arch} -> ${outFile}`);
  execFileSync(
    "go",
    ["build", "-trimpath", "-o", outFile, "./cmd/walletapi"],
    {
      cwd: REPO_ROOT,
      stdio: "inherit",
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: t.GOOS,
        GOARCH: t.GOARCH,
      },
    }
  );
}
