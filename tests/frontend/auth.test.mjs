import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, webcrypto } from 'node:crypto'
import { AdminSession, validateHealth } from '../../packages/ui/auth.ts'
const enc = new TextEncoder()
const unb64 = s => Buffer.from(s, 'base64url')
const sha = s => createHash('sha256').update(s).digest('hex')
const health = { protocol: 2, site_id: 'site', capabilities: ['native-auth-v1','device-signature-v1','leases-v1','profile-revision-v1','admin-v1','diagnostics-v1'] }

function server(t, role = 'admin', clockOffset = 0) {
  const original = globalThis.fetch
  const wallNow = Date.now
  let publicKey, begin, token = 'first-token', sawRefresh = false
  const paths = []
  globalThis.fetch = async (path, options) => {
    paths.push(path)
    assert.equal(options.redirect, 'error')
    assert.equal(options.cache, 'no-store')
    assert.equal(options.headers['X-NetGo-Protocol'], '2')
    if (path.endsWith('/health')) return new Response(JSON.stringify({ ...health, server_unix: Math.floor((wallNow() + clockOffset) / 1000) }))
    const body = options.body || '', data = body ? JSON.parse(body) : {}
    if (path.endsWith('/transactions')) {
      begin = data
      publicKey = await webcrypto.subtle.importKey('raw', unb64(begin.public_key), 'Ed25519', false, ['verify'])
    } else if (path.endsWith('/complete')) {
      const proof = 'netgo-native-v1\nsite\ntx\nnonce'
      assert(await webcrypto.subtle.verify('Ed25519', publicKey, unb64(data.Signature), enc.encode(proof)))
      assert.equal(data.Alias, 'admin')
    } else if (path.endsWith('/exchange')) {
      assert.equal(createHash('sha256').update(data.Verifier).digest('base64url'), begin.challenge)
      assert(await webcrypto.subtle.verify('Ed25519', publicKey, unb64(data.Signature), enc.encode('netgo-native-v1\nsite\ntx\nnonce\nexchange\n' + data.Verifier)))
    } else {
      const h = options.headers
      assert(Math.abs(Number(h['X-NetGo-Time']) * 1000 - wallNow() - clockOffset) < 2000)
      const proof = [options.method, path, h['X-NetGo-Time'], h['X-NetGo-Nonce'], sha(body), sha(token)].join('\n')
      assert.equal(h.Authorization, 'Bearer ' + token)
      assert(await webcrypto.subtle.verify('Ed25519', publicKey, unb64(h['X-NetGo-Signature']), enc.encode(proof)))
      if (path.endsWith('/refresh')) { assert.equal(data.refresh_token, 'refresh'); sawRefresh = true; token = 'renewed-token' }
    }
    let result = { ok: true }
    if (path.endsWith('/transactions')) result = { id: 'tx', nonce: 'nonce' }
    if (path.endsWith('/exchange') || path.endsWith('/refresh')) result = { token, refresh_token: 'refresh', expires_at: new Date(wallNow() + clockOffset + (sawRefresh ? 3600000 : 60000)).toISOString(), user: { role } }
    return new Response(JSON.stringify(result), { status: 200 })
  }
  t.after(() => { globalThis.fetch = original })
  return { paths, refreshed: () => sawRefresh }
}

test('trusted server time signs requests despite skew and later client clock changes', async t => {
  for (const offset of [-3600000, 3600000]) await t.test(String(offset), async t => {
    const fixture = server(t, 'admin', offset), session = new AdminSession()
    await session.login('site', 'admin', 'password')
    const wallNow = Date.now
    t.after(() => { Date.now = wallNow })
    Date.now = () => wallNow() + 86400000
    await session.request('/netgo/v2/admin/state')
    assert(fixture.refreshed())
  })
})

test('management login proves PKCE/key ownership and refresh signs the old token', async t => {
  const fixture = server(t)
  const session = new AdminSession()
  await session.login('site', 'admin', 'test-password')
  await session.request('/netgo/v2/admin/state')
  assert(fixture.refreshed())
  assert(fixture.paths.includes('/netgo/v2/admin/state'))
})

test('ordinary users cannot retain an administrator session', async t => {
  server(t, 'user')
  await assert.rejects(new AdminSession().login('site', 'admin', 'test-password'), /管理权限/)
})

test('concurrent management requests share one refresh of the rotating credential', async t => {
  const fixture = server(t)
  const session = new AdminSession()
  await session.login('site', 'admin', 'test-password')
  await Promise.all(Array.from({ length: 20 }, () => session.request('/netgo/v2/admin/state')))
  assert.equal(fixture.paths.filter(path => path.endsWith('/refresh')).length, 1)
  assert.equal(fixture.paths.filter(path => path.endsWith('/admin/state')).length, 20)
  assert(session.active)
})

