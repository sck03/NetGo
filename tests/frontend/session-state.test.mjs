import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { AdminSession } from '../../packages/ui/auth.ts'
import { LatestQuery } from '../../packages/ui/query.ts'

function app() {
  const source = fs.readFileSync(new URL('../../apps/ui/src/App.vue', import.meta.url), 'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
  const code = ts.transpileModule(source + '\nexports.test = {admin,logged,notice,initialPassword,state,createUser,logout};', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const context = { exports: {}, console, setInterval, clearInterval, setTimeout, document: {hidden:false}, require(name) {
    if (name==='vue') return {ref:value=>({value}),computed:fn=>({get value(){return fn()}}),watch:()=>{},onMounted:()=>{},onUnmounted:()=>{}}
    if(name.endsWith('/auth'))return {AdminSession}
    if(name.endsWith('/query'))return {LatestQuery}
    if(name.endsWith('/contracts'))return {emptyState:()=>({users:{},resources:{},devices:{},leases:{}})}
    return {}
  }}
  vm.runInNewContext(code,context)
  return context.exports.test
}

test('logout clears the one-time password and every page observes session invalidation', async () => {
  const ui=app()
  ui.admin.onActiveChange(true)
  ui.admin.request=async path=>path==='/netgo/v2/admin/users'?{initial_password:'temporary-test-password'}:{}
  assert(await ui.createUser({Alias:'alice',Email:'alice@example.test',Name:'Alice'}))
  assert.equal(ui.initialPassword.value,'temporary-test-password')
  assert(!ui.notice.value.includes('temporary-test-password'))
  ui.admin.logout()
  assert.equal(ui.logged.value,false)
  assert.equal(ui.initialPassword.value,'')
  assert.equal(ui.notice.value,'')
  assert.equal(Object.keys(ui.state.value.users).length,0)
})
