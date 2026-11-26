import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { ref, computed, watch, effectScope } from 'vue'
import { LatestQuery } from '../../packages/ui/query.ts'

function picker(request, selected = '') {
  const source = fs.readFileSync(new URL('../../apps/ui/src/UserPicker.vue', import.meta.url), 'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
  const code = ts.transpileModule(source + '\nexports.test={load,page,after,busy,error,selectedOnPage,selectedLabel};', {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  const scope = effectScope()
  let unmount
  const props = { request, modelValue: selected, disabled: false }
  const context = {exports:{},defineProps:()=>props,defineEmits:()=>()=>{},setTimeout,clearTimeout,require(name) {
    if (name === 'vue') return {ref,computed,watch,onMounted:()=>{},onUnmounted:callback=>{unmount=callback}}
    if (name.endsWith('/query')) return {LatestQuery}
    return {}
  }}
  scope.run(() => vm.runInNewContext(code, context))
  return {...context.exports.test,props,stop() { unmount(); scope.stop() }}
}

test('remote account pagination preserves a selection outside the current page', async () => {
  const calls = []
  const ui = picker(async path => {
    const after = new URL(path, 'https://test.example').searchParams.get('after')
    calls.push(after)
    return {users:[{id:after ? 'later' : 'selected',alias:'Account',name:'Name'}],next_after:after ? '' : 'cursor'}
  }, 'selected')
  try {
    await ui.load()
    assert.equal(ui.selectedOnPage.value, true)
    await ui.load('cursor')
    assert.equal(ui.selectedOnPage.value, false)
    assert.equal(ui.props.modelValue, 'selected')
    assert.equal(ui.selectedLabel.value, 'Name · Account')
    assert.equal(ui.after.value, 'cursor')
    assert.deepEqual(calls, ['', 'cursor'])
  } finally { ui.stop() }
})

test('a stale account request cannot replace a newer page or its loading state', async () => {
  const old = Promise.withResolvers(), fresh = Promise.withResolvers()
  let calls = 0
  const ui = picker(() => ++calls === 1 ? old.promise : fresh.promise)
  try {
    const first = ui.load()
    await Promise.resolve()
    const second = ui.load('next')
    await Promise.resolve()
    old.reject(Error('old request failed'))
    await first
    assert.equal(ui.busy.value, true)
    assert.equal(ui.error.value, '')
    fresh.resolve({users:[{id:'new'}],next_after:''})
    await second
    assert.equal(ui.page.value.users[0].id, 'new')
    assert.equal(ui.busy.value, false)
  } finally { ui.stop() }
})
