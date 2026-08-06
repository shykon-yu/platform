const os = require('node:os')
const { spawn } = require('node:child_process')

const CONFLICTING_ADAPTER_PATTERN = /tap-windows|tap adapter|openvpn|zerotier|radmin vpn|hamachi|gateway nc adapter|softether vpn client adapter/i

function decodeProcessOutput(chunks) {
  const buffer = Buffer.concat(chunks)
  const utf8 = buffer.toString('utf8')
  if (!utf8.includes('\uFFFD')) return utf8
  try {
    return new TextDecoder('gb18030').decode(buffer)
  } catch {
    return utf8
  }
}

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
    if (fields.length !== 8) return null
    return {
      description: decodeField(fields[0]),
      ipEnabled: fields[1].toLowerCase() === 'true',
      interfaceIndex: /^\d+$/.test(fields[2]) ? Number(fields[2]) : null,
      interfaceMetric: /^\d+$/.test(fields[3]) ? Number(fields[3]) : null,
      ipAddresses: decodeField(fields[4]).split(',').filter(Boolean),
      subnets: decodeField(fields[5]).split(',').filter(Boolean),
      defaultGateways: decodeField(fields[6]).split(',').filter(Boolean),
      dnsServers: decodeField(fields[7]).split(',').filter(Boolean),
    }
  }).filter(Boolean)
}

function runPowerShell(script, timeoutMs = 8000) {
  return new Promise((resolve, reject) => {
    const utf8Script = `
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$OutputEncoding = [Text.Encoding]::UTF8
${script}`
    const encoded = Buffer.from(utf8Script, 'utf16le').toString('base64')
    const child = spawn('powershell.exe', ['-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', encoded], {
      windowsHide: true,
    })
    const stdout = []
    const stderr = []
    const timer = setTimeout(() => {
      child.kill()
      reject(new Error('读取 Windows 网卡配置超时'))
    }, timeoutMs)
    child.stdout.on('data', (chunk) => { stdout.push(chunk) })
    child.stderr.on('data', (chunk) => { stderr.push(chunk) })
    child.once('error', (error) => {
      clearTimeout(timer)
      reject(error)
    })
    child.once('close', (code) => {
      clearTimeout(timer)
      const stdoutText = decodeProcessOutput(stdout)
      const stderrText = decodeProcessOutput(stderr)
      if (code === 0) resolve(stdoutText)
      else reject(new Error(stderrText.trim() || `PowerShell 退出代码 ${code}`))
    })
  })
}

function runProcess(file, args, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const child = spawn(file, args, { windowsHide: true })
    const stdout = []
    const stderr = []
    const timer = setTimeout(() => {
      child.kill()
      reject(new Error(`执行 ${file} 超时`))
    }, timeoutMs)
    child.stdout.on('data', (chunk) => { stdout.push(chunk) })
    child.stderr.on('data', (chunk) => { stderr.push(chunk) })
    child.once('error', (error) => {
      clearTimeout(timer)
      reject(error)
    })
    child.once('close', (code) => {
      clearTimeout(timer)
      const stdoutText = decodeProcessOutput(stdout)
      const stderrText = decodeProcessOutput(stderr)
      if (code === 0) resolve(stdoutText)
      else reject(new Error(stderrText.trim() || `${file} 退出代码 ${code}`))
    })
  })
}

function parseNetshInterfaces(output) {
  return String(output || '').split(/\r?\n/).map((line) => {
    const match = line.match(/^\s*(\d+)\s+(\d+)\s+\d+\s+\S+\s+(.+?)\s*$/)
    if (!match) return null
    return { interfaceIndex: Number(match[1]), interfaceMetric: Number(match[2]), name: match[3] }
  }).filter(Boolean)
}

function parseTasklistPids(output) {
  const processNames = /^(WE8|PES8|dpnsvr)\.exe$/i
  return String(output || '').split(/\r?\n/).map((line) => {
    const match = line.match(/^"([^"]+)","(\d+)"/)
    if (!match || !processNames.test(match[1])) return null
    return { name: match[1].replace(/\.exe$/i, ''), pid: Number(match[2]) }
  }).filter(Boolean)
}

function findNetstatLines(output, processes) {
  const pidSet = new Set(processes.map(({ pid }) => String(pid)))
  return String(output || '').split(/\r?\n/).map((line) => {
    const trimmed = line.trim()
    if (!trimmed) return null
    const fields = trimmed.split(/\s+/)
    return pidSet.has(fields.at(-1)) ? trimmed : null
  }).filter(Boolean)
}

async function queryNetshInterface(name) {
  if (process.platform !== 'win32' || !name) return null
  const netsh = `${process.env.SystemRoot || 'C:\\Windows'}\\System32\\netsh.exe`
  const interfaces = parseNetshInterfaces(await runProcess(netsh, ['interface', 'ipv4', 'show', 'interfaces']))
  return interfaces.find((item) => item.name.localeCompare(name, undefined, { sensitivity: 'accent' }) === 0) || null
}

