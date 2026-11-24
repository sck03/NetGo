const enc = new TextEncoder()
export const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
const random = () => b64(crypto.getRandomValues(new Uint8Array(32)))
const digest = async (s: string) => new Uint8Array(await crypto.subtle.digest('SHA-256', enc.encode(s)))
const hex = (b: Uint8Array) => Array.from(b, x => x.toString(16).padStart(2, '0')).join('')

export function validateHealth(health: any, expectedSite = '') {
  const required = ['native-auth-v1', 'device-signature-v1', 'leases-v1', 'profile-revision-v1', 'admin-v1', 'diagnostics-v1']
  if (!health || health.protocol !== 2 || !Array.isArray(health.capabilities) || !required.every(x => health.capabilities.includes(x))) throw Error('服务端协议不兼容，请使用匹配版本的客户端和服务端')
  if (typeof health.site_id !== 'string' || !health.site_id || (expectedSite && health.site_id !== expectedSite)) throw Error('站点身份不一致')
  if (health.server_unix !== undefined) {
    if (!Number.isSafeInteger(health.server_unix) || health.server_unix < 946684800 || health.server_unix > 4102444800) throw Error('服务端时间格式无效')
  }
  return health.site_id as string
}

export class AdminSession {
  private key: CryptoKeyPair | null = null
  private token = ''
  private refreshToken = ''
  private expiresAt = 0
  private generation = 0
  private refreshing: Promise<void> | null = null
  private pending = new Set<AbortController>()
  private serverTime = 0
  private observedAt = 0
  private now() { return this.serverTime ? this.serverTime + performance.now() - this.observedAt : Date.now() }
  get active() { return !!this.token && !!this.key }

  private async send(path: string, method: string, input?: unknown) {
    if (!path.startsWith('/netgo/v2/') || path.includes('#') || path.includes('\n')) throw Error('无效的管理请求')
    const generation = this.generation
    const token = this.token, key = this.key
    const body = input === undefined ? '' : JSON.stringify(input)
    const headers: Record<string, string> = { 'Content-Type': 'application/json', 'X-NetGo-Protocol': '2' }
    if (token && key) {
      const stamp = String(Math.floor(this.now() / 1000)), nonce = random()
      const message = [method, path, stamp, nonce, hex(await digest(body)), hex(await digest(token))].join('\n')
      headers.Authorization = 'Bearer ' + token
      headers['X-NetGo-Time'] = stamp
      headers['X-NetGo-Nonce'] = nonce
      headers['X-NetGo-Signature'] = b64(new Uint8Array(await crypto.subtle.sign('Ed25519', key.privateKey, enc.encode(message))))
    }
    if (generation !== this.generation) throw Error('管理登录已取消')
    const controller = new AbortController()
    this.pending.add(controller)
    const timeout = setTimeout(() => controller.abort(), 20000)
    try {
      const response = await fetch(path, { method, headers, body: body || undefined, cache: 'no-store', redirect: 'error', signal: controller.signal })
      const result = await response.json()
      if (generation !== this.generation) throw Error('管理登录已取消')
      if (!response.ok) {
        if (response.status === 401) this.logout()
        throw Error(result.error || '请求失败')
      }
      return result
    } catch (error) {
      if (generation !== this.generation) throw Error('管理登录已取消')
      throw error
    } finally {
      clearTimeout(timeout)
      this.pending.delete(controller)
    }
  }

  private refresh() {
    if (this.refreshing) return this.refreshing
    const generation = this.generation, refreshToken = this.refreshToken
    const pending = this.send('/netgo/v2/auth/refresh', 'POST', { refresh_token: refreshToken }).then(fresh => {
      if (generation !== this.generation) throw Error('管理登录已取消')
      this.token = fresh.token
      this.refreshToken = fresh.refresh_token || ''
      this.expiresAt = Date.parse(fresh.expires_at)
    }).finally(() => { if (this.refreshing === pending) this.refreshing = null })
    this.refreshing = pending
    return pending
  }

  async request(path: string, method = 'GET', input?: unknown) {
    const generation = this.generation
    if (this.token && this.refreshToken && this.expiresAt - this.now() < 120000) {
      await this.refresh()
    }
    if (generation !== this.generation) throw Error('管理登录已取消')
    return this.send(path, method, input)
  }

  async login(siteID: string, alias: string, password: string) {
    this.logout()
    const generation = this.generation
    const health = await this.send('/netgo/v2/health', 'GET')
    siteID = validateHealth(health, siteID)
    if (generation !== this.generation) throw Error('管理登录已取消')
    if (health.server_unix !== undefined) { this.serverTime = health.server_unix * 1000; this.observedAt = performance.now() }
    const key = await crypto.subtle.generateKey({ name: 'Ed25519' }, false, ['sign', 'verify']) as CryptoKeyPair
    if (generation !== this.generation) throw Error('管理登录已取消')
    this.key = key
    const verifier = random()
    const publicKey = b64(new Uint8Array(await crypto.subtle.exportKey('raw', key.publicKey)))
    const begin = { client_type:'admin',site_id: siteID, public_key: publicKey, wireguard_key: btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32)))), challenge: b64(await digest(verifier)) }
    try {
      const tx = await this.send('/netgo/v2/auth/transactions', 'POST', begin)
      const proof = ['netgo-native-v1', siteID, tx.id, tx.nonce].join('\n')
      const sign = async (s: string) => b64(new Uint8Array(await crypto.subtle.sign('Ed25519', key.privateKey, enc.encode(s))))
      await this.send('/netgo/v2/auth/transactions/' + tx.id + '/complete', 'POST', { Alias: alias, Password: password, Signature: await sign(proof) })
      password = ''
      const result = await this.send('/netgo/v2/auth/transactions/' + tx.id + '/exchange', 'POST', { Verifier: verifier, Signature: await sign(proof + '\nexchange\n' + verifier) })
      if (generation !== this.generation) throw Error('管理登录已取消')
      if (result.user.role !== 'admin') throw Error('此账号没有管理权限')
      this.token = result.token
      this.refreshToken = result.refresh_token || ''
      this.expiresAt = Date.parse(result.expires_at)
      return result.user
    } catch (error) {
      if (generation === this.generation) this.logout()
      throw error
    }
  }

  logout() {
    this.generation++
    for (const controller of this.pending) controller.abort()
    this.pending.clear()
    this.refreshing = null
    this.token = ''
    this.refreshToken = ''
    this.expiresAt = 0
    this.key = null
    this.serverTime = this.observedAt = 0
  }
}
