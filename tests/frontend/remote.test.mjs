import { test } from 'node:test'
import assert from 'node:assert/strict'
import { expiryInput, expiryPayload, hostStatusLabel, remoteCapabilities } from '../../packages/ui/remote.ts'

test('remote availability uses authoritative states independently of the client clock', () => {
  const original = Date.now
  try {
    for (const offset of [-86400000, 86400000]) {
      Date.now = () => original() + offset
      assert.equal(hostStatusLabel('ready'), '可连接')
      assert.equal(hostStatusLabel('busy'), '正在使用')
      assert.equal(hostStatusLabel('vpn_offline'), 'VPN 未连接')
    }
    assert.equal(hostStatusLabel('unexpected'), '状态未知')
  } finally { Date.now = original }
})
test('grant edits retain view and remove unsupported capabilities', () => {
  assert.equal(remoteCapabilities(63, 7), 7)
  assert.equal(remoteCapabilities(2, 3), 3)
  assert.equal(remoteCapabilities(7, 1), 1)
  assert.equal(remoteCapabilities(1, 0), 0)
  assert.equal(remoteCapabilities(1, 2), 0)
  assert.equal(remoteCapabilities(NaN, 7), 0)
})
test('editing a grant preserves its expiration instead of silently extending it', () => {
  const expiry = '2026-12-01T10:30:00.000Z'
  assert.equal(expiryPayload(expiryInput(expiry)), expiry)
  assert.equal(expiryInput('0001-01-01T00:00:00Z'), '')
  assert.equal(expiryPayload(''), undefined)
  assert.throws(() => expiryPayload('invalid'))
})
