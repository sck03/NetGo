<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { hostStatusLabel, remotePermissions as permissions } from '../../../packages/ui/remote'
import { LatestQuery } from '../../../packages/ui/query'
import RemoteHistory from './RemoteHistory.vue'
import ListPager from './ListPager.vue'
import RemoteGrants from './RemoteGrants.vue'
import type { RemoteHost as Host, RemoteInventory, AdminRequest } from '../../../packages/ui/contracts'
const props = defineProps<{ request: AdminRequest }>()
const inventory = ref<RemoteInventory>({ hosts: [], sessions: [], users: {}, next_after: '' })
const after = ref('')
const error = ref(''), loadError = ref(''), notice = ref(''), busy = ref(false), loading = ref(false), loaded = ref(false)
const search = ref(''), statusFilter = ref('all'), updated = ref('')
const hostID = ref('')
const activeSessions = computed(() => inventory.value.sessions.filter(s => s.state !== 'closed'))
const hostNames = computed(() => Object.fromEntries(inventory.value.hosts.map(h => [h.id, h.name])))
const readyCount = computed(() => inventory.value.hosts.filter(h => h.status === 'ready').length)
const filteredHosts = computed(() => inventory.value.hosts.filter(h => statusFilter.value === 'all' || h.status === statusFilter.value))
const label = (bits: number) => permissions.filter(p => bits & p.bit).map(p => p.name).join('、')
const hints: Record<string, string> = {
  ready: '使用客户端选择此主机，开始远程协助。', busy: '已有远控会话，结束后即可再次连接。',
  offline: '请在对方电脑上重新开启远程桌面共享。', disabled: '请让对方在客户端点击“允许本机远控”。',
  vpn_offline: '请让对方先登录站点并连接 VPN。',
}
function chooseHost(host: Host) {
  hostID.value = host.id
  document.getElementById('remote-grant')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
let alive = true, refreshVersion = 0, timer: ReturnType<typeof setInterval> | undefined
const query = new LatestQuery(signal => props.request<RemoteInventory>('/netgo/v2/admin/remote/inventory?after=' + encodeURIComponent(after.value) + '&q=' + encodeURIComponent(search.value), 'GET', undefined, signal), data => {
  inventory.value = data; loadError.value = ''; loaded.value = true; updated.value = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
})
async function refresh() {
  if (busy.value || !alive || document.hidden) return
  const version = ++refreshVersion
  loading.value = true
  try { await query.run() }
  catch (e) { if (alive) loadError.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive && version === refreshVersion) loading.value = false }
}
async function change(path: string, method: string, body?: unknown, message = '操作已完成。') {
  if (busy.value) return
  busy.value = true; query.invalidate(); error.value = ''; notice.value = ''
  try { await props.request(path, method, body); if (alive) notice.value = message; return true }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e); return false }
  finally { if (alive) { busy.value = false; await refresh() } }
}
let searchTimer: ReturnType<typeof setTimeout> | undefined
async function navigate(cursor = '') {
  query.invalidate(); after.value = cursor
  hostID.value = ''; statusFilter.value = 'all'
  await refresh()
}
watch(search, () => {
  query.invalidate()
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { void navigate() }, 250)
})
onMounted(() => { void refresh(); timer = setInterval(refresh, 5000) })
onUnmounted(() => { alive = false; query.invalidate(); if (timer) clearInterval(timer); if (searchTimer) clearTimeout(searchTimer) })
</script>
<template>
  <div class="panel-heading remote-heading"><div><h2>让远程协助更简单</h2><p>对方开启共享 → 管理员授予桌面权限 → 用户在客户端连接。</p></div><button class="secondary" :disabled="busy || loading" @click="refresh">{{ loading ? '刷新中…' : '刷新状态' }}</button></div>
  <p v-if="error || loadError" class="alert error" role="alert">{{ error || loadError }}<span v-if="loaded && loadError"> · 当前显示上次成功获取的数据。</span></p>
  <p v-if="notice" class="alert" role="status">{{ notice }}</p>
  <div class="stats remote-stats"><section class="card"><span>本页电脑</span><strong>{{ loaded ? inventory.hosts.length : '—' }}</strong></section><section class="card"><span>本页可连接</span><strong>{{ loaded ? readyCount : '—' }}</strong></section><section class="card"><span>本页会话</span><strong>{{ loaded ? activeSessions.length : '—' }}</strong></section></div>
  <section class="card" :aria-busy="loading">
    <div class="panel-heading"><div><h3>远程主机</h3><p>桌面权限需要单独授予。这里管理授权，实际画面在 NetGo 客户端打开。</p></div><small class="muted" v-if="updated">更新于 {{ updated }}</small></div>
    <div class="filter-bar"><label class="search-field">查找电脑<input v-model="search" type="search" maxlength="128" placeholder="搜索所有电脑的名称或系统"></label><label>本页状态<select v-model="statusFilter"><option value="all">全部状态</option><option value="ready">可连接</option><option value="busy">正在使用</option><option value="offline">主机失联</option><option value="vpn_offline">VPN 未连接</option><option value="disabled">共享已关闭</option></select></label></div>
    <div class="host-grid">
      <article v-for="host in filteredHosts" :key="host.id" class="host-card" :class="{ selected: hostID === host.id }">
        <div class="panel-heading"><div class="host-identity"><span class="device-icon" aria-hidden="true">▣</span><div><h4>{{ host.name }}</h4><small>{{ host.platform }}</small></div></div><span class="status-pill" :class="host.status">{{ hostStatusLabel(host.status) }}</span></div>
        <p class="host-hint">{{ hints[host.status] || '请刷新状态，或检查客户端版本。' }}</p>
        <div class="capability-tags"><span v-for="permission in permissions.filter(p => host.capabilities & p.bit)" :key="permission.bit">{{ permission.name }}</span></div>
        <div class="host-footer"><small>{{ host.require_consent ? '每次连接需要对方同意' : '允许已授权的无人值守连接' }}</small><button class="secondary" :disabled="busy" @click="chooseHost(host)">设置授权</button></div>
      </article>
    </div>
    <div v-if="!filteredHosts.length" class="empty"><strong>{{ !loaded ? (loadError ? '暂时无法读取主机' : '正在读取主机…') : (search.trim() || inventory.hosts.length) ? '没有匹配的电脑' : '还没有共享的电脑' }}</strong><p v-if="loaded && !inventory.hosts.length && !search.trim()">让对方连接 VPN，在客户端打开远程桌面并选择“允许本机远控”，然后刷新此页。</p></div>
  </section>
  <ListPager :after="after" :next="inventory.next_after" :busy="busy || loading" @navigate="navigate" />
  <RemoteGrants v-model:host-id="hostID" :hosts="inventory.hosts" :request="request" @changed="refresh" />
  <section class="card"><h3>本页电脑正在进行的远控</h3><div v-for="session in activeSessions" :key="session.id" class="access-row"><div><strong>{{ inventory.users[session.user_id] || session.user_id }}</strong><small>连接 {{ hostNames[session.host_id] || session.host_id }}</small></div><div><span class="status-pill" :class="session.state === 'active' ? 'ready' : 'busy'">{{ session.state === 'active' ? '已连接' : '连接验证中' }}</span><small>{{ label(session.capabilities) }}</small></div><button class="secondary danger-text" :disabled="busy" @click="change('/netgo/v2/remote/sessions/' + encodeURIComponent(session.id), 'DELETE', undefined, '远控会话已停止，桌面授权仍然保留。')">停止会话</button></div><div v-if="loaded && !activeSessions.length" class="empty">当前没有远控会话。</div></section>
  <RemoteHistory :request="request" />
</template>
