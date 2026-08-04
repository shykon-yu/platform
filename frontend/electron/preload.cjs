const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('we8Desktop', {
  connectVpn: (lease) => ipcRenderer.invoke('connect-vpn', lease),
  ensureSoftEther: () => ipcRenderer.invoke('ensure-softether'),
  ensureFirewallRule: () => ipcRenderer.invoke('ensure-firewall-rule'),
  disconnectVpn: (username) => ipcRenderer.invoke('disconnect-vpn', username),
  chooseGame: () => ipcRenderer.invoke('choose-game'),
  launchGame: (gamePath) => ipcRenderer.invoke('launch-game', gamePath),
})
