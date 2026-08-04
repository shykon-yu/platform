const { app, BrowserWindow, Menu, dialog, ipcMain } = require('electron')
const { spawn } = require('node:child_process')
const fs = require('node:fs')
const path = require('node:path')
const { version: appVersion } = require('../package.json')

const DEFAULT_NIC = 'VPN'
const DEFAULT_VPN_HOST = 'www.jingzhu.top'
const DEFAULT_VPN_PORT = 992
const API_URL = process.env.VITE_API_BASE_URL || 'http://www.jingzhu.top:8082/api/v1'

function createWindow() {
  const window = new BrowserWindow({
    width: 1180,
    height: 760,
    minWidth: 900,
    minHeight: 620,
    title: `WEL职业联盟对战平台 v${appVersion}`,
    backgroundColor: '#f4f7f6',
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  })

  window.loadFile(path.join(__dirname, '..', 'dist', 'index.html'))
}

function createChineseMenu() {
  const template = [
    {
      label: '文件',
      submenu: [
        { role: 'reload', label: '重新载入' },
        { role: 'forceReload', label: '强制重新载入' },
        { type: 'separator' },
        { role: 'quit', label: '退出' },
      ],
    },
    {
      label: '编辑',
      submenu: [
        { role: 'undo', label: '撤销' },
        { role: 'redo', label: '重做' },
        { type: 'separator' },
        { role: 'cut', label: '剪切' },
        { role: 'copy', label: '复制' },
        { role: 'paste', label: '粘贴' },
        { role: 'selectAll', label: '全选' },
      ],
    },
    {
      label: '查看',
      submenu: [
        { role: 'resetZoom', label: '实际大小' },
        { role: 'zoomIn', label: '放大' },
        { role: 'zoomOut', label: '缩小' },
        { type: 'separator' },
        { role: 'togglefullscreen', label: '全屏' },
      ],
    },
    {
      label: '窗口',
      submenu: [
        { role: 'minimize', label: '最小化' },
        { role: 'close', label: '关闭' },
      ],
    },
    {
      label: '帮助',
      submenu: [
        {
          label: '关于 WEL职业联盟对战平台',
          click: () => dialog.showMessageBox({
            type: 'info',
            title: '关于',
            message: `WEL职业联盟对战平台 v${appVersion}`,
          }),
        },
      ],
    },
  ]
  Menu.setApplicationMenu(Menu.buildFromTemplate(template))
}

function locateVpncmd() {
  const candidates = [
    'C:\\Program Files\\WEL\\SoftEther\\vpncmd_x64.exe',
    'C:\\Program Files\\SoftEther VPN Client\\vpncmd.exe',
    'C:\\Program Files (x86)\\SoftEther VPN Client\\vpncmd.exe',
  ]
  const existing = candidates.find((candidate) => fs.existsSync(candidate))
  return existing || 'vpncmd.exe'
}

function locateInstalledVpncmd() {
  const vpncmd = locateVpncmd()
  return vpncmd === 'vpncmd.exe' ? null : vpncmd
}

function isRunningAsAdmin() {
  return new Promise((resolve) => {
    if (process.platform !== 'win32') return resolve(false)
    const child = spawn('cmd.exe', ['/C', 'net', 'session'], { windowsHide: true })
    child.on('close', (code) => resolve(code === 0))
    child.on('error', () => resolve(false))
  })
}

function querySoftEtherService() {
  return new Promise((resolve) => {
    if (process.platform !== 'win32') return resolve({ installed: false, running: false })
    const child = spawn('cmd.exe', ['/C', 'sc', 'query', 'SEVPNCLIENT'], { windowsHide: true })
    let output = ''
    child.stdout.on('data', (chunk) => { output += chunk.toString() })
    child.stderr.on('data', (chunk) => { output += chunk.toString() })
    child.on('error', () => resolve({ installed: false, running: false }))
    child.on('close', (code) => {
      const installed = code === 0
      const running = installed && /RUNNING/i.test(output)
      resolve({ installed, running })
    })
  })
}

