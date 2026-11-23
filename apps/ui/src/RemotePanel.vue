<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
type Host = { id: string; name: string; platform: string; enabled: boolean; require_consent: boolean; last_seen: string; capabilities: number }
type Grant = { id: string; user_id: string; host_id: string; capabilities: number }
type Session = { id: string; user_id: string; host_id: string; state: string; capabilities: number; expires_at: string }
const props = defineProps<{ request: (path: string, method?: string, body?: any) => Promise<any>; users: Record<string, { alias: string }> }>()
const inventory = ref<{ hosts: Host[]; grants: Grant[]; sessions: Session[] }>({ hosts: [], grants: [], sessions: [] })
const error = ref(''), notice = ref(''), busy = ref(false)
const form = ref({ user_id: '', host_id: '', capabilities: 1 })
const permissions = [{ bit: 1, name: '观看' }, { bit: 2, name: '键鼠输入' }, { bit: 4, name: '音频' }, { bit: 8, name: '剪贴板' }, { bit: 16, name: '上传' }, { bit: 32, name: '下载' }]
let alive = true, loading = false, timer: ReturnType<typeof setInterval> | undefined
const label = (bits: number) => permissions.filter(p => bits & p.bit).map(p => p.name).join('、')
function toggle(bit: number, checked: boolean) { form.value.capabilities = checked ? form.value.capabilities | bit : form.value.capabilities & ~bit }
async function refresh() {
  if (loading) return
  loading = true
  try { const data = await props.request('/netgo/v2/remote'); if (alive) { inventory.value = data; error.value = '' } }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e) }
  finally { loading = false }
}
async function change(path: string, method: string, body?: any) {
  busy.value = true; error.value = ''; notice.value = ''
  try { await props.request(path, method, body); if (alive) { notice.value = '已保存。旧授权会话已关闭。'; await refresh() } }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive) busy.value = false }
}
onMounted(() => { void refresh(); timer = setInterval(refresh, 5000) })
onUnmounted(() => { alive = false; if (timer) clearInterval(timer) })
</script>
<template>
  <p v-if="error" class="error" role="alert">{{ error }}</p>
  <p v-if="notice" role="status">{{ notice }}</p>
  <section class="card">
    <h3>远程主机</h3>
    <p>设备必须主动开启被控功能。网络接入权限不会自动授予桌面访问权限。</p>
    <div v-for="host in inventory.hosts" :key="host.id" class="resource">
      <strong>{{ host.name }}</strong><span>{{ host.platform }}</span>
      <span>{{ host.enabled && Date.now() - Date.parse(host.last_seen) < 15000 ? '在线' : '离线或已关闭' }}</span>
      <span>{{ host.require_consent ? '连接需本机同意' : '允许已授权的无人值守连接' }}</span>
      <small>{{ label(host.capabilities) }}</small>
    </div>
    <div v-if="!inventory.hosts.length" class="empty">还没有设备登记为远程主机。</div>
  </section>
  <section class="card">
    <h3>桌面访问授权</h3>
    <form class="resource-form" @submit.prevent="change('/netgo/v2/admin/remote/grants', 'PUT', form)">
      <label>账号<select v-model="form.user_id" required><option value="" disabled>选择账号</option><option v-for="(user, id) in users" :key="id" :value="id">{{ user.alias }}</option></select></label>
      <label>主机<select v-model="form.host_id" required><option value="" disabled>选择主机</option><option v-for="host in inventory.hosts" :key="host.id" :value="host.id">{{ host.name }}</option></select></label>
      <label v-for="permission in permissions" :key="permission.bit" class="check"><input type="checkbox" :checked="!!(form.capabilities & permission.bit)" :disabled="permission.bit === 1" @change="toggle(permission.bit, ($event.target as HTMLInputElement).checked)">{{ permission.name }}</label>
      <button class="primary" :disabled="busy">保存授权</button>
    </form>
    <div v-for="grant in inventory.grants" :key="grant.id" class="resource">
      <strong>{{ users[grant.user_id]?.alias || grant.user_id }}</strong><span>{{ inventory.hosts.find(h => h.id === grant.host_id)?.name }}</span><span>{{ label(grant.capabilities) }}</span>
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
