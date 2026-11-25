<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { AdminSession } from '../../../packages/ui/auth'
import { LatestQuery } from '../../../packages/ui/query'
import RemotePanel from './RemotePanel.vue'
import ResourcePanel from './ResourcePanel.vue'
import UserPanel from './UserPanel.vue'
import type { Resource } from '../../../packages/ui/resources'
const page = ref('overview'), alias = ref(''), password = ref(''), error = ref(''), notice = ref(''), busy = ref(false), logged = ref(false)
const state = ref<any>({ users: {}, resources: {}, devices: {}, leases: {}, audit: [] })
const admin = new AdminSession()
let timer: ReturnType<typeof setInterval> | undefined
const stateQuery = new LatestQuery(() => admin.request('/netgo/v2/admin/state'), value => { state.value = value })
const users = computed<any[]>(() => Object.values(state.value.users))
const resources = computed<any[]>(() => Object.values(state.value.resources))
const devices = computed<any[]>(() => Object.values(state.value.devices).filter((d: any) => d.client_type !== 'admin'))
const leases = computed<any[]>(() => Object.values(state.value.leases))
async function action(fn: () => Promise<void>) {
  if (busy.value) return false
  stateQuery.invalidate()
  busy.value = true; error.value = ''; notice.value = ''
  try { await fn(); return true } catch (e) { error.value = e instanceof Error ? e.message : String(e); if (logged.value && !admin.active) logout(); return false } finally { busy.value = false }
}
async function refresh() {
  if (!logged.value) return
  try { await stateQuery.run() } catch (e) { if (!admin.active) logout(); throw e }
}
async function login() {
  await action(async () => {
    try {
      await admin.login('', alias.value, password.value); logged.value = true; await refresh()
    } finally { password.value = '' }
  })
}
function logout() { stateQuery.invalidate(); admin.logout(); logged.value = false; state.value = { users: {}, resources: {}, devices: {}, leases: {}, audit: [] } }
async function saveUser(u: any) {
  return action(async () => {
    await admin.request('/netgo/v2/admin/users/' + encodeURIComponent(u.id), 'PUT', { ...u, max_sessions: u.max_sessions || 1 })
    await refresh(); notice.value = '账号设置已保存，旧会话已撤销。'
  })
}
async function createUser(account: { Alias: string; Email: string; Name: string }) {
  return action(async () => {
    const result = await admin.request('/netgo/v2/admin/users', 'POST', account)
    notice.value = result.initial_password ? '初始密码（仅本次显示）：' + result.initial_password : '账号已创建，请通过身份服务激活并设置密码。'
    await refresh()
  })
}
async function deleteUser(user: any) {
  if (!window.confirm('删除账号 ' + user.alias + ' 及其设备登记？所有连接将被撤销。')) return false
  return action(async () => {
    await admin.request('/netgo/v2/admin/users/' + encodeURIComponent(user.id), 'DELETE')
    await refresh(); notice.value = '账号及其设备已删除。'
  })
}
async function saveResource(resource: Resource) {
  return action(async () => {
    await admin.request('/netgo/v2/admin/resources/' + encodeURIComponent(resource.id), 'PUT', resource)
    await refresh(); notice.value = '资源已保存，服务会自动应用策略；请到账号与权限中勾选此资源。'
  })
}
async function deleteResource(resource: Resource) {
  return action(async () => {
    await admin.request('/netgo/v2/admin/resources/' + encodeURIComponent(resource.id), 'DELETE')
    await refresh(); notice.value = '资源已删除，服务会自动更新策略。'
  })
}
function exportSite() { return action(async () => download(await admin.request('/netgo/v2/site'), 'netgo-site.json')) }
function download(value: any, name: string) {
  const a = document.createElement('a'), url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' }))
  a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
watch(page, () => { if (logged.value && !busy.value) refresh().catch(e => { error.value = String(e.message || e) }) })
onMounted(() => { timer = setInterval(() => { if (!busy.value) refresh().catch(e => { error.value = String(e.message || e) }) }, 5000) })
onUnmounted(() => { if (timer) clearInterval(timer); stateQuery.invalidate(); admin.logout() })
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
        <section class="card"><h3>从这里开始</h3><div class="quick-start">
          <button class="secondary" @click="page = 'resources'"><strong>1 · 添加资源</strong><small>选择网页、文件共享或终端等常用服务</small></button>
          <button class="secondary" @click="page = 'users'"><strong>2 · 创建账号并授权</strong><small>勾选每个用户可以访问的资源</small></button>
          <button class="secondary" :disabled="busy" @click="exportSite"><strong>3 · 导出客户端站点文件</strong><small>用户导入文件后登录，自动选择连接入口</small></button>
          <button class="secondary" @click="page = 'remote'"><strong>远程桌面</strong><small>登记主机并授予查看或控制权限</small></button>
        </div></section>
        <section class="card"><h3>访问策略</h3><p>{{ state.last_error ? '策略应用遇到问题，请查看诊断或重试。' : state.revision === state.observed_revision ? '当前策略已生效。' : '设置已保存，正在自动应用策略。' }}</p>
          <button v-if="state.last_error" class="secondary" :disabled="busy" @click="action(async () => { await admin.request('/netgo/v2/admin/reconcile', 'POST', {}); await refresh(); notice = '资源策略已应用。' })">重试应用</button>
          <details><summary>技术信息</summary><dl><dt>配置版本</dt><dd>{{ state.revision }}</dd><dt>已应用版本</dt><dd>{{ state.observed_revision }}</dd><dt>最近结果</dt><dd>{{ state.last_error || '正常' }}</dd></dl></details>
          <p class="muted">新增账号默认无访问权限。修改权限会撤销相关连接，用户重新连接后获取新权限。</p>
        </section>
        <section class="card" aria-live="polite"><h3>服务检查与告警</h3>
          <p>{{ state.runtime?.status === 'ok' ? '最近服务检查通过，外部网络可达性需单独验证。' : '有待处理或尚未完成的检查，请查看诊断。' }}</p>
          <p v-for="alert in (state.runtime?.alerts || [])" :key="alert.code" class="alert" :class="{ error: alert.severity === 'critical' }">{{ alert.message }}</p>
          <button class="secondary" @click="page = 'diagnostics'">查看运行诊断</button>
        </section>
      </template>
      <UserPanel v-else-if="page === 'users'" :users="users" :resources="resources" :busy="busy" :create="createUser" :save="saveUser" :remove="deleteUser" />
      <ResourcePanel v-else-if="page === 'resources'" :resources="resources" :busy="busy" :save="saveResource" :remove="deleteResource" />
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