test('logout aborts a pending refresh and cannot revive the management session', async t => {
  server(t)
  const session = new AdminSession()
  await session.login('site', 'admin', 'test-password')
  const fetch = globalThis.fetch
  let entered, release, signal
  const started = new Promise(resolve => { entered = resolve })
  globalThis.fetch = (path, options) => {
    if (!path.endsWith('/refresh')) return fetch(path, options)
    signal = options.signal
    entered()
    return new Promise(resolve => { release = resolve })
  }
  const pending = session.request('/netgo/v2/admin/state')
  await started
  session.logout()
  assert(signal.aborted)
  release(new Response(JSON.stringify({ token: 'late-token', expires_at: new Date(Date.now() + 3600000).toISOString() })))
  await assert.rejects(pending, /取消/)
  assert.equal(session.active, false)
})

test('external URLs cannot receive management credentials', async t => {
  server(t)
  const session = new AdminSession()
  await session.login('site', 'admin', 'test-password')
  await assert.rejects(session.request('https://external.example/collect'), /无效/)
})

test('logout cancels a delayed login response', async t => {
  const original = globalThis.fetch
  let release, entered
  const started = new Promise(resolve => { entered = resolve })
  globalThis.fetch = () => { entered(); return new Promise(resolve => { release = resolve }) }
  t.after(() => { globalThis.fetch = original })
  const session = new AdminSession(), login = session.login('site', 'admin', 'password')
  await started; session.logout(); release(new Response(JSON.stringify({ id: 'tx', nonce: 'nonce' }), { status: 200 }))
  await assert.rejects(login, /取消/)
})

test('incompatible servers cannot receive administrator passwords', async t => {
  const original = globalThis.fetch, paths = []
  globalThis.fetch = async path => { paths.push(path); return new Response(JSON.stringify({ ...health, capabilities: ['native-auth-v1'] })) }
  t.after(() => { globalThis.fetch = original })
  await assert.rejects(new AdminSession().login('site','admin','secret'), /不兼容/)
  assert.deepEqual(paths, ['/netgo/v2/health'])
  assert.throws(() => validateHealth({ ...health, protocol: 3 }, 'site'), /不兼容/)
  assert.throws(() => validateHealth(health, 'another'), /身份/)
})

test('a delayed 401 for the previous token cannot log out a refreshed session', async t => {
 server(t)
 const upstream = globalThis.fetch
 let release, entered
 const started = new Promise(resolve => { entered=resolve })
 globalThis.fetch = async (path, options) => {
  if (path.endsWith('/slow')) { entered(); return new Promise(resolve => {release=resolve}) }
  const response = await upstream(path, options)
  if (path.endsWith('/exchange')) { const result=await response.json();result.expires_at=new Date(Date.now()+121000).toISOString();return new Response(JSON.stringify(result)) }
  return response
 }
 const original=globalThis.performance
 let clock=0
 Object.defineProperty(globalThis,'performance',{configurable:true,value:{now:()=>clock}})
 t.after(()=>Object.defineProperty(globalThis,'performance',{configurable:true,value:original}))
 const session=new AdminSession()
 await session.login('site','admin','password')
 const slow=session.request('/netgo/v2/admin/slow')
 await started;clock=2000
 await session.request('/netgo/v2/admin/state')
 release(new Response(JSON.stringify({error:'unauthorized'}),{status:401}))
 await assert.rejects(slow,/unauthorized/)
 assert(session.active)
 await session.request('/netgo/v2/admin/state')
})

test('old-token rejection waits for an in-flight successful refresh', async t => {
 server(t);const upstream=globalThis.fetch
 let rejectOld, releaseRefresh, slowEntered, refreshEntered
 const slowStarted=new Promise(r=>slowEntered=r), refreshStarted=new Promise(r=>refreshEntered=r)
 globalThis.fetch=async (path,options)=>{
  if(path.endsWith('/slow')){slowEntered();return new Promise(r=>rejectOld=r)}
  const response=await upstream(path,options)
  if(path.endsWith('/exchange')){const body=await response.json();body.expires_at=new Date(Date.now()+121000).toISOString();return new Response(JSON.stringify(body))}
  if(path.endsWith('/refresh')){refreshEntered();return new Promise(r=>releaseRefresh=()=>r(response))}
  return response
 }
 const original=globalThis.performance;let clock=0
 Object.defineProperty(globalThis,'performance',{configurable:true,value:{now:()=>clock}})
 t.after(()=>Object.defineProperty(globalThis,'performance',{configurable:true,value:original}))
 const session=new AdminSession();await session.login('site','admin','password')
 const slow=session.request('/netgo/v2/admin/slow');const rejected=assert.rejects(slow,/unauthorized/);await slowStarted;clock=2000
 const fresh=session.request('/netgo/v2/admin/state');await refreshStarted
 rejectOld(new Response(JSON.stringify({error:'unauthorized'}),{status:401}));await new Promise(setImmediate)
 assert(session.active);releaseRefresh();await fresh;await rejected;assert(session.active)
})
