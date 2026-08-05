const { runPowerShell } = require('./network.cjs')

const INBOUND_RULE = 'WEL WE8 Game Inbound'
const OUTBOUND_RULE = 'WEL WE8 Game Outbound'

function powerShellLiteral(value) {
  return `'${String(value).replace(/'/g, "''")}'`
}

function buildFirewallScript(gamePath) {
  return `
$program = ${powerShellLiteral(gamePath)}
$netsh = Join-Path $env:SystemRoot 'System32\\netsh.exe'

& $netsh advfirewall firewall delete rule "name=${INBOUND_RULE}" | Out-Null
& $netsh advfirewall firewall delete rule "name=${OUTBOUND_RULE}" | Out-Null

& $netsh advfirewall firewall add rule "name=${INBOUND_RULE}" "dir=in" "action=allow" "program=$program" "enable=yes" "profile=any" "remoteip=10.80.0.0/16" "protocol=any" | Out-Null
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $netsh advfirewall firewall add rule "name=${OUTBOUND_RULE}" "dir=out" "action=allow" "program=$program" "enable=yes" "profile=any" "remoteip=10.80.0.0/16" "protocol=any" | Out-Null
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
`
}

async function configureGameFirewall(gamePath) {
  const innerCommand = Buffer.from(buildFirewallScript(gamePath), 'utf16le').toString('base64')
  const elevatedCommand = `
$arguments = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', '${innerCommand}')
$process = Start-Process -FilePath 'powershell.exe' -ArgumentList $arguments -Verb RunAs -WindowStyle Hidden -Wait -PassThru
if ($null -eq $process) { exit 1 }
exit $process.ExitCode
`
  try {
    await runPowerShell(elevatedCommand, 120000)
  } catch {
    throw new Error('WE8 防火墙规则配置失败，请同意管理员授权后重试')
  }
}

module.exports = { buildFirewallScript, configureGameFirewall, powerShellLiteral }
