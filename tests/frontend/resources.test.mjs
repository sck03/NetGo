import { test } from 'node:test'
import assert from 'node:assert/strict'
import { resourcePayload } from '../../packages/ui/resources.ts'
const form = { id: 'resource-example', name: ' 文件服务 ', cidr: '192.168.18.20', protocol: 'tcp', ports: '445', enabled: true }

test('a host IP becomes a single-host resource without widening access', () => {
  assert.deepEqual(resourcePayload(form), { ...form, name: '文件服务', cidr: '192.168.18.20/32', ports: [445] })
  assert.equal(resourcePayload({ ...form, cidr: '192.168.18.0/23' }).cidr, '192.168.18.0/23')
  assert.throws(() => resourcePayload({ ...form, cidr: '192.168.18.20/24' }), new RegExp('192[.]168[.]18[.]0/24'))
})
test('invalid host, mask, ports and resource identity are rejected', () => {
  for (const cidr of ['', '192.168.18.256', '192.168.018.1', 'example.com', '192.168.18.20/', '192.168.18.20/-1', '192.168.18.20/33', '192.168.18.20/32/1']) assert.throws(() => resourcePayload({ ...form, cidr }))
  for (const ports of ['', '0', '65536', '443,', '2.5', '1e3', '0x50']) assert.throws(() => resourcePayload({ ...form, ports }))
  assert.throws(() => resourcePayload({ ...form, id: '../bad' }))
  assert.throws(() => resourcePayload({ ...form, protocol: 'any' }))
  assert.throws(() => resourcePayload({ ...form, name: ' ' }))
  assert.deepEqual(resourcePayload({ ...form, ports: '443, 443, 8443' }).ports, [443, 8443])
})
test('internet and ICMP ignore irrelevant ports without broadening normal resources', () => {
  assert.equal(resourcePayload({ ...form, protocol: 'internet', cidr: '', ports: '' }).cidr, '0.0.0.0/0')
  assert.deepEqual(resourcePayload({ ...form, protocol: 'icmp', ports: '' }).ports, [])
  assert.throws(() => resourcePayload({ ...form, cidr: '0.0.0.0/0' }))
})
