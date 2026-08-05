const os = require('node:os')
const { spawn } = require('node:child_process')

const CONFLICTING_ADAPTER_PATTERN = /tap-windows|tap adapter|openvpn|zerotier|radmin vpn|hamachi|softether vpn client adapter/i

function ipv4ToNumber(value) {
  const parts = String(value || '').split('.').map(Number)
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return null
  return parts.reduce((result, part) => ((result << 8) | part) >>> 0, 0)
}

function isIPv4InCIDR(address, cidr) {
  const [network, prefixText] = String(cidr || '').split('/')
  const addressNumber = ipv4ToNumber(address)
  const networkNumber = ipv4ToNumber(network)
  const prefix = Number(prefixText)
  if (addressNumber === null || networkNumber === null || !Number.isInteger(prefix) || prefix < 0 || prefix > 32) return false
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
  return (addressNumber & mask) === (networkNumber & mask)
}

function findRoomAddress(cidr, interfaces = os.networkInterfaces()) {
  for (const [name, addresses] of Object.entries(interfaces)) {
    for (const address of addresses || []) {
      const isIPv4 = address.family === 'IPv4' || address.family === 4
      if (isIPv4 && !address.internal && isIPv4InCIDR(address.address, cidr)) {
        return { name, address: address.address }
      }
    }
  }
  return null
}

function decodeField(value) {
  if (!value) return ''
  try {
    return Buffer.from(value, 'base64').toString('utf8')
  } catch {
    return ''
  }
}

function parseAdapterOutput(output) {
  return String(output || '').split(/\r?\n/).map((line) => {
    const fields = line.trim().split('|')
    if (fields.length !== 6) return null
    return {
      description: decodeField(fields[0]),
      ipEnabled: fields[1].toLowerCase() === 'true',
      ipAddresses: decodeField(fields[2]).split(',').filter(Boolean),
      subnets: decodeField(fields[3]).split(',').filter(Boolean),
      defaultGateways: decodeField(fields[4]).split(',').filter(Boolean),
      dnsServers: decodeField(fields[5]).split(',').filter(Boolean),
    }
  }).filter(Boolean)
}

function runPowerShell(script, timeoutMs = 8000) {
  return new Promise((resolve, reject) => {
    const encoded = Buffer.from(script, 'utf16le').toString('base64')
    const child = spawn('powershell.exe', ['-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', encoded], {
      windowsHide: true,
    })
    let stdout = ''
    let stderr = ''
    const timer = setTimeout(() => {
      child.kill()
      reject(new Error('读取 Windows 网卡配置超时'))
    }, timeoutMs)
    child.stdout.on('data', (chunk) => { stdout += chunk.toString() })
    child.stderr.on('data', (chunk) => { stderr += chunk.toString() })
    child.once('error', (error) => {
      clearTimeout(timer)
      reject(error)
    })
    child.once('close', (code) => {
      clearTimeout(timer)
      if (code === 0) resolve(stdout)
      else reject(new Error(stderr.trim() || `PowerShell 退出代码 ${code}`))
    })
  })
}

async function queryWindowsAdapters() {
  if (process.platform !== 'win32') return []
  const script = `
function Encode-Value($value) {
  if ($null -eq $value) { return '' }
  return [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$value))
}
Get-WmiObject Win32_NetworkAdapterConfiguration | ForEach-Object {
  $fields = @(
    (Encode-Value $_.Description),
    ([string]$_.IPEnabled),
    (Encode-Value ($_.IPAddress -join ',')),
    (Encode-Value ($_.IPSubnet -join ',')),
    (Encode-Value ($_.DefaultIPGateway -join ',')),
    (Encode-Value ($_.DNSServerSearchOrder -join ','))
  )
  [Console]::Out.WriteLine($fields -join '|')
}`
  return parseAdapterOutput(await runPowerShell(script))
}

function analyzeNetwork(cidr, roomAddress, adapters) {
  const actualIp = roomAddress?.address || null
  const roomAdapter = actualIp
    ? adapters.find((adapter) => adapter.ipAddresses.includes(actualIp)) || null
    : null
  const conflictingAdapters = adapters
    .filter((adapter) => adapter.ipEnabled && adapter !== roomAdapter && CONFLICTING_ADAPTER_PATTERN.test(adapter.description))
    .map((adapter) => adapter.description)
  const warnings = []

  if (!actualIp) warnings.push(`尚未获取房间网段 ${cidr} 的虚拟 IP`)
  if (roomAdapter?.defaultGateways.length) warnings.push(`VPN 网卡仍存在默认网关：${roomAdapter.defaultGateways.join(', ')}`)
  if (roomAdapter?.dnsServers.length) warnings.push(`VPN 网卡仍存在 DNS：${roomAdapter.dnsServers.join(', ')}`)
  if (conflictingAdapters.length) warnings.push(`检测到其他已启用虚拟网卡：${conflictingAdapters.join('、')}`)

  return {
    connected: Boolean(actualIp),
    actualIp,
    subnetCidr: cidr,
    adapterName: roomAddress?.name || null,
    adapterDescription: roomAdapter?.description || roomAddress?.name || null,
    defaultGateways: roomAdapter?.defaultGateways || [],
    dnsServers: roomAdapter?.dnsServers || [],
    conflictingAdapters,
    warnings,
  }
}

async function inspectVpnNetwork(cidr) {
  const roomAddress = findRoomAddress(cidr)
  let adapters = []
  try {
    adapters = await queryWindowsAdapters()
  } catch {
    // The actual room IP is still reliable when WMI is unavailable.
  }
  return analyzeNetwork(cidr, roomAddress, adapters)
}

async function waitForVpnNetwork(cidr, timeoutMs = 30000) {
  const startedAt = Date.now()
  while (Date.now() - startedAt < timeoutMs) {
    const roomAddress = findRoomAddress(cidr)
    if (roomAddress) return inspectVpnNetwork(cidr)
    await new Promise((resolve) => setTimeout(resolve, 750))
  }
  return inspectVpnNetwork(cidr)
}

module.exports = {
  analyzeNetwork,
  findRoomAddress,
  inspectVpnNetwork,
  isIPv4InCIDR,
  parseAdapterOutput,
  runPowerShell,
  waitForVpnNetwork,
}
