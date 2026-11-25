<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { expiryInput, expiryPayload, hostStatusLabel, remoteCapabilities, remotePermissions as permissions } from '../../../packages/ui/remote'
type Host = { id: string; name: string; platform: string; status: string; require_consent: boolean; capabilities: number }
type Grant = { id: string; user_id: string; host_id: string; capabilities: number; active: boolean; expires_at: string }
type Session = { id: string; user_id: string; host_id: string; state: string; capabilities: number; expires_at: string }
const props = defineProps<{ request: (path: string, method?: string, body?: any) => Promise<any>; users: Record<string, { alias: string }> }>()
const inventory = ref<{ hosts: Host[]; grants: Grant[]; sessions: Session[] }>({ hosts: [], grants: [], sessions: [] })
const error = ref(''), loadError = ref(''), notice = ref(''), busy = ref(false)
const form = ref({ user_id: '', host_id: '', capabilities: 1, expiry: '' })
const selectedHost = computed(() => inventory.value.hosts.find(h => h.id === form.value.host_id))
const availablePermissions = computed(() => permissions.filter(p => (selectedHost.value?.capabilities ?? 0) & p.bit))
watch(() => [form.value.user_id, form.value.host_id], () => {
  const existing = inventory.value.grants.find(g => g.user_id === form.value.user_id && g.host_id === form.value.host_id)
  form.value.capabilities = remoteCapabilities(existing?.capabilities ?? 1, selectedHost.value?.capabilities ?? 0)
  form.value.expiry = expiryInput(existing?.expires_at ?? '')
})
function editGrant(grant: Grant) {
  form.value = { user_id: grant.user_id, host_id: grant.host_id, capabilities: grant.capabilities, expiry: expiryInput(grant.expires_at) }
}
async function save() {
  const capabilities = remoteCapabilities(form.value.capabilities, selectedHost.value?.capabilities ?? 0)
  if (!capabilities || !form.value.user_id) return
  try {
    const { user_id, host_id, expiry } = form.value
    await change('/netgo/v2/admin/remote/grants', 'PUT', { user_id, host_id, capabilities, expires_at: expiryPayload(expiry) })
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}
let alive = true, loading = false, revision = 0, timer: ReturnType<typeof setInterval> | undefined
const label = (bits: number) => permissions.filter(p => bits & p.bit).map(p => p.name).join('、')
function toggle(bit: number, checked: boolean) { form.value.capabilities = checked ? form.value.capabilities | bit : form.value.capabilities & ~bit }
async function refresh() {
  if (loading || busy.value) return
  loading = true
  const requested = revision
  try { const data = await props.request('/netgo/v2/remote'); if (alive && requested === revision) { inventory.value = data; loadError.value = '' } }
  catch (e) { if (alive && requested === revision) loadError.value = e instanceof Error ? e.message : String(e) }
  finally { loading = false }
}
async function change(path: string, method: string, body?: any) {
  if (busy.value) return
  busy.value = true; revision++; error.value = ''; notice.value = ''
  try { await props.request(path, method, body); if (alive) notice.value = '操作成功，相关旧会话已关闭。' }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive) { busy.value = false; await refresh() } }
}
onMounted(() => { void refresh(); timer = setInterval(refresh, 5000) })
onUnmounted(() => { alive = false; if (timer) clearInterval(timer) })
</script>
<template>
  <p v-if="error || loadError" class="error" role="alert">{{ error || loadError }}</p>
  <p v-if="notice" role="status">{{ notice }}</p>
  <section class="card">
    <h3>远程主机</h3>
    <p>设备必须主动开启被控功能。网络接入权限不会自动授予桌面访问权限。</p>
    <div v-for="host in inventory.hosts" :key="host.id" class="resource">
      <strong>{{ host.name }}</strong><span>{{ host.platform }}</span>
      <span>{{ hostStatusLabel(host.status) }}</span>
      <span>{{ host.require_consent ? '连接需本机同意' : '允许已授权的无人值守连接' }}</span>
      <small>{{ label(host.capabilities) }}</small>
    </div>
    <div v-if="!inventory.hosts.length" class="empty">还没有设备登记为远程主机。</div>
  </section>
  <section class="card">
    <h3>桌面访问授权</h3>
    <p>选择账号和主机后修改权限；只显示该主机已支持的功能。保存会替换此账号对该主机的原授权。</p>
    <form class="resource-form" @submit.prevent="save">
      <label>账号<select v-model="form.user_id" required><option value="" disabled>选择账号</option><option v-for="(user, id) in users" :key="id" :value="id">{{ user.alias }}</option></select></label>
      <label>主机<select v-model="form.host_id" required><option value="" disabled>选择主机</option><option v-for="host in inventory.hosts" :key="host.id" :value="host.id">{{ host.name }}</option></select></label>
      <label v-for="permission in availablePermissions" :key="permission.bit" class="check"><input type="checkbox" :checked="!!(form.capabilities & permission.bit)" :disabled="busy || permission.bit === 1" @change="toggle(permission.bit, ($event.target as HTMLInputElement).checked)">{{ permission.name }}</label>
      <label>到期时间（可留空）<input v-model="form.expiry" type="datetime-local" :disabled="busy"></label>
      <button class="primary" :disabled="busy || !form.user_id || !selectedHost || !(selectedHost.capabilities & 1)">保存授权</button>
    </form>
    <div v-for="grant in inventory.grants" :key="grant.id" class="resource">
      <strong>{{ users[grant.user_id]?.alias || grant.user_id }}</strong><span>{{ inventory.hosts.find(h => h.id === grant.host_id)?.name }}</span><span>{{ label(grant.capabilities) }}</span><span v-if="!grant.active">已到期</span>
      <button class="secondary" :disabled="busy" @click="editGrant(grant)">修改权限</button>
      <button class="secondary" :disabled="busy" @click="change('/netgo/v2/admin/remote/grants/' + encodeURIComponent(grant.id), 'DELETE')">撤销授权</button>
    </div>
  </section>
  <section class="card">
    <h3>远控会话</h3>
    <div v-for="session in inventory.sessions.filter(s => s.state !== 'closed')" :key="session.id" class="resource">
      <strong>{{ users[session.user_id]?.alias }}</strong><span>{{ inventory.hosts.find(h => h.id === session.host_id)?.name }}</span><span>{{ session.state === 'active' ? '已接受' : '等待接受' }}</span><span>{{ label(session.capabilities) }}</span>
      <button class="secondary" :disabled="busy" @click="change('/netgo/v2/remote/sessions/' + encodeURIComponent(session.id), 'DELETE')">停止会话</button>
    </div>
    <div v-if="!inventory.sessions.some(s => s.state !== 'closed')" class="empty">没有活动远控会话。</div>
  </section>
</template>
