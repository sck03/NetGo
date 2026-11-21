import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, webcrypto } from 'node:crypto'
import { AdminSession } from '../../packages/ui/auth.ts'
const enc = new TextEncoder()
const unb64 = s => Buffer.from(s, 'base64url')
const sha = s => createHash('sha256').update(s).digest('hex')

function server(t, role = 'admin') {
  const original = globalThis.fetch
  let publicKey, begin, token = 'first-token', sawRefresh = false
  const paths = []
  globalThis.fetch = async (path, options) => {
    paths.push(path)
    assert.equal(options.redirect, 'error')
    assert.equal(options.cache, 'no-store')
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
      const proof = [options.method, path, h['X-NetGo-Time'], h['X-NetGo-Nonce'], sha(body), sha(token)].join('\n')
      assert.equal(h.Authorization, 'Bearer ' + token)
      assert(await webcrypto.subtle.verify('Ed25519', publicKey, unb64(h['X-NetGo-Signature']), enc.encode(proof)))
      if (path.endsWith('/refresh')) { assert.equal(data.refresh_token, 'refresh'); sawRefresh = true; token = 'renewed-token' }
    }
    let result = { ok: true }
    if (path.endsWith('/transactions')) result = { id: 'tx', nonce: 'nonce' }
    if (path.endsWith('/exchange') || path.endsWith('/refresh')) result = { token, refresh_token: 'refresh', expires_at: new Date(Date.now() + (sawRefresh ? 3600000 : 60000)).toISOString(), user: { role } }
    return new Response(JSON.stringify(result), { status: 200 })
  }
  t.after(() => { globalThis.fetch = original })
  return { paths, refreshed: () => sawRefresh }
}

test('management login proves PKCE/key ownership and refresh signs the old token', async t => {
  const fixture = server(t)
  const session = new AdminSession()
  await session.login('site', 'admin', 'test-password')
  await session.request('/netgo/v1/admin/state')
  assert(fixture.refreshed())
  assert(fixture.paths.includes('/netgo/v1/admin/state'))
})

test('ordinary users cannot retain an administrator session', async t => {
  server(t, 'user')
  await assert.rejects(new AdminSession().login('site', 'admin', 'test-password'), /管理权限/)
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
