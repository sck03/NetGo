<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { AdminSession } from '../../../packages/ui/auth'
import RemotePanel from './RemotePanel.vue'
const page = ref('overview'), alias = ref(''), password = ref(''), error = ref(''), notice = ref(''), busy = ref(false), logged = ref(false)
const state = ref<any>({ users: {}, resources: {}, devices: {}, leases: {}, audit: [] })
const admin = new AdminSession()
let timer: ReturnType<typeof setInterval> | undefined, refreshing = false
const users = computed<any[]>(() => Object.values(state.value.users))
const resources = computed<any[]>(() => Object.values(state.value.resources))
const devices = computed<any[]>(() => Object.values(state.value.devices).filter((d: any) => d.client_type !== 'admin'))
const leases = computed<any[]>(() => Object.values(state.value.leases))
const userForm = ref({ Alias: '', Email: '', Name: '' })
const resourceForm = ref({ id: '', name: '', cidr: '192.168.50.20/32', protocol: 'tcp', ports: '443', enabled: true })
async function action(fn: () => Promise<void>) {
  busy.value = true; error.value = ''; notice.value = ''
  try { await fn() } catch (e) { error.value = e instanceof Error ? e.message : String(e); if (logged.value && !admin.active) logout() } finally { busy.value = false }
}
async function refresh() {
  if (!logged.value || refreshing) return
  refreshing = true
  try { state.value = await admin.request('/netgo/v2/admin/state') } catch (e) { if (!admin.active) logout(); throw e } finally { refreshing = false }
}
async function login() {
  await action(async () => {
    try {
      await admin.login('', alias.value, password.value); logged.value = true; await refresh()
    } finally { password.value = '' }
  })
}
function logout() { admin.logout(); logged.value = false; state.value = { users: {}, resources: {}, devices: {}, leases: {}, audit: [] } }
async function saveUser(u: any) {
  await action(async () => {
    await admin.request('/netgo/v2/admin/users/' + encodeURIComponent(u.id), 'PUT', { ...u, max_sessions: u.max_sessions || 1 })
    await refresh(); notice.value = '账号设置已保存，旧会话已撤销。'
  })
}
async function createUser() {
  await action(async () => {
    const result = await admin.request('/netgo/v2/admin/users', 'POST', userForm.value)
    notice.value = result.initial_password ? '初始密码（仅本次显示）：' + result.initial_password : '账号已创建，请通过身份服务激活并设置密码。'
    userForm.value = { Alias: '', Email: '', Name: '' }; await refresh()
  })
}
async function deleteUser(user: any) {
  if (!window.confirm('删除账号 ' + user.alias + ' 及其设备登记？所有连接将被撤销。')) return
  await action(async () => {
    await admin.request('/netgo/v2/admin/users/' + encodeURIComponent(user.id), 'DELETE')
    await refresh(); notice.value = '账号及其设备已删除。'
  })
}
async function saveResource() {
  await action(async () => {
    const f = resourceForm.value
    const ports = f.protocol === 'icmp' ? [] : f.ports.split(',').map(x => Number(x.trim()))
    if (ports.some(x => !Number.isInteger(x) || x < 1 || x > 65535)) throw Error('端口必须为 1–65535 的整数')
    await admin.request('/netgo/v2/admin/resources/' + encodeURIComponent(f.id), 'PUT', { ...f, ports })
    await refresh(); notice.value = '资源已保存，请应用策略后重新连接以获取权限。'
  })
}
function download(value: any, name: string) {
  const a = document.createElement('a'), url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' }))
  a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
function expiryValue(value: string) { if (!value || value.startsWith('0001-')) return ''; const d = new Date(value); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function setExpiry(user: any, event: Event) { const value = (event.target as HTMLInputElement).value; user.expires_at = value ? new Date(value).toISOString() : '0001-01-01T00:00:00Z' }
onMounted(() => { timer = setInterval(() => refresh().catch(e => { error.value = String(e.message || e) }), 5000) })
onUnmounted(() => { if (timer) clearInterval(timer); admin.logout() })
</script>
<template>
  <div class="shell">
    <aside>
      <div class="brand"><span class="mark">N</span><div>NetGo<small>局域网接入管理</small></div></div>
      <div class="workspace">站点管理</div>
      <nav>
        <button :class="{ active: page === 'overview' }" @click="page = 'overview'">◉　站点概览</button>
        <template v-if="logged">
          <button :class="{ active: page === 'users' }" @click="page = 'users'">♙　账号与权限</button>
          <button :class="{ active: page === 'resources' }" @click="page = 'resources'">▦　局域网资源</button>
          <button :class="{ active: page === 'sessions' }" @click="page = 'sessions'">⇄　设备与会话</button>
          <button :class="{ active: page === 'remote' }" @click="page = 'remote'">▣　远程桌面</button>
          <button :class="{ active: page === 'diagnostics' }" @click="page = 'diagnostics'">≡　诊断与审计</button>
        </template>
      </nav>
      <div class="aside-bottom"><span class="dot"></span>本地身份 · 可选 VPS<small>NetGo {{ state.runtime?.version || '开发版' }}</small></div>
    </aside>
    <main>
      <header><div><div class="eyebrow">LOCAL NETWORK CONTROL</div><h1>{{ page === 'overview' ? '管理你的局域网接入' : page === 'users' ? '账号与访问权限' : page === 'resources' ? '局域网资源' : page === 'sessions' ? '设备与在线会话' : page === 'remote' ? '远程桌面与授权' : '管理审计' }}</h1></div><button v-if="logged" class="secondary" @click="logout">退出管理</button><span v-else class="badge">Linux 控制台</span></header>
      <div v-if="error" class="alert error" role="alert">{{ error }}</div><div v-if="notice" class="alert" role="status">{{ notice }}</div>
      <section v-if="!logged" class="card login-card">
        <div class="eyebrow">ADMIN ACCESS</div><h2>登录站点管理</h2><p>使用这台 Linux 上的管理员账号。</p>
        <form @submit.prevent="login"><label>账号<input v-model="alias" required autocomplete="username"></label><label>密码<input v-model="password" type="password" required autocomplete="current-password"></label><button class="primary" :disabled="busy">登录管理控制台 →</button></form>
        <p class="muted">本次认证信息保存在浏览器内存，退出管理或关闭页面后清除。</p>
      </section>
      <template v-else-if="page === 'overview'">
        <div class="stats"><section class="card"><span>账号</span><strong>{{ users.length }}</strong></section><section class="card"><span>局域网资源</span><strong>{{ resources.length }}</strong></section><section class="card"><span>有效租约</span><strong>{{ leases.length }}</strong></section></div>
        <section class="card"><h3>配置与运行状态</h3><dl><dt>配置版本</dt><dd>{{ state.revision }}</dd><dt>已应用版本</dt><dd>{{ state.observed_revision }}</dd><dt>最近结果</dt><dd>{{ state.last_error || '正常' }}</dd></dl>
          <div class="actions"><button class="primary" :disabled="busy" @click="action(async () => { await admin.request('/netgo/v2/admin/reconcile', 'POST', {}); await refresh(); notice = '资源策略已应用。' })">应用资源策略</button><button class="secondary" @click="action(async () => download(await admin.request('/netgo/v2/site'), 'netgo-site.json'))">导出 Windows 站点配置</button></div>
          <p class="muted">新增用户默认无访问权限。修改账号或资源会撤销关联的旧会话。</p>
        </section>
        <section class="card" aria-live="polite"><h3>服务检查与告警</h3>
          <p>{{ state.runtime?.status === 'ok' ? '最近服务检查通过，外部网络可达性需单独验证。' : '有待处理或尚未完成的检查，请查看诊断。' }}</p>
          <p v-for="alert in (state.runtime?.alerts || [])" :key="alert.code" class="alert" :class="{ error: alert.severity === 'critical' }">{{ alert.message }}</p>
          <button class="secondary" @click="page = 'diagnostics'">查看运行诊断</button>
        </section>
      </template>
      <template v-else-if="page === 'users'">
        <section class="card"><h3>创建普通账号</h3><form class="inline-form" @submit.prevent="createUser"><input v-model="userForm.Alias" required placeholder="短用户名"><input v-model="userForm.Email" type="email" required placeholder="邮箱"><input v-model="userForm.Name" required placeholder="显示名称"><button class="primary" :disabled="busy">创建账号</button></form></section>
        <section class="card"><h3>账号权限</h3><div v-for="u in users" :key="u.id" class="user-row"><div><strong>{{ u.alias }}</strong><small>{{ u.email }} · {{ u.role }}</small></div><label class="check"><input type="checkbox" v-model="u.disabled">禁用</label><label>设备上限<input class="number" v-model.number="u.max_devices" type="number" min="1" max="100"></label><label>并发上限<input class="number" v-model.number="u.max_sessions" type="number" min="1" max="100" :placeholder="String(u.max_sessions || 1)"></label><label>到期时间<input type="datetime-local" :value="expiryValue(u.expires_at)" @change="setExpiry(u, $event)"></label><label>授权资源<select v-model="u.resources" multiple><option v-for="r in resources" :value="r.id" :key="r.id">{{ r.name }}</option></select></label><button class="secondary" :disabled="busy" @click="saveUser(u)">保存并撤销旧会话</button><button class="secondary" :disabled="busy" @click="deleteUser(u)">删除账号</button></div></section>
      </template>
      <template v-else-if="page === 'resources'">
        <section class="card"><h3>添加或修改资源</h3><form class="resource-form" @submit.prevent="saveResource"><label>资源 ID<input v-model="resourceForm.id" required pattern="[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}"></label><label>名称<input v-model="resourceForm.name" required></label><label>IPv4 网段或主机<input v-model="resourceForm.cidr" required></label><label>协议<select v-model="resourceForm.protocol"><option>tcp</option><option>udp</option><option>icmp</option></select></label><label v-if="resourceForm.protocol !== 'icmp'">端口（逗号分隔）<input v-model="resourceForm.ports" required></label><label class="check"><input v-model="resourceForm.enabled" type="checkbox">启用</label><button class="primary" :disabled="busy">保存资源</button></form></section>
        <section class="card"><div v-for="r in resources" :key="r.id" class="resource"><strong>{{ r.name }}</strong><code>{{ r.cidr }}</code><span>{{ r.protocol }} {{ r.ports.join(', ') }} · {{ r.enabled ? '启用' : '停用' }}</span><button class="secondary" @click="resourceForm = { ...r, ports: r.ports.join(',') }">编辑</button><button class="secondary" :disabled="busy" @click="action(async () => { await admin.request('/netgo/v2/admin/resources/' + encodeURIComponent(r.id), 'DELETE'); await refresh() })">删除</button></div><div v-if="!resources.length" class="empty">尚未配置资源。</div></section>
      </template>
      <template v-else-if="page === 'sessions'">
        <section class="card"><h3>正在授权的会话</h3><div v-for="l in leases" :key="l.id" class="resource"><strong>{{ state.users[l.user_id]?.alias }}</strong><code>{{ l.ip }}</code><span>{{ new Date(l.expires_at).toLocaleString() }}</span><button class="secondary" :disabled="busy" @click="action(async () => { await admin.request('/netgo/v2/admin/sessions/' + l.id, 'DELETE'); await refresh() })">立即撤销</button></div><div v-if="!leases.length" class="empty">当前没有有效会话。</div></section>
        <section class="card"><h3>已登记设备</h3><div v-for="d in devices" :key="d.id" class="resource"><strong>{{ state.users[d.user_id]?.alias }}</strong><code>{{ d.id.slice(0, 12) }}</code><span>{{ d.ip || '待连接' }}</span><small>{{ new Date(d.created_at).toLocaleString() }}</small><button class="secondary" :disabled="busy" @click="action(async () => { await admin.request('/netgo/v2/admin/devices/' + encodeURIComponent(d.id), 'DELETE'); await refresh(); notice = '设备登记及其会话已撤销。' })">撤销设备</button></div></section>
      </template>
      <RemotePanel v-else-if="page === 'remote'" :users="state.users" :request="(path, method, body) => admin.request(path, method, body)" />
      <template v-else-if="page === 'diagnostics'">
        <section class="card"><h3>运行诊断</h3><p>最近检查：{{ state.runtime?.sampled_at && !state.runtime.sampled_at.startsWith('0001-') ? new Date(state.runtime.sampled_at).toLocaleString() : '等待采样' }}</p>
          <dl><dt>运行时间</dt><dd>{{ state.runtime?.uptime_seconds || 0 }} 秒</dd><dt>进程堆内存</dt><dd>{{ Math.round((state.runtime?.process_heap_bytes || 0) / 1048576) }} MiB</dd><dt>近一分钟认证失败</dt><dd>{{ state.runtime?.authentication_failures_last_minute || 0 }}</dd></dl>
          <table><thead><tr><th>检查</th><th>状态</th><th>说明</th></tr></thead><tbody><tr v-for="check in (state.runtime?.checks || [])" :key="check.name"><td>{{ check.name }}</td><td>{{ check.status }}</td><td>{{ check.detail }}</td></tr></tbody></table>
          <button class="secondary" :disabled="busy" @click="action(async () => download(await admin.request('/netgo/v2/admin/diagnostics'), 'netgo-diagnostics.json'))">导出脱敏诊断</button>
        </section>
        <section class="card"><h3>管理审计</h3><table><thead><tr><th>时间</th><th>操作</th><th>对象</th></tr></thead><tbody><tr v-for="(a, i) in [...(state.audit || [])].reverse().slice(0, 100)" :key="i"><td>{{ new Date(a.at).toLocaleString() }}</td><td>{{ a.action }}</td><td class="truncate">{{ a.object }}</td></tr></tbody></table></section>
      </template>
      <footer>NetGo · 账号保留在本地，连接由你掌控。</footer>
    </main>
  </div>
</template>
