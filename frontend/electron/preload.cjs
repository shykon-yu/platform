const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('we8Desktop', {
  connectVpn: (lease) => ipcRenderer.invoke('connect-vpn', lease),
  restoreVpn: (lease) => ipcRenderer.invoke('restore-vpn', lease),
  inspectVpn: (lease) => ipcRenderer.invoke('inspect-vpn', lease),
  copyVpnDiagnostics: (lease) => ipcRenderer.invoke('copy-vpn-diagnostics', lease),
  configureGameFirewall: (gamePath) => ipcRenderer.invoke('configure-game-firewall', gamePath),
  desktopStatus: () => ipcRenderer.invoke('desktop-status'),
  prepareDesktop: () => ipcRenderer.invoke('prepare-desktop'),
  disconnectVpn: (username) => ipcRenderer.invoke('disconnect-vpn', username),
  chooseGame: () => ipcRenderer.invoke('choose-game'),
  launchGame: (gamePath) => ipcRenderer.invoke('launch-game', gamePath),
})
