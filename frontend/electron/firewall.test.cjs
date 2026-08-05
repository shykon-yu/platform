const test = require('node:test')
const assert = require('node:assert/strict')
const { buildFirewallScript, powerShellLiteral } = require('./firewall.cjs')

test('escapes apostrophes in PowerShell path literals', () => {
  assert.equal(powerShellLiteral("C:\\Games\\Player's WE8\\WE8.exe"), "'C:\\Games\\Player''s WE8\\WE8.exe'")
})

test('limits game firewall rules to the WEL virtual subnet', () => {
  const script = buildFirewallScript('D:\\实况足球8\\WE8.exe')
  assert.match(script, /WEL WE8 Game Inbound/)
  assert.match(script, /WEL WE8 Game Outbound/)
  assert.match(script, /remoteip=10\.80\.0\.0\/16/)
  assert.match(script, /profile=any/)
  assert.match(script, /program=\$program/)
})
