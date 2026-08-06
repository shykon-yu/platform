const test = require('node:test')
const assert = require('node:assert/strict')
const { analyzeNetwork, buildVpnPriorityScript, findNetstatLines, findRoomAddress, isIPv4InCIDR, parseAdapterOutput, parseNetshInterfaces, parseTasklistPids } = require('./network.cjs')

test('matches only addresses in the room subnet', () => {
  assert.equal(isIPv4InCIDR('10.80.3.10', '10.80.3.0/24'), true)
  assert.equal(isIPv4InCIDR('10.80.4.10', '10.80.3.0/24'), false)
  assert.equal(isIPv4InCIDR('invalid', '10.80.3.0/24'), false)
})

test('finds the real room address from operating system interfaces', () => {
  const result = findRoomAddress('10.80.3.0/24', {
    Ethernet: [{ family: 'IPv4', address: '192.168.1.20', internal: false }],
    VPN: [{ family: 'IPv4', address: '10.80.3.11', internal: false }],
  })
  assert.deepEqual(result, { name: 'VPN', address: '10.80.3.11' })
})

test('reports stale gateway and enabled conflicting adapters', () => {
  const status = analyzeNetwork('10.80.3.0/24', { name: 'VPN', address: '10.80.3.11' }, [
    {
      description: 'SoftEther VPN Client Adapter - VPN', ipEnabled: true,
      interfaceIndex: 18, interfaceMetric: 25,
      ipAddresses: ['10.80.3.11'], subnets: ['255.255.255.0'],
      defaultGateways: ['10.80.3.1'], dnsServers: ['10.80.3.1'],
    },
    {
      description: 'TAP-Windows Adapter V9', ipEnabled: true,
      interfaceIndex: 19, interfaceMetric: 1,
      ipAddresses: ['10.0.0.2'], subnets: ['255.255.255.0'],
      defaultGateways: [], dnsServers: [],
    },
    {
      description: 'Gateway NC Adapter', ipEnabled: true,
      interfaceIndex: 20, interfaceMetric: 5,
      ipAddresses: ['192.168.88.2'], subnets: ['255.255.255.0'],
      defaultGateways: [], dnsServers: [],
    },
  ])

  assert.equal(status.connected, true)
  assert.equal(status.actualIp, '10.80.3.11')
  assert.equal(status.interfaceIndex, 18)
  assert.equal(status.interfaceMetric, 25)
  assert.deepEqual(status.conflictingAdapters, ['TAP-Windows Adapter V9', 'Gateway NC Adapter'])
  assert.deepEqual(status.conflictingAdapterIndexes, [19, 20])
  assert.equal(status.warnings.length, 4)
})

test('parses base64 encoded PowerShell adapter fields', () => {
  const encode = (value) => Buffer.from(value, 'utf8').toString('base64')
  const output = `${encode('SoftEther VPN Client Adapter - VPN')}|True|18|25|${encode('10.80.1.10')}|${encode('255.255.255.0')}||\n`
  const adapters = parseAdapterOutput(output)
  assert.equal(adapters.length, 1)
  assert.equal(adapters[0].description, 'SoftEther VPN Client Adapter - VPN')
  assert.equal(adapters[0].interfaceIndex, 18)
  assert.equal(adapters[0].interfaceMetric, 25)
  assert.deepEqual(adapters[0].defaultGateways, [])
})

test('parses interface index and metric from localized netsh output', () => {
  const output = `Idx     Met         MTU          状态                名称\r\n---  ----------  ----------  ------------  ---------------------------\r\n 37           5        1500  connected     VPN - VPN Client\r\n  6          35        1500  connected     WLAN\r\n`
  assert.deepEqual(parseNetshInterfaces(output), [
    { interfaceIndex: 37, interfaceMetric: 5, name: 'VPN - VPN Client' },
    { interfaceIndex: 6, interfaceMetric: 35, name: 'WLAN' },
  ])
})

test('builds a Win7-compatible netsh command for the room adapter', () => {
  const script = buildVpnPriorityScript(18, [19, 19, 20])
  assert.match(script, /interface=18/)
  assert.match(script, /metric=1/)
  assert.match(script, /interface=19/)
  assert.match(script, /interface=20/)
  assert.match(script, /metric=5000/)
  assert.equal((script.match(/interface=19/g) || []).length, 1)
  assert.match(script, /store=persistent/)
  assert.throws(() => buildVpnPriorityScript('invalid'), /接口编号无效/)
})

test('parses WE8 tasklist rows and matches netstat endpoints by PID', () => {
  const processes = parseTasklistPids([
    '"WE8.exe","3108","Console","1","42,000 K"',
    '"explorer.exe","1200","Console","1","80,000 K"',
    '"dpnsvr.exe","5520","Console","1","8,000 K"',
  ].join('\r\n'))
  assert.deepEqual(processes, [
    { name: 'WE8', pid: 3108 },
    { name: 'dpnsvr', pid: 5520 },
  ])

  const endpoints = findNetstatLines([
    '  UDP    0.0.0.0:5739           *:*                                    3108',
    '  UDP    0.0.0.0:49288          *:*                                    10908',
    '  TCP    10.80.1.13:2300        10.80.1.14:49820       ESTABLISHED     5520',
  ].join('\r\n'), processes)
  assert.equal(endpoints.length, 2)
  assert.match(endpoints[0], /5739/)
  assert.match(endpoints[1], /ESTABLISHED/)
})
