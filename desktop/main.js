// Electron shell for Aether Pay Desktop. Deliberately thin: all real
// wallet logic (keyring, signing, chain queries) lives in the
// cmd/walletapi Go binary, spawned here as a child process exactly the
// way a user running it by hand would. This window loads
// web/aether-pay-desktop.html and points it at that local process
// (http://localhost:8090). The shell adds only what a page can't do
// itself: open links in the browser, and update the app.
const { app, BrowserWindow, shell, ipcMain } = require("electron");
const { autoUpdater } = require("electron-updater");
const { spawn } = require("child_process");
const path = require("path");
const os = require("os");
const http = require("http");
const crypto = require("crypto");

const PORT = 8090;
const EXPLORER_URL = "https://explorer.157-245-252-221.sslip.io";
const RELEASES_URL = "https://github.com/whoyoujoshin/aether/releases";
const HEALTH_URL = `http://localhost:${PORT}/api/accounts`;
const UPDATE_CHECK_EVERY_MS = 6 * 60 * 60 * 1000;

// A fresh secret per launch, known only to walletapi and this window:
// walletapi signs with this machine's keys, and without it any web page
// open in a browser could ask it to spend (see cmd/walletapi withCORS).
// Passed through the environment, not the command line, which other
// local users can read.
const API_TOKEN = crypto.randomBytes(32).toString("hex");

let backendProcess = null;
let mainWindow = null;

function backendBinaryPath() {
  const osKey = { win32: "win", darwin: "mac", linux: "linux" }[process.platform];
  const archKey = { x64: "x64", arm64: "arm64" }[process.arch];
  const binName = process.platform === "win32" ? "walletapi.exe" : "walletapi";

  if (!osKey || !archKey) {
    throw new Error(`Unsupported platform/arch: ${process.platform}/${process.arch}`);
  }

  // Packaged app: electron-builder copies resources/<os>-<arch>/* into
  // "backend" under process.resourcesPath (see package.json's "build"
  // config). Dev mode: read straight from the resources/ dir this
  // repo's build-backend.js script populates.
  if (app.isPackaged) {
    return path.join(process.resourcesPath, "backend", binName);
  }
  return path.join(__dirname, "resources", `${osKey}-${archKey}`, binName);
}

// The wallet keeps its own keyring (e.g. %APPDATA%\Aether Pay\keyring),
// so it lists only accounts its user created or chose to bring over,
// never every key the CLI tools ever made in ~/.aether.
function backendArgs() {
  return [
    "--keyring-dir", path.join(app.getPath("userData"), "keyring"),
    "--legacy-keyring-dir", path.join(os.homedir(), ".aether"),
  ];
}

// The USDC the wallet shows and sends on the public testnet: Circle's
// USDC on Injective's testnet, through Osmosis (wallet.TestnetUSDC in Go;
// see docs/OSMOSIS-TESTNET.md). Any other token shows by its bare denom,
// never as USDC. A USDC setting already in the environment wins.
function usdcEnv() {
  const e = process.env;
  if (e.AETHER_USDC_CHANNEL || e.AETHER_USDC_PATH) return {};
  return {
    AETHER_USDC_PATH: "transfer/channel-1/transfer/channel-10092",
    AETHER_USDC_BASE_DENOM: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d",
    AETHER_USDC_ISSUER: "Injective",
  };
}

function startBackend() {
  const binPath = backendBinaryPath();
  backendProcess = spawn(binPath, backendArgs(), {
    stdio: ["ignore", "pipe", "pipe"],
    env: Object.assign({}, process.env, usdcEnv(), { AETHER_WALLET_TOKEN: API_TOKEN }),
  });

  backendProcess.stdout.on("data", (d) => process.stdout.write(`[walletapi] ${d}`));
  backendProcess.stderr.on("data", (d) => process.stderr.write(`[walletapi] ${d}`));
  backendProcess.on("error", (err) => {
    console.error("Failed to start walletapi backend:", err);
  });
  backendProcess.on("exit", (code) => {
    console.log(`walletapi backend exited with code ${code}`);
    backendProcess = null;
  });
}

function stopBackend() {
  if (backendProcess) {
    backendProcess.kill();
    backendProcess = null;
  }
}

