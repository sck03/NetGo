<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { AdminRequest } from '../../../packages/ui/contracts'
import type { AccountCleanup, AccountCleanupPage } from '../../../packages/ui/models.generated'
import { LatestQuery } from '../../../packages/ui/query'
import ListPager from './ListPager.vue'
const props = defineProps<{ request: AdminRequest }>()
const page = ref<AccountCleanupPage>({ tasks: [], next_after: '' }), after = ref('')
const error = ref(''), busy = ref(false), now = ref(Date.now())
const statuses: Record<string, string> = { checking: '核对创建结果中', upstream_unavailable: '身份服务暂不可用', not_found: '暂未发现账号，仍保留创建预约', cleanup_failed: '上游账号清理待重试' }
const query = new LatestQuery(signal => props.request<AccountCleanupPage>('/netgo/v2/admin/account-creations?after=' + encodeURIComponent(after.value), 'GET', undefined, signal), value => { page.value = value })
let alive = true, timer: ReturnType<typeof setInterval> | undefined
const canManage = (task: AccountCleanup) => !task.pending || (Date.parse(task.created_at) > 0 && now.value - Date.parse(task.created_at) >= 600000)
async function load(cursor = after.value) {
  if (busy.value || !alive) return
  query.invalidate(); after.value = cursor; now.value = Date.now()
  try { await query.run() } catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e) }
}
async function manage(task: AccountCleanup, confirmed_absent = false) {
  if (busy.value || !canManage(task)) return
  if (confirmed_absent && !window.confirm('请先在身份服务确认原创建请求已结束、该邮箱账号不存在。仅查询不到账号不足以证明创建失败。已完成这两项核对，并释放“' + task.alias + '”的用户名和邮箱预约？')) return
  busy.value = true; query.invalidate(); error.value = ''
  try { await props.request('/netgo/v2/admin/account-creations/' + encodeURIComponent(task.id), 'POST', { confirmed_absent }) }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive) { busy.value = false; await load() } }
}
onMounted(() => { void load(); timer = setInterval(() => { if (!document.hidden) void load() }, 10000) })
onUnmounted(() => { alive = false; query.invalidate(); if (timer) clearInterval(timer) })
</script>
<template>
  <section v-if="page.tasks.length || after || error" class="card" :aria-busy="busy">
    <div class="panel-heading"><div><h3>账号创建待处理</h3><p>后台会自动核对并清理；结果未确定时保留用户名和邮箱，避免重复创建。</p></div><button class="secondary" :disabled="busy" @click="load()">刷新</button></div>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <div v-for="task in page.tasks" :key="task.id" class="access-row">
      <div><strong>{{ task.alias }}</strong><small>{{ task.email }}</small></div>
      <div><span>{{ statuses[task.status] || '等待核对或清理' }}</span><small>{{ task.last_checked && !task.last_checked.startsWith('0001-') ? '最近核对：' + new Date(task.last_checked).toLocaleString() : '尚未完成核对' }}</small></div>
      <div class="actions"><button class="secondary" :disabled="busy || !canManage(task)" @click="manage(task)">重新核对</button><button v-if="task.pending" class="secondary danger-text" :disabled="busy || !canManage(task)" @click="manage(task, true)">确认未创建，释放预约</button></div>
    </div>
    <p class="muted">创建后至少等待十分钟，才可人工处理。释放预约前必须确认身份服务的原请求已结束。</p>
    <ListPager :after="after" :next="page.next_after" :busy="busy" @navigate="load" />
  </section>
</template>
