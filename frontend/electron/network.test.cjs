const test = require('node:test')
const assert = require('node:assert/strict')
const { analyzeNetwork, findRoomAddress, isIPv4InCIDR, parseAdapterOutput } = require('./network.cjs')

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
      ipAddresses: ['10.80.3.11'], subnets: ['255.255.255.0'],
      defaultGateways: ['10.80.3.1'], dnsServers: ['10.80.3.1'],
    },
    {
      description: 'TAP-Windows Adapter V9', ipEnabled: true,
      ipAddresses: ['10.0.0.2'], subnets: ['255.255.255.0'],
      defaultGateways: [], dnsServers: [],
    },
  ])

  assert.equal(status.connected, true)
  assert.equal(status.actualIp, '10.80.3.11')
  assert.deepEqual(status.conflictingAdapters, ['TAP-Windows Adapter V9'])
  assert.equal(status.warnings.length, 3)
})

test('parses base64 encoded PowerShell adapter fields', () => {
  const encode = (value) => Buffer.from(value, 'utf8').toString('base64')
  const output = `${encode('SoftEther VPN Client Adapter - VPN')}|True|${encode('10.80.1.10')}|${encode('255.255.255.0')}||\n`
  const adapters = parseAdapterOutput(output)
  assert.equal(adapters.length, 1)
  assert.equal(adapters[0].description, 'SoftEther VPN Client Adapter - VPN')
  assert.deepEqual(adapters[0].defaultGateways, [])
})
