<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { LatestQuery } from '../../../packages/ui/query'
import { emptyState, type AdminState, type AdminRequest, type DeleteResult } from '../../../packages/ui/contracts'
import CleanupStatus from './CleanupStatus.vue'
import ListPager from './ListPager.vue'

const props = defineProps<{ request: AdminRequest }>()
const sessions = ref<AdminState>(emptyState()), inventory = ref<AdminState>(emptyState())
const sessionAfter = ref(''), deviceAfter = ref(''), busy = ref(false), error = ref(''), notice = ref('')
const loaded = ref(false)
const leases = computed(() => Object.values(sessions.value.leases))
const devices = computed(() => Object.values(inventory.value.devices))
const sessionQuery = new LatestQuery(signal => props.request<Partial<AdminState>>('/netgo/v2/admin/state?section=sessions&after=' + encodeURIComponent(sessionAfter.value), 'GET', undefined, signal), value => { sessions.value = { ...emptyState(), ...value } })
const deviceQuery = new LatestQuery(signal => props.request<Partial<AdminState>>('/netgo/v2/admin/state?section=devices&after=' + encodeURIComponent(deviceAfter.value), 'GET', undefined, signal), value => { inventory.value = { ...emptyState(), ...value } })
let timer: ReturnType<typeof setInterval> | undefined
let mounted = true
function failure(e: unknown) { if (mounted) error.value = e instanceof Error ? e.message : String(e) }
async function refresh() {
  if (!mounted) return
  await Promise.all([sessionQuery.run(), deviceQuery.run()])
  if (mounted) loaded.value = true
}
async function navigate(kind: 'sessions' | 'devices', after: string) {
  if (busy.value) return
  busy.value = true; error.value = ''
  const query = kind === 'sessions' ? sessionQuery : deviceQuery
  query.invalidate()
  if (kind === 'sessions') sessionAfter.value = after
  else deviceAfter.value = after
  try { await query.run() } catch (e) { failure(e) } finally { busy.value = false }
}
async function revoke(kind: 'sessions' | 'devices', id: string) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  sessionQuery.invalidate(); deviceQuery.invalidate()
  try {
    const result = await props.request<Partial<DeleteResult>>('/netgo/v2/admin/' + kind + '/' + encodeURIComponent(id), 'DELETE')
    if (!mounted) return
    await refresh()
    notice.value = result.cleanup_pending ? '访问已撤销，上游暂不可用，后台将自动重试清理。' : '访问已撤销。'
  } catch (e) { failure(e) } finally { busy.value = false }
}
onMounted(() => {
  busy.value = true
  refresh().catch(failure).finally(() => { busy.value = false })
  timer = setInterval(() => { if (!document.hidden && !busy.value) refresh().catch(failure) }, 5000)
})
onUnmounted(() => { mounted = false; if (timer) clearInterval(timer); sessionQuery.invalidate(); deviceQuery.invalidate() })
</script>
<template>
  <p v-if="error" class="alert error" role="alert">{{ error }}</p>
  <p v-if="notice" class="alert" role="status">{{ notice }}</p>
  <p v-if="!loaded" class="muted" aria-live="polite">正在加载设备与会话…</p>
  <template v-else>
    <section class="card" aria-label="有效会话">
      <h3>正在授权的会话</h3>
      <div v-for="lease in leases" :key="lease.id" class="resource">
        <strong>{{ sessions.users[lease.user_id]?.alias || lease.user_id }}</strong><code>{{ lease.ip }}</code>
        <span>{{ new Date(lease.expires_at).toLocaleString() }}</span>
        <button class="secondary" :disabled="busy" @click="revoke('sessions', lease.id)">立即撤销</button>
      </div>
      <div v-if="!leases.length" class="empty">{{ sessionAfter ? '本页已无有效会话，请返回第一页查看。' : '当前没有有效会话。' }}</div>
      <ListPager :after="sessionAfter" :next="sessions.next_after || ''" :busy="busy" @navigate="navigate('sessions', $event)" />
    </section>
    <section class="card" aria-label="设备登记">
      <h3>已登记设备</h3>
      <div v-for="device in devices" :key="device.id" class="resource">
        <strong>{{ inventory.users[device.user_id]?.alias || device.user_id }}</strong><code>{{ device.id.slice(0, 12) }}</code>
        <span>{{ device.delete_pending ? '访问已撤销，等待上游清理' : (device.ip || '待连接') }}</span>
        <CleanupStatus v-if="device.delete_pending" :retry="device" />
        <small>{{ new Date(device.created_at).toLocaleString() }}</small>
        <button class="secondary" :disabled="busy" @click="revoke('devices', device.id)">{{ device.delete_pending ? '重试清理' : '撤销设备' }}</button>
      </div>
      <div v-if="!devices.length" class="empty">{{ deviceAfter ? '本页已无设备，请返回第一页查看。' : '当前没有已登记设备。' }}</div>
      <ListPager :after="deviceAfter" :next="inventory.next_after || ''" :busy="busy" @navigate="navigate('devices', $event)" />
    </section>
  </template>
</template>
