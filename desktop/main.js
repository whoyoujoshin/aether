// Electron shell for Aether Pay Desktop. Deliberately thin: all real
// wallet logic (keyring, signing, chain queries) lives in the existing
// cmd/walletapi Go binary, spawned here as a child process exactly the
// way a user running it by hand would. This window just loads the
// existing, already live-verified web/aether-pay-desktop.html
// unmodified and points it at that local process, the same
// http://localhost:8090 it already expects.
const { app, BrowserWindow, shell } = require("electron");
const { spawn } = require("child_process");
const path = require("path");
const os = require("os");
const http = require("http");
const crypto = require("crypto");

const PORT = 8090;
const EXPLORER_URL = "https://explorer.157-245-252-221.sslip.io";
const HEALTH_URL = `http://localhost:${PORT}/api/accounts`;

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

function startBackend() {
  const binPath = backendBinaryPath();
  backendProcess = spawn(binPath, backendArgs(), {
    stdio: ["ignore", "pipe", "pipe"],
    env: Object.assign({}, process.env, { AETHER_WALLET_TOKEN: API_TOKEN }),
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
    },
  });

  // The same page a developer opens by hand against `go run
  // ./cmd/walletapi`; it talks to walletapi at http://localhost:8090.
  mainWindow.loadFile(path.join(__dirname, "..", "web", "aether-pay-desktop.html"), { query: { token: API_TOKEN } });

  // Explorer links open in the user's browser; the wallet window itself
  // never navigates away from the wallet (it holds the API token).
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith(EXPLORER_URL + "/")) shell.openExternal(url);
    return { action: "deny" };
  });
  mainWindow.webContents.on("will-navigate", (event) => event.preventDefault());
  mainWindow.on("closed", () => {
    mainWindow = null;
  });
}

app.whenReady().then(() => {
  startBackend();
  createWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", () => {
  if (backendProcess) {
    backendProcess.kill();
    backendProcess = null;
  }
});