async function desktopStatus() {
  const vpncmdPath = locateInstalledVpncmd()
  const softetherInstalled = Boolean(vpncmdPath)
  const service = await querySoftEtherService()
  const admin = await isRunningAsAdmin()
  const systemVersion = process.platform === 'win32' ? process.getSystemVersion() : ''
  const isWindows7 = process.platform === 'win32' && systemVersion.startsWith('6.1')
  const ready = softetherInstalled && service.running
  const message = ready
    ? '联机环境已准备好'
    : isWindows7
      ? '检测到 Win7，必须先安装 SoftEther VPN Client 虚拟网卡组件，安装完成后重新打开平台。'
      : softetherInstalled
        ? 'SoftEther 文件已安装，但后台服务未运行。请重新运行完整安装包并同意管理员授权。'
        : '未检测到 SoftEther VPN Client。请使用完整安装包安装，拒绝管理员授权将无法联机。'
  return { admin, softetherInstalled, vpncmdPath, serviceInstalled: service.installed, serviceRunning: service.running, systemVersion, isWindows7, ready, message }
}

async function prepareDesktop() {
  const status = await desktopStatus()
  if (!status.ready) {
    throw new Error(status.message || '联机环境未准备好，请重新运行完整安装包并同意管理员授权。')
  }
  return status
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

function apiHostname() {
  try {
    const hostname = new URL(API_URL).hostname
    return isLocalOrPlaceholderHost(hostname) ? DEFAULT_VPN_HOST : hostname
  } catch {
    return DEFAULT_VPN_HOST
  }
}

function isLocalOrPlaceholderHost(host) {
  const normalized = String(host || '').trim().toLowerCase()
  return !normalized
    || normalized === 'localhost'
    || normalized === '127.0.0.1'
    || normalized === '0.0.0.0'
    || normalized === '::1'
    || normalized === 'pending-softether-host'
}

function resolveVpnServer(lease) {
  const host = isLocalOrPlaceholderHost(lease.host) ? apiHostname() : String(lease.host).trim()
  const rawPort = Number(lease.port)
  const port = Number.isInteger(rawPort) && rawPort > 0 && rawPort < 65536 && rawPort !== 443
    ? rawPort
    : DEFAULT_VPN_PORT
  return `${host}:${port}`
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
  await prepareDesktop()
  const nic = await resolveNic(lease.nicName)
  const account = `WEL-${lease.username}`
  const server = resolveVpnServer(lease)
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountDelete', account]).catch(() => {})
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountCreate', account, `/SERVER:${server}`, `/HUB:${lease.hub}`, `/USERNAME:${lease.username}`, `/NICNAME:${nic}`])
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountPasswordSet', account, `/PASSWORD:${lease.password}`, '/TYPE:standard'])
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountConnect', account])
})

ipcMain.handle('desktop-status', () => desktopStatus())

ipcMain.handle('prepare-desktop', () => prepareDesktop())

ipcMain.handle('disconnect-vpn', async (_event, username) => {
  validateIdentifier(username, 'VPN 用户名')
  const account = `WEL-${username}`
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountDisconnect', account]).catch(() => {})
  await runVpncmd(['localhost', '/CLIENT', '/CMD', 'AccountDelete', account]).catch(() => {})
})

ipcMain.handle('launch-game', async (_event, gamePath) => {
  if (!gamePath || !gamePath.trim()) throw new Error('请先选择 WE8 游戏程序')

  const normalizedPath = path.normalize(gamePath.trim().replace(/^"(.*)"$/, '$1'))
  if (!fs.existsSync(normalizedPath)) {
    throw new Error(`找不到 WE8 游戏程序：${normalizedPath}`)
  }

  const stat = fs.statSync(normalizedPath)
  if (!stat.isFile()) {
    throw new Error('选择的 WE8 路径不是可执行文件')
  }

  if (process.platform === 'win32') {
    // Shell launch avoids EACCES from direct spawn on some Windows 11
    // installations and preserves support for non-ASCII game paths.
    return new Promise((resolve, reject) => {
      const child = spawn(
        'cmd.exe',
        ['/d', '/c', 'start', '""', normalizedPath],
        { detached: true, windowsHide: true, stdio: 'ignore' },
      )
      child.once('error', (error) => reject(new Error(`无法启动 WE8：${error.message}`)))
      child.once('spawn', () => {
        child.unref()
        resolve()
      })
    })
  }

  return new Promise((resolve, reject) => {
    const child = spawn(normalizedPath, [], {
      cwd: path.dirname(normalizedPath),
      detached: true,
      windowsHide: true,
      stdio: 'ignore',
    })
    child.once('error', (error) => reject(new Error(`无法启动 WE8：${error.message}`)))
    child.once('spawn', () => {
      child.unref()
      resolve()
    })
  })
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
  createChineseMenu()
  createWindow()
  app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow() })
})

app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
