import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { AdminSession } from '../../packages/ui/auth.ts'
import { LatestQuery } from '../../packages/ui/query.ts'
import { ref, computed, watch, nextTick } from 'vue'

function app() {
  const source = fs.readFileSync(new URL('../../apps/ui/src/App.vue', import.meta.url), 'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
  const code = ts.transpileModule(source + '\nexports.test = {admin,logged,notice,initialPassword,state,createUser,logout,action,page,busy,refresh,error,readError};', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const context = { exports: {}, console, setInterval, clearInterval, setTimeout, document: {hidden:false}, require(name) {
    if (name==='vue') return {ref,computed,watch,onMounted:()=>{},onUnmounted:()=>{}}
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

test('navigation loads the selected page while an unrelated action is pending', async () => {
  const ui=app(), requests=[]
  ui.admin.onActiveChange(true)
  ui.admin.request=async path=>{requests.push(path);return {users:{alice:{id:'alice'}}}}
  let complete
  const pending=ui.action(()=>new Promise(resolve=>{complete=resolve}))
  ui.page.value='users'
  await nextTick()
  await Promise.resolve()
  assert.equal(ui.busy.value,true)
  assert.equal(requests.length,1)
  assert.match(requests[0],/section=users/)
  complete()
  await pending
  assert.equal(ui.state.value.users.alice.id,'alice')
  ui.logout()
})

test('a completed mutation supersedes a page query started while saving', async () => {
  const ui=app()
  ui.admin.onActiveChange(true)
  let finishCreate, finishOldRead, reads=0
  ui.admin.request=path=>{
    if(path==='/netgo/v2/admin/users') return new Promise(resolve=>{finishCreate=resolve})
    if(++reads===1) return new Promise(resolve=>{finishOldRead=resolve})
    return Promise.resolve({users:{new:{id:'new'}}})
  }
  const saving=ui.createUser({Alias:'new',Email:'new@example.test',Name:'New'})
  ui.page.value='users'
  await nextTick();await Promise.resolve()
  finishCreate({})
  await saving
  finishOldRead({users:{old:{id:'old'}}})
  await Promise.resolve();await Promise.resolve()
  assert.equal(reads,2)
  assert.equal(ui.state.value.users.new.id,'new')
  assert.equal(ui.state.value.users.old,undefined)
  ui.logout()
})

test('overview polling recovers without erasing action failures or repeating successful writes', async () => {
  const ui = app()
  ui.admin.onActiveChange(true)
  Object.defineProperty(ui.admin, 'active', {get: () => true})
  let offline = true, writes = 0
  ui.admin.request = async path => {
    if (path === '/netgo/v2/admin/users') { writes++; return {initial_password:'one-time'} }
    if (offline) throw 'offline'
    return {}
  }
  assert.equal(await ui.createUser({Alias:'new',Email:'new@example.test',Name:'New'}), true)
  assert.equal(ui.readError.value, 'offline')
  assert.equal(ui.initialPassword.value, 'one-time')
  await ui.action(async () => { throw 'write failed' })
  offline = false
  await ui.refresh()
  assert.equal(ui.readError.value, '')
  assert.equal(ui.error.value, 'write failed')
  assert.equal(writes, 1)
  ui.logout()
})
