const { app, BrowserWindow, dialog, ipcMain } = require('electron')
const { spawn } = require('node:child_process')
const fs = require('node:fs')
const path = require('node:path')

const DEFAULT_NIC = 'VPN'
const API_URL = process.env.VITE_API_BASE_URL || 'http://www.jingzhu.top:8082/api/v1'

function createWindow() {
  const window = new BrowserWindow({
    width: 1180,
    height: 760,
    minWidth: 900,
    minHeight: 620,
    title: 'WEL职业联盟对战平台',
    backgroundColor: '#f4f7f6',
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  })

  window.loadFile(path.join(__dirname, '..', 'dist', 'index.html'))
}

function locateVpncmd() {
  const candidates = [
    'C:\\Program Files\\SoftEther VPN Client\\vpncmd.exe',
    'C:\\Program Files (x86)\\SoftEther VPN Client\\vpncmd.exe',
  ]
  const existing = candidates.find((candidate) => fs.existsSync(candidate))
  return existing || 'vpncmd.exe'
}

function runVpncmd(args) {
  return new Promise((resolve, reject) => {
    const child = spawn(locateVpncmd(), args, { windowsHide: true, windowsVerbatimArguments: false })
    let stdout = ''
    let stderr = ''
    child.stdout.on('data', (chunk) => { stdout += chunk.toString() })
    child.stderr.on('data', (chunk) => { stderr += chunk.toString() })
    child.on('error', (error) => reject(`无法启动 SoftEther 客户端：${error.message}`))
    child.on('close', (code) => {
      if (code === 0) return resolve(stdout)
      const detail = [stdout.trim(), stderr.trim()].filter(Boolean).join('\n')
      reject(`SoftEther 客户端命令执行失败：${args[args.indexOf('/CMD') + 1] || 'vpncmd'}${detail ? `\n${detail}` : ''}`)
    })
  })
}

function validateIdentifier(value, label) {
  if (!value || value.length > 96 || !/^[A-Za-z0-9._-]+$/.test(value)) {
    throw new Error(`${label}格式不正确`)
  }
}

function isNicName(value) {
  return value === DEFAULT_NIC || /^VPN([2-9]|[1-9][0-9]|1[01][0-9]|12[0-7])$/.test(value)
}

function nicExists(output, nic) {
  return output.split(/\r?\n/).some((line) => line.split('|').some((part) => part.trim().toLowerCase() === nic.toLowerCase()))
}

async function resolveNic(preferred) {
  const candidates = []
  if (preferred && isNicName(preferred.trim())) candidates.push(preferred.trim())
  candidates.push(DEFAULT_NIC, ...Array.from({ length: 126 }, (_, index) => `VPN${index + 2}`))
  const unique = [...new Set(candidates)]
  const list = await runVpncmd(['localhost', '/CLIENT', '/CMD', 'NicList'])
  const existing = unique.find((nic) => nicExists(list, nic))
  if (existing) return existing
  let lastError = ''
  for (const nic of unique) {
    try {
      await runVpncmd(['localhost', '/CLIENT', '/CMD', 'NicCreate', nic])
      return nic
    } catch (error) {
      lastError = String(error)
      if (!lastError.includes('错误代码: 32') && !lastError.includes('specified name') && !lastError.includes('指定名称')) throw error
      const refreshed = await runVpncmd(['localhost', '/CLIENT', '/CMD', 'NicList'])
      if (nicExists(refreshed, nic)) return nic
    }
  }
  throw new Error(lastError || '无法创建 SoftEther 虚拟网卡')
}

ipcMain.handle('connect-vpn', async (_event, lease) => {
  validateIdentifier(lease.hub, '虚拟 HUB')
  validateIdentifier(lease.username, 'VPN 用户名')
  if (!lease.host || !lease.password || !lease.port) throw new Error('VPN 连接参数不完整')
  const nic = await resolveNic(lease.nicName)
  const account = `WEL-${lease.username}`
  const server = `${lease.host}:${lease.port}`
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountDelete', account]).catch(() => {})
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountCreate', account, `/SERVER:${server}`, `/HUB:${lease.hub}`, `/USERNAME:${lease.username}`, `/NICNAME:${nic}`])
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountPasswordSet', account, `/PASSWORD:${lease.password}`, '/TYPE:standard'])
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountConnect', account])
})

ipcMain.handle('disconnect-vpn', async (_event, username) => {
  validateIdentifier(username, 'VPN 用户名')
  const account = `WEL-${username}`
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountDisconnect', account]).catch(() => {})
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountDelete', account]).catch(() => {})
})

ipcMain.handle('launch-game', async (_event, gamePath) => {
  if (!gamePath || !gamePath.trim()) throw new Error('请先选择 WE8 游戏程序')
  const child = spawn(gamePath, [], { detached: true, windowsHide: true, stdio: 'ignore' })
  child.unref()
})

ipcMain.handle('choose-game', async (event) => {
  const window = BrowserWindow.fromWebContents(event.sender)
  const result = await dialog.showOpenDialog(window, {
    title: '选择 WE8 游戏程序',
    properties: ['openFile'],
    filters: [
      { name: 'WE8 游戏程序', extensions: ['exe'] },
      { name: '所有文件', extensions: ['*'] },
    ],
  })
  return result.canceled ? null : result.filePaths[0] || null
})

app.whenReady().then(() => {
  process.env.VITE_API_BASE_URL = API_URL
  createWindow()
  app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow() })
})

app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
