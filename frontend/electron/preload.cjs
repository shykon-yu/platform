const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('we8Desktop', {
  connectVpn: (lease) => ipcRenderer.invoke('connect-vpn', lease),
  disconnectVpn: (username) => ipcRenderer.invoke('disconnect-vpn', username),
  chooseGame: () => ipcRenderer.invoke('choose-game'),
  launchGame: (gamePath) => ipcRenderer.invoke('launch-game', gamePath),
})
