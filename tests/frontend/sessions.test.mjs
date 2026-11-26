import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { ref, computed } from 'vue'
import { LatestQuery } from '../../packages/ui/query.ts'

function panel(request) {
  const source = fs.readFileSync(new URL('../../apps/ui/src/SessionPanel.vue', import.meta.url), 'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
  const code = ts.transpileModule(source + '\nexports.test={refresh,navigate,revoke,sessions,inventory,sessionAfter,deviceAfter};', {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  let stop
  const context={exports:{},defineProps:()=>({request}),setInterval,clearInterval,document:{hidden:false},require(name){
    if(name==='vue')return {ref,computed,onMounted:()=>{},onUnmounted:callback=>{stop=callback}}
    if(name.endsWith('/query'))return {LatestQuery}
    if(name.endsWith('/contracts'))return {emptyState:()=>({users:{},resources:{},devices:{},leases:{}})}
    return {}
  }}
  vm.runInNewContext(code,context)
  return {...context.exports.test,stop}
}

test('device and lease pages retain independent cursors and owner labels', async () => {
  const calls=[]
  const ui=panel(async path=>{
    calls.push(path)
    const url=new URL(path,'https://test.example')
    return url.searchParams.get('section')==='sessions'
      ? {leases:{active:{id:'active',user_id:'owner'}},users:{owner:{alias:'Alice'}},next_after:'active'}
      : {devices:{[url.searchParams.get('after')||'first']:{id:'device'}},next_after:'device-page-2'}
  })
  await ui.refresh()
  assert.equal(ui.sessions.value.leases.active.user_id,'owner')
  await ui.navigate('devices','device-page-2')
  assert.equal(ui.sessionAfter.value,'')
  assert.equal(ui.deviceAfter.value,'device-page-2')
  assert.equal(ui.sessions.value.users.owner.alias,'Alice')
  await ui.navigate('sessions','active')
  assert.equal(ui.deviceAfter.value,'device-page-2')
  assert.equal(calls.filter(path=>path.includes('section=devices')).length,2)
  ui.stop()
})

test('leaving the page during revocation does not schedule new reads', async () => {
  const deleted=Promise.withResolvers(),calls=[]
  const ui=panel((path,method)=>{calls.push(path);return method==='DELETE'?deleted.promise:Promise.resolve({})})
  const saving=ui.revoke('devices','obsolete')
  ui.stop()
  deleted.resolve({deleted:true})
  await saving
  assert.deepEqual(calls,['/netgo/v2/admin/devices/obsolete'])
})
