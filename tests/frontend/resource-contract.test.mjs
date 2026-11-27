import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import { renderToString } from '@vue/server-renderer'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as resources from '../../packages/ui/resources.ts'

test('the Go API ICMP response renders and opens in the resource editor', async () => {
  const resource = JSON.parse(fs.readFileSync(new URL('../contracts/resource-icmp.json', import.meta.url)))
  const source = fs.readFileSync(new URL('../../apps/ui/src/ResourcePanel.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(source)
  const script = compileScript(descriptor, { id: 'resource-contract', inlineTemplate: true })
  const code = ts.transpileModule(script.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const context = { exports: {}, require: name => name === 'vue' ? vue : resources }
  vm.runInNewContext(code, context)
  const component = context.exports.default
  const html = await renderToString(vue.createSSRApp(component, {
    resources: [resource], network: { lan_interface: 'eth0', local_cidrs: ['192.168.18.0/23'], internet_enabled: false, dns: [] },
    busy: false, save: async () => true, remove: async () => true,
  }))
  assert.match(html, /Ping/)
  assert.match(html, /ICMP/)
  // Exercise the same edit handler whose ports.join previously failed.
  const editScript = descriptor.scriptSetup.content + '\nexports.edit = edit; exports.form = form;'
  const editing = { exports: {}, defineProps: () => ({}), require: context.require }
  vm.runInNewContext(ts.transpileModule(editScript, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, editing)
  editing.exports.edit(resource)
  assert.equal(editing.exports.form.value.ports, '')
})
