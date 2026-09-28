// The only way the wallet page reaches the Electron shell: app version
// and updates. Nothing here can touch keys, files or the network.
const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("aetherApp", {
  info: () => ipcRenderer.invoke("app-info"),
  updateState: () => ipcRenderer.invoke("update-state"),
  checkForUpdates: () => ipcRenderer.invoke("update-check"),
  downloadUpdate: () => ipcRenderer.invoke("update-download"),
  installUpdate: () => ipcRenderer.invoke("update-install"),
  onUpdateState: (cb) => {
    const listener = (_event, state) => cb(state);
    ipcRenderer.on("update-state", listener);
    return () => ipcRenderer.removeListener("update-state", listener);
  },
});