function waitForBackend(timeoutMs = 10000, intervalMs = 200) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve) => {
    const attempt = () => {
      const req = http.get(HEALTH_URL, { headers: { "X-Wallet-Token": API_TOKEN } }, (res) => {
        res.resume();
        resolve(true);
      });
      req.on("error", () => {
        if (Date.now() >= deadline) {
          resolve(false);
          return;
        }
        setTimeout(attempt, intervalMs);
      });
    };
    attempt();
  });
}

// --- Updates ---
//
// From this repo's GitHub releases (package.json build.publish), which
// carry the installer, its .blockmap and the update-info .yml: an
// update downloads only the blocks that changed when it can, not the
// whole installer again. Every release is a "-testnet" prerelease, so
// prereleases are allowed. Nothing downloads or installs until the
// user asks; only the packaged Windows app updates (the only platform
// whose installer the releases carry).
const updatable = app.isPackaged && process.platform === "win32";
let updateState = { state: updatable ? "idle" : "unsupported" };

function setUpdateState(s) {
  updateState = s;
  if (mainWindow) mainWindow.webContents.send("update-state", s);
}

function setupUpdates() {
  ipcMain.handle("app-info", () => ({
    version: app.getVersion(),
    platform: process.platform,
    updatable,
    releasesUrl: RELEASES_URL,
  }));
  ipcMain.handle("update-state", () => updateState);
  ipcMain.handle("update-check", () => checkForUpdates());
  ipcMain.handle("update-download", async () => {
    if (!updatable || updateState.state !== "available") return updateState;
    try {
      await autoUpdater.downloadUpdate();
    } catch (err) {
      setUpdateState({ state: "error", message: err.message });
    }
    return updateState;
  });
  ipcMain.handle("update-install", () => {
    if (updateState.state !== "ready") return updateState;
    stopBackend();
    // Silent install, then relaunch: the installer is per-user, no prompt.
    autoUpdater.quitAndInstall(true, true);
    return updateState;
  });

  if (!updatable) return;
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = false;
  autoUpdater.allowPrerelease = true;
  autoUpdater.on("checking-for-update", () => setUpdateState({ state: "checking" }));
  autoUpdater.on("update-not-available", () => setUpdateState({ state: "current", checkedAt: Date.now() }));
  autoUpdater.on("update-available", (info) => setUpdateState({
    state: "available",
    version: info.version,
    releaseDate: info.releaseDate,
    size: (info.files || []).reduce((n, f) => n + (f.size || 0), 0),
  }));
  autoUpdater.on("download-progress", (p) => setUpdateState({
    state: "downloading", version: updateState.version, percent: p.percent, transferred: p.transferred, total: p.total,
  }));
  autoUpdater.on("update-downloaded", (info) => setUpdateState({ state: "ready", version: info.version }));
  autoUpdater.on("error", (err) => setUpdateState({ state: "error", message: err ? err.message : "update failed" }));
}

async function checkForUpdates() {
  if (!updatable || ["checking", "downloading", "ready"].includes(updateState.state)) return updateState;
  try {
    await autoUpdater.checkForUpdates();
  } catch (err) {
    setUpdateState({ state: "error", message: err.message });
  }
  return updateState;
}

async function createWindow() {
  await waitForBackend();

  mainWindow = new BrowserWindow({
    width: 1280,
    height: 860,
    minWidth: 720,
    minHeight: 560,
    title: "Aether Pay",
    icon: path.join(__dirname, "build", "icon.png"),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      preload: path.join(__dirname, "preload.js"),
    },
  });

  // The same page a developer opens by hand against `go run
  // ./cmd/walletapi`; it talks to walletapi at http://localhost:8090.
  mainWindow.loadFile(path.join(__dirname, "..", "web", "aether-pay-desktop.html"), { query: { token: API_TOKEN } });

  // Explorer and release links open in the user's browser; the wallet
  // window itself never navigates away (it holds the API token).
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith(EXPLORER_URL + "/") || url.startsWith(RELEASES_URL)) shell.openExternal(url);
    return { action: "deny" };
  });
  mainWindow.webContents.on("will-navigate", (event) => event.preventDefault());
  mainWindow.on("closed", () => {
    mainWindow = null;
  });
}

app.whenReady().then(async () => {
  setupUpdates();
  startBackend();
  await createWindow();
  if (updatable) {
    setTimeout(checkForUpdates, 5000);
    setInterval(checkForUpdates, UPDATE_CHECK_EVERY_MS);
  }

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", stopBackend);