async function queryWindowsAdapters() {
  if (process.platform !== 'win32') return []
  const script = `
function Encode-Value($value) {
  if ($null -eq $value) { return '' }
  return [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$value))
}
Get-WmiObject Win32_NetworkAdapterConfiguration | ForEach-Object {
  $metric = $_.IPConnectionMetric
  try {
    $interface = Get-ItemProperty ("HKLM:\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters\\Interfaces\\" + $_.SettingID) -ErrorAction Stop
    if ($null -ne $interface.InterfaceMetric) { $metric = $interface.InterfaceMetric }
  } catch {}
  $fields = @(
    (Encode-Value $_.Description),
    ([string]$_.IPEnabled),
    ([string]$_.InterfaceIndex),
    ([string]$metric),
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
  const conflictingAdapterDetails = adapters
    .filter((adapter) => adapter.ipEnabled && adapter !== roomAdapter && CONFLICTING_ADAPTER_PATTERN.test(adapter.description))
  const conflictingAdapters = conflictingAdapterDetails.map((adapter) => adapter.description)
  const conflictingAdapterIndexes = conflictingAdapterDetails
    .filter((adapter) => adapter.interfaceMetric === null || adapter.interfaceMetric < 5000)
    .map((adapter) => adapter.interfaceIndex)
    .filter((interfaceIndex) => Number.isInteger(interfaceIndex) && interfaceIndex > 0)
  const warnings = []

  if (!actualIp) warnings.push(`尚未获取房间网段 ${cidr} 的虚拟 IP`)
  if (roomAdapter?.defaultGateways.length) warnings.push(`VPN 网卡仍存在默认网关：${roomAdapter.defaultGateways.join(', ')}`)
  if (roomAdapter?.dnsServers.length) warnings.push(`VPN 网卡仍存在 DNS：${roomAdapter.dnsServers.join(', ')}`)
  if (roomAdapter && roomAdapter.interfaceMetric !== null && roomAdapter.interfaceMetric > 5) {
    warnings.push(`VPN 网卡跃点较高：${roomAdapter.interfaceMetric}`)
  }
  if (conflictingAdapterIndexes.length) warnings.push(`检测到可能干扰 WE8 的虚拟网卡：${conflictingAdapters.join('、')}`)

  return {
    connected: Boolean(actualIp),
    actualIp,
    subnetCidr: cidr,
    adapterName: roomAddress?.name || null,
    adapterDescription: roomAdapter?.description || roomAddress?.name || null,
    interfaceIndex: roomAdapter?.interfaceIndex ?? null,
    interfaceMetric: roomAdapter?.interfaceMetric ?? null,
    defaultGateways: roomAdapter?.defaultGateways || [],
    dnsServers: roomAdapter?.dnsServers || [],
    conflictingAdapters,
    conflictingAdapterIndexes,
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
  const network = analyzeNetwork(cidr, roomAddress, adapters)
  if (!roomAddress || network.interfaceIndex !== null) return network

  try {
    const fallback = await queryNetshInterface(roomAddress.name)
    if (!fallback) return network
    return {
      ...network,
      interfaceIndex: fallback.interfaceIndex,
      interfaceMetric: fallback.interfaceMetric,
    }
  } catch {
    return network
  }
}

function buildVpnPriorityScript(interfaceIndex, conflictingAdapterIndexes = []) {
  const index = Number(interfaceIndex)
  if (!Number.isInteger(index) || index <= 0) throw new Error('VPN 网卡接口编号无效')
  const conflictingIndexes = [...new Set(conflictingAdapterIndexes.map(Number))]
    .filter((candidate) => Number.isInteger(candidate) && candidate > 0 && candidate !== index)
  const lowerConflictingMetrics = conflictingIndexes.map((candidate) => `
& $netsh interface ipv4 set interface "interface=${candidate}" "metric=5000" "store=persistent" | Out-Null
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }`).join('')
  return `
$netsh = Join-Path $env:SystemRoot 'System32\\netsh.exe'
& $netsh interface ipv4 set interface "interface=${index}" "metric=1" "store=persistent" | Out-Null
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
${lowerConflictingMetrics}
`
}

async function runElevatedPowerShell(script, timeoutMs = 120000) {
  const innerCommand = Buffer.from(script, 'utf16le').toString('base64')
  const elevatedCommand = `
$arguments = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', '${innerCommand}')
$process = Start-Process -FilePath 'powershell.exe' -ArgumentList $arguments -Verb RunAs -WindowStyle Hidden -Wait -PassThru
if ($null -eq $process) { exit 1 }
exit $process.ExitCode
`
  return runPowerShell(elevatedCommand, timeoutMs)
}

async function prioritizeVpnNetwork(cidr) {
  const network = await inspectVpnNetwork(cidr)
  if (!network.connected || network.interfaceIndex === null) return network
  if (network.interfaceMetric === 1 && network.conflictingAdapterIndexes.length === 0) return network

  try {
    await runElevatedPowerShell(buildVpnPriorityScript(network.interfaceIndex, network.conflictingAdapterIndexes))
    await new Promise((resolve) => setTimeout(resolve, 500))
    return inspectVpnNetwork(cidr)
  } catch {
    return {
      ...network,
      warnings: [...network.warnings, '无法自动调整 VPN 网卡优先级，请同意管理员授权后重试'],
    }
  }
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
  buildVpnPriorityScript,
  decodeProcessOutput,
  findNetstatLines,
  findRoomAddress,
  inspectVpnNetwork,
  isIPv4InCIDR,
  parseAdapterOutput,
  parseNetshInterfaces,
  parseTasklistPids,
  prioritizeVpnNetwork,
  runPowerShell,
  runProcess,
  waitForVpnNetwork,
}
