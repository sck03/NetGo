import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { ref } from 'vue'
import { LatestQuery } from '../../packages/ui/query.ts'

function panel(request) {
  const source = fs.readFileSync(new URL('../../apps/ui/src/AccountCreations.vue', import.meta.url), 'utf8').split('<script setup lang="ts">')[1].split('</script>')[0]
  const code = ts.transpileModule(source + '\nexports.test = {load,manage,page,error,actionError};', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  let mounted, unmounted, poll
  const context = { exports: {}, Error, defineProps: () => ({request}), document: {hidden:false}, window:{confirm:()=>true},
    setInterval: fn => {poll=fn; return 1}, clearInterval:()=>{}, require(name) {
      if(name==='vue') return {ref,onMounted:fn=>{mounted=fn},onUnmounted:fn=>{unmounted=fn}}
      if(name.endsWith('/query'))return {LatestQuery}
      return {}
    }}
  vm.runInNewContext(code,context)
  return {...context.exports.test, mount:()=>mounted(), poll:()=>poll(), close:()=>unmounted()}
}
const flush = () => new Promise(resolve=>setImmediate(resolve))
const task = {id:'old',alias:'old',pending:true,can_manage:true,created_at:'2099-01-01T00:00:00Z'}

test('polls reuse a slow query and pagination alone cancels stale reads', async () => {
  const calls=[]
  const ui=panel((path,method,body,signal)=>{ const result=Promise.withResolvers();calls.push({path,signal,...result});return result.promise })
  ui.mount();await flush()
  for(let i=0;i<3;i++){ui.poll();await flush()}
  assert.equal(calls.length,1);assert.equal(calls[0].signal.aborted,false)
  const navigation=ui.load('next');await flush()
  assert.equal(calls[0].signal.aborted,true);assert.equal(calls.length,2)
  calls[1].resolve({tasks:[task],next_after:''});await navigation
  calls[0].resolve({tasks:[],next_after:'stale'});await flush()
  assert.equal(ui.page.value.tasks[0].id,'old');assert.equal(ui.page.value.next_after,'')
  ui.close()
})

test('successful reads clear read errors without hiding action failures', async () => {
  let failRead=true,posts=0
  const ui=panel(async (path,method)=>{
    if(method==='POST'){posts++;throw Error('operator conflict')}
    if(failRead)throw Error('offline')
    return {tasks:[task],next_after:''}
  })
  await ui.load();assert.equal(ui.error.value,'offline')
  failRead=false;await ui.load();assert.equal(ui.error.value,'')
  // A future browser date cannot override authoritative can_manage.
  await ui.manage(task);assert.equal(posts,1)
  assert.equal(ui.actionError.value,'operator conflict');assert.equal(ui.error.value,'')
  await ui.load();assert.equal(ui.actionError.value,'operator conflict')
  await ui.manage({...task,can_manage:false});assert.equal(posts,1)
  ui.close()
})
