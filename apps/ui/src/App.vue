<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { AdminSession } from '../../../packages/ui/auth'
import { LatestQuery } from '../../../packages/ui/query'
import RemotePanel from './RemotePanel.vue'
import ResourcePanel from './ResourcePanel.vue'
import UserPanel from './UserPanel.vue'
import SessionPanel from './SessionPanel.vue'
import ListPager from './ListPager.vue'
import type { User, AdminState, AuditPage } from '../../../packages/ui/contracts'
import { emptyState } from '../../../packages/ui/contracts'
import type { Resource } from '../../../packages/ui/resources'
const page = ref('overview'), alias = ref(''), password = ref(''), error = ref(''), notice = ref(''), busy = ref(false), logged = ref(false)
const state = ref<AdminState>(emptyState())
const pageReady = ref(false)
const initialPassword = ref('')
const audit = ref<AuditPage>({events:[],next_before:0}), auditBefore = ref(0)
const auditQuery = new LatestQuery(() => admin.request('/netgo/v2/admin/audit' + (auditBefore.value ? '?before=' + auditBefore.value : '')), value => { audit.value = value })
async function auditPage(before = 0) { auditQuery.invalidate(); auditBefore.value=before; await auditQuery.run() }
const admin = new AdminSession(active => {
  logged.value = active
  if (!active) clearSession()
})
let timer: ReturnType<typeof setInterval> | undefined
const listAfter = ref('')
const stateQuery = new LatestQuery(() => admin.request(['overview', 'diagnostics', 'sessions'].includes(page.value)
  ? '/netgo/v2/admin/summary'
  : '/netgo/v2/admin/state?section=' + page.value + '&after=' + encodeURIComponent(listAfter.value)), value => { state.value = { ...emptyState(), ...value }; pageReady.value = true })
