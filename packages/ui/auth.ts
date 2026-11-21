const enc = new TextEncoder()
export const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
const random = () => b64(crypto.getRandomValues(new Uint8Array(32)))
const digest = async (s: string) => new Uint8Array(await crypto.subtle.digest('SHA-256', enc.encode(s)))
const hex = (b: Uint8Array) => Array.from(b, x => x.toString(16).padStart(2, '0')).join('')

export class AdminSession {
  private key: CryptoKeyPair | null = null
  private token = ''
  private refreshToken = ''
  private expiresAt = 0
  private generation = 0
  get active() { return !!this.token && !!this.key }

  private async send(path: string, method: string, input?: unknown) {
    if (!path.startsWith('/netgo/v1/') || path.includes('#') || path.includes('\n')) throw Error('无效的管理请求')
    const generation = this.generation
    const body = input === undefined ? '' : JSON.stringify(input)
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (this.token && this.key) {
      const stamp = String(Math.floor(Date.now() / 1000)), nonce = random()
      const message = [method, path, stamp, nonce, hex(await digest(body)), hex(await digest(this.token))].join('\n')
      headers.Authorization = 'Bearer ' + this.token
      headers['X-NetGo-Time'] = stamp
      headers['X-NetGo-Nonce'] = nonce
      headers['X-NetGo-Signature'] = b64(new Uint8Array(await crypto.subtle.sign('Ed25519', this.key.privateKey, enc.encode(message))))
    }
    if (generation !== this.generation) throw Error('管理登录已取消')
    const response = await fetch(path, { method, headers, body: body || undefined, cache: 'no-store', redirect: 'error' })
    const result = await response.json()
    if (generation !== this.generation) throw Error('管理登录已取消')
    if (!response.ok) {
      if (response.status === 401) this.logout()
      throw Error(result.error || '请求失败')
    }
    return result
  }

  async request(path: string, method = 'GET', input?: unknown) {
    if (this.token && this.refreshToken && this.expiresAt - Date.now() < 120000) {
      const fresh = await this.send('/netgo/v1/auth/refresh', 'POST', { refresh_token: this.refreshToken })
      this.token = fresh.token
      this.refreshToken = fresh.refresh_token || ''
      this.expiresAt = Date.parse(fresh.expires_at)
    }
    return this.send(path, method, input)
  }

  async login(siteID: string, alias: string, password: string) {
    this.logout()
    const generation = this.generation
    const key = await crypto.subtle.generateKey({ name: 'Ed25519' }, false, ['sign', 'verify']) as CryptoKeyPair
    if (generation !== this.generation) throw Error('管理登录已取消')
    this.key = key
    const verifier = random()
    const publicKey = b64(new Uint8Array(await crypto.subtle.exportKey('raw', key.publicKey)))
    const begin = { client_type:'admin',site_id: siteID, public_key: publicKey, wireguard_key: btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32)))), challenge: b64(await digest(verifier)) }
    try {
      const tx = await this.send('/netgo/v1/auth/transactions', 'POST', begin)
      const proof = ['netgo-native-v1', siteID, tx.id, tx.nonce].join('\n')
      const sign = async (s: string) => b64(new Uint8Array(await crypto.subtle.sign('Ed25519', key.privateKey, enc.encode(s))))
      await this.send('/netgo/v1/auth/transactions/' + tx.id + '/complete', 'POST', { Alias: alias, Password: password, Signature: await sign(proof) })
      password = ''
      const result = await this.send('/netgo/v1/auth/transactions/' + tx.id + '/exchange', 'POST', { Verifier: verifier, Signature: await sign(proof + '\nexchange\n' + verifier) })
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
    this.token = ''
    this.refreshToken = ''
    this.expiresAt = 0
    this.key = null
  }
}
