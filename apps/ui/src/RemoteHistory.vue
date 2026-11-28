<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { LatestQuery } from '../../../packages/ui/query'
import type { RemoteHistoryPage, AdminRequest } from '../../../packages/ui/contracts'
const props = defineProps<{ request: AdminRequest }>()
const page = ref<RemoteHistoryPage>({ sessions: [], next_before: '', users: {}, hosts: {} })
const loaded = ref(false), busy = ref(false), error = ref(''), before = ref('')
let alive = true
const query = new LatestQuery(signal => props.request<RemoteHistoryPage>('/netgo/v2/admin/remote/history?limit=50&before=' + encodeURIComponent(before.value), 'GET', undefined, signal), value => { page.value=value; loaded.value=true })
async function load(cursor = '') {
  if (busy.value) return
  busy.value=true; error.value=''; before.value=cursor
  try { await query.run() } catch (e) { if (alive) error.value=e instanceof Error ? e.message : String(e) }
  finally { if (alive) busy.value=false }
}
onUnmounted(() => { alive=false; query.invalidate() })
</script>
<template>
  <section class="card" :aria-busy="busy">
    <div class="panel-heading"><div><h3>已结束的远控</h3><p>保留最近 24 小时的会话，按页查看。更早的操作可在审计记录中查询。</p></div><button class="secondary" :disabled="busy" @click="load()">{{ loaded ? '刷新记录' : '查看记录' }}</button></div>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <div v-for="session in page.sessions" :key="session.id" class="access-row"><div><strong>{{ page.users[session.user_id] || session.user_id }}</strong><small>{{ page.hosts[session.host_id] || session.host_id }}</small></div><div><small>开始：{{ new Date(session.created_at).toLocaleString() }}</small><small>结束：{{ new Date(session.closed_at).toLocaleString() }}</small></div><span class="muted">已结束</span></div>
    <p v-if="loaded && !page.sessions.length" class="empty">没有保留的已结束会话。</p>
    <button v-if="page.next_before" class="secondary" :disabled="busy" @click="load(page.next_before)">下一页</button>
  </section>
</template>