async function listPage(after = '') { listAfter.value = after; await refresh(true) }
const users = computed(() => Object.values(state.value.users))
const resources = computed(() => Object.values(state.value.resources))
async function action(fn: () => Promise<void>) {
  if (busy.value) return false
  stateQuery.invalidate()
  busy.value = true; error.value = ''; notice.value = ''; initialPassword.value = ''
  try { await fn(); return true } catch (e) { error.value = e instanceof Error ? e.message : String(e); if (logged.value && !admin.active) logout(); return false } finally { busy.value = false }
}
async function refresh(fresh = false) {
  if (!logged.value) return
  if (fresh) { stateQuery.invalidate(); auditQuery.invalidate() }
  try { await stateQuery.run(); if (page.value === 'diagnostics') await auditQuery.run() } catch (e) { if (!admin.active) logout(); throw e }
}
async function login() {
  await action(async () => {
    try {
      await admin.login('', alias.value, password.value); logged.value = true; await refresh(true)
    } finally { password.value = '' }
  })
}
function clearSession() {
  pageReady.value = false
  stateQuery.invalidate(); auditQuery.invalidate()
  state.value = emptyState(); listAfter.value = ''; audit.value = { events: [], next_before: 0 }; auditBefore.value = 0
  notice.value = ''; initialPassword.value = ''; password.value = ''; error.value = ''; page.value = 'overview'
}
function logout() { admin.logout() }
async function saveUser(u: User) {
  return action(async () => {
    await admin.request('/netgo/v2/admin/users/' + encodeURIComponent(u.id), 'PUT', u)
    await refresh(true); notice.value = '账号设置已保存，旧会话已撤销。'
  })
}
async function createUser(account: { Alias: string; Email: string; Name: string }) {
  return action(async () => {
    const result = await admin.request('/netgo/v2/admin/users', 'POST', account)
    initialPassword.value = result.initial_password || ''
    notice.value = result.initial_password ? '账号已创建，请保存下方初始密码。' : '账号已创建，请通过身份服务激活并设置密码。'
    await refresh(true)
  })
}
async function deleteUser(user: User) {
  if (!window.confirm('删除账号 ' + user.alias + ' 及其设备登记？所有连接将被撤销。')) return false
  return action(async () => {
    const result = await admin.request('/netgo/v2/admin/users/' + encodeURIComponent(user.id), 'DELETE')
    await refresh(true); notice.value = result.cleanup_pending ? '账号访问已撤销，等待上游清理，后台将自动重试。' : '账号及其设备已删除。'
  })
}
async function saveResource(resource: Resource) {
  return action(async () => {
    await admin.request('/netgo/v2/admin/resources/' + encodeURIComponent(resource.id), 'PUT', resource)
    await refresh(true); notice.value = '资源已保存，服务会自动应用策略；请到账号与权限中勾选此资源。'
  })
}
async function deleteResource(resource: Resource) {
  return action(async () => {
    await admin.request('/netgo/v2/admin/resources/' + encodeURIComponent(resource.id), 'DELETE')
    await refresh(true); notice.value = '资源已删除，服务会自动更新策略。'
  })
}
function exportSite() { return action(async () => download(await admin.request('/netgo/v2/site'), 'netgo-site.json')) }
function download(value: any, name: string) {
  const a = document.createElement('a'), url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' }))
  a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
watch(page, () => { initialPassword.value = ''; pageReady.value = false; stateQuery.invalidate(); auditQuery.invalidate(); listAfter.value = ''; state.value = emptyState(); if (logged.value) refresh().catch(e => { error.value = String(e.message || e) }) })
onMounted(() => { timer = setInterval(() => { if (!document.hidden && !busy.value && ['overview','diagnostics'].includes(page.value)) refresh().catch(e => { error.value = String(e.message || e) }) }, 5000) })
onUnmounted(() => { if (timer) clearInterval(timer); stateQuery.invalidate(); auditQuery.invalidate(); admin.logout() })
</script>
<template>
  <div class="shell">
    <aside>
      <div class="brand"><span class="mark">N</span><div>NetGo<small>局域网接入管理</small></div></div>
      <div class="workspace">站点管理</div>
      <nav aria-label="站点管理导航">
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
      <div v-if="error" class="alert error" role="alert">{{ error }}</div><div v-if="logged && notice" class="alert" role="status">{{ notice }}</div>
      <div v-if="logged && initialPassword" class="alert" role="status">初始密码（仅本次显示）：<code>{{ initialPassword }}</code> <button class="secondary" @click="initialPassword = ''">已保存，隐藏密码</button></div>
      <section v-if="!logged" class="card login-card">
        <div class="eyebrow">ADMIN ACCESS</div><h2>登录站点管理</h2><p>使用这台 Linux 上的管理员账号。</p>
        <form @submit.prevent="login"><label>账号<input v-model="alias" required autocomplete="username"></label><label>密码<input v-model="password" type="password" required autocomplete="current-password"></label><button class="primary" :disabled="busy">登录管理控制台 →</button></form>
        <p class="muted">本次认证信息保存在浏览器内存，退出管理或关闭页面后清除。</p>
      </section>
      <section v-else-if="!pageReady" class="card" role="status">
        <p>{{ error ? '页面尚未加载，请重试。' : '正在加载页面…' }}</p>
        <button v-if="error" class="secondary" @click="action(() => refresh(true))">重新加载</button>
      </section>
      <template v-else-if="page === 'overview'">
        <div class="stats"><section class="card"><span>账号</span><strong>{{ state.counts?.users || 0 }}</strong></section><section class="card"><span>局域网资源</span><strong>{{ state.counts?.resources || 0 }}</strong></section><section class="card"><span>有效租约</span><strong>{{ state.counts?.leases || 0 }}</strong></section></div>
        <section class="card"><h3>从这里开始</h3><div class="quick-start">
          <button class="secondary" @click="page = 'resources'"><strong>1 · 添加资源</strong><small>选择网页、文件共享或终端等常用服务</small></button>
          <button class="secondary" @click="page = 'users'"><strong>2 · 创建账号并授权</strong><small>勾选每个用户可以访问的资源</small></button>
          <button class="secondary" :disabled="busy" @click="exportSite"><strong>3 · 导出客户端站点文件</strong><small>用户导入文件后登录，自动选择连接入口</small></button>
          <button class="secondary" @click="page = 'remote'"><strong>远程桌面</strong><small>登记主机并授予查看或控制权限</small></button>
        </div></section>
        <section class="card"><h3>访问策略</h3><p>{{ state.last_error ? '策略应用遇到问题，请查看诊断或重试。' : state.revision === state.observed_revision ? '当前策略已生效。' : '设置已保存，正在自动应用策略。' }}</p>
          <button v-if="state.last_error" class="secondary" :disabled="busy" @click="action(async () => { await admin.request('/netgo/v2/admin/reconcile', 'POST', {}); await refresh(true); notice = '资源策略已应用。' })">重试应用</button>
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
      <ResourcePanel v-else-if="page === 'resources' && state.network" :resources="resources" :network="state.network" :busy="busy" :save="saveResource" :remove="deleteResource" />
      <SessionPanel v-else-if="page === 'sessions'" :request="(path, method, body) => admin.request(path, method, body)" />
      <RemotePanel v-else-if="page === 'remote'" :users="state.users" :request="(path, method, body) => admin.request(path, method, body)" />
      <template v-else-if="page === 'diagnostics'">
        <section class="card"><h3>运行诊断</h3><p>最近检查：{{ state.runtime?.sampled_at && !state.runtime.sampled_at.startsWith('0001-') ? new Date(state.runtime.sampled_at).toLocaleString() : '等待采样' }}</p>
          <dl><dt>运行时间</dt><dd>{{ state.runtime?.uptime_seconds || 0 }} 秒</dd><dt>进程堆内存</dt><dd>{{ Math.round((state.runtime?.process_heap_bytes || 0) / 1048576) }} MiB</dd><dt>并发任务</dt><dd>{{ state.runtime?.process_goroutines || 0 }}</dd><dt>累计请求 / 错误</dt><dd>{{ state.runtime?.requests || 0 }} / {{ state.runtime?.request_errors || 0 }}</dd><dt>累计 CPU 时间</dt><dd>{{ (state.runtime?.host_resources?.process_cpu_seconds || 0).toFixed(1) }} 秒</dd><dt>峰值驻留内存</dt><dd>{{ Math.round((state.runtime?.host_resources?.process_max_rss_bytes || 0) / 1048576) }} MiB</dd><dt>数据库磁盘可用</dt><dd>{{ ((state.runtime?.host_resources?.database_disk_available_bytes || 0) / 1073741824).toFixed(1) }} GiB</dd><dt>近一分钟认证失败</dt><dd>{{ state.runtime?.authentication_failures_last_minute || 0 }}</dd></dl>
          <table><thead><tr><th>检查</th><th>状态</th><th>说明</th></tr></thead><tbody><tr v-for="check in (state.runtime?.checks || [])" :key="check.name"><td>{{ check.name }}</td><td>{{ check.status }}</td><td>{{ check.detail }}</td></tr></tbody></table>
          <button class="secondary" :disabled="busy" @click="action(async () => download(await admin.request('/netgo/v2/admin/diagnostics'), 'netgo-diagnostics.json'))">导出脱敏诊断</button>
        </section>
        <section class="card"><h3>管理审计</h3><table><thead><tr><th>时间</th><th>操作</th><th>对象</th></tr></thead><tbody><tr v-for="a in audit.events" :key="a.sequence"><td>{{ new Date(a.at).toLocaleString() }}</td><td>{{ a.action }}</td><td class="truncate">{{ a.object }}</td></tr></tbody></table><button class="secondary" :disabled="busy || !auditBefore" @click="action(() => auditPage())">最新记录</button><button class="secondary" :disabled="busy || !audit.next_before" @click="action(() => auditPage(audit.next_before))">更早记录</button></section>
      </template>
      <ListPager v-if="logged && ['users','resources'].includes(page)" :after="listAfter" :next="state.next_after || ''" :busy="busy" @navigate="action(() => listPage($event))" />
      <footer>NetGo · 账号保留在本地，连接由你掌控。</footer>
    </main>
  </div>
</template>
