import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import { parse } from '@vue/compiler-sfc'
import * as resources from '../../packages/ui/resources.ts'
import * as remote from '../../packages/ui/remote.ts'
import { LatestQuery } from '../../packages/ui/query.ts'

function panel(name, props, expose, emit = () => {}) {
  const { descriptor } = parse(fs.readFileSync(new URL('../../apps/ui/src/' + name + '.vue', import.meta.url), 'utf8'))
  const script = descriptor.scriptSetup.content + '\nObject.assign(exports, {' + expose + '});'
  const context = { exports: {}, defineProps: () => props, defineEmits: () => emit, setTimeout, clearTimeout, document: { getElementById: () => null }, require: name => name === 'vue' ? { ...vue, onMounted() {}, onUnmounted() {} } : name.endsWith('/resources') ? resources : name.endsWith('/remote') ? remote : { LatestQuery } }
  vm.runInNewContext(ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, context)
  return context.exports
}

test('resource conflict preserves the draft and prevents restoring disabled access', async () => {
  const resource = { id: 'nas', name: 'NAS', cidr: '192.168.18.2/32', protocol: 'tcp', ports: [443], enabled: true, edit_token: 'first' }
  let saves = 0
  const props = vue.reactive({ resources: [resource], save: async () => { saves++; return true } })
  const p = panel('ResourcePanel', props, 'edit, form, stale, save')
  p.edit(resource); p.form.value.name = 'My draft'
  props.resources = [{ ...resource, enabled: false, edit_token: 'second' }]
  assert.equal(p.stale.value, true)
  assert.equal(p.form.value.name, 'My draft')
  await p.save(); assert.equal(saves, 0)
})

test('grant refresh detects changes without overwriting draft authority or expiry', async () => {
  const grant = { id: 'g', user_id: 'u', host_id: 'h', capabilities: 3, expires_at: '0001-01-01T00:00:00Z', edit_token: 'first' }
  let saves = 0
  const p = panel('RemotePanel', { request: async () => { saves++ } }, 'inventory, form, editGrant, stale, reloadGrant, save')
  p.inventory.value = { hosts: [{ id: 'h', capabilities: 3 }], grants: [grant], sessions: [], users: {} }
  p.editGrant(grant); p.form.value.expiry = '2030-01-01T12:00'
  p.inventory.value.grants = [{ ...grant, capabilities: 1, edit_token: 'second' }]
  assert.equal(p.stale.value, true)
  assert.equal(p.form.value.expiry, '2030-01-01T12:00')
  await p.save(); assert.equal(saves, 0)
  p.reloadGrant(); assert.equal(p.form.value.capabilities, 1); assert.equal(p.stale.value, false)
  p.inventory.value.grants = []; assert.equal(p.stale.value, true)
})

test('resource picker retains selected labels across pages and searches', async () => {
  const paths = []
  const props = vue.reactive({ modelValue: ['a'], resources: [{ id: 'a', name: 'Archive' }], disabled: false,
    request: async path => {
      paths.push(path)
      return path.includes('after=b') || path.includes('q=Gamma')
        ? { resources: [{ id: 'c', name: 'Gamma' }], next_after: '' }
        : { resources: [{ id: 'a', name: 'Archive' }, { id: 'b', name: 'Backup' }], next_after: 'b' }
    } })
  const p = panel('ResourcePicker', props, 'load, toggle, offPage, labels, page, search', (_, value) => { props.modelValue = value })
  await p.load()
  p.toggle('b', true); await vue.nextTick()
  await p.load('b'); await vue.nextTick()
  assert.deepEqual(Array.from(props.modelValue), ['a', 'b'])
  assert.deepEqual(Array.from(p.offPage.value), ['a', 'b'])
  assert.equal(p.labels.value.b, 'Backup')
  p.search.value = 'Gamma'; await vue.nextTick()
  await new Promise(resolve => setTimeout(resolve, 300))
  assert.ok(paths.at(-1).endsWith('after=&q=Gamma'))
  assert.equal(p.page.value.resources[0].id, 'c')
  assert.equal(p.labels.value.a, 'Archive')
  p.toggle('a', false); await vue.nextTick()
  assert.deepEqual(Array.from(props.modelValue), ['b'])
  assert.deepEqual(Object.keys(p.labels.value), ['b'])
})
