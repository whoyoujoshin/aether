// The only way the wallet page reaches the Electron shell: app version,
// updates, and which network the app uses. Nothing here can touch keys.
const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("aetherApp", {
  info: () => ipcRenderer.invoke("app-info"),
  updateState: () => ipcRenderer.invoke("update-state"),
  checkForUpdates: () => ipcRenderer.invoke("update-check"),
  downloadUpdate: () => ipcRenderer.invoke("update-download"),
  installUpdate: () => ipcRenderer.invoke("update-install"),
  network: () => ipcRenderer.invoke("network-get"),
  setNetwork: (s) => ipcRenderer.invoke("network-set", s),
  onUpdateState: (cb) => {
    const listener = (_event, state) => cb(state);
    ipcRenderer.on("update-state", listener);
    return () => ipcRenderer.removeListener("update-state", listener);
  },
});
