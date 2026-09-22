// The page's only door to the outside: one call, answered by the main process.
const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('journal', { read: () => ipcRenderer.invoke('journal') });
