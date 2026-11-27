import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import { parse } from '@vue/compiler-sfc'

test('an open account editor detects a new revision without replacing the draft', async () => {
  const user = { id: 'u', alias: 'user', name: 'User', resources: ['nas'], edit_revision: 1, disabled: false }
  let saves = 0
  const props = vue.reactive({ users: [user], save: async () => { saves++; return true } })
  const source = fs.readFileSync(new URL('../../apps/ui/src/UserPanel.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(source)
  const script = descriptor.scriptSetup.content + '\nObject.assign(exports, { edit, draft, stale, latest, save });'
  const editing = { exports: {}, defineProps: () => props, require: name => name === 'vue' ? vue : {} }
  vm.runInNewContext(ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, editing)
  const panel = editing.exports
  panel.edit(user)
  panel.draft.value.name = 'My draft'
  props.users = [{ ...user, disabled: true, edit_revision: 2 }]
  assert.equal(panel.stale.value, true)
  assert.equal(panel.draft.value.name, 'My draft')
  await panel.save()
  assert.equal(saves, 0)
  panel.edit(panel.latest.value)
  assert.equal(panel.draft.value.disabled, true)
  assert.equal(panel.stale.value, false)
  await panel.save()
  assert.equal(saves, 1)
})
