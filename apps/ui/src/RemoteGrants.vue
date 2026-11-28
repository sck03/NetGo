<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { expiryInput, expiryPayload, remoteCapabilities, remotePermissions as permissions } from '../../../packages/ui/remote'
import { LatestQuery } from '../../../packages/ui/query'
import type { AdminRequest, RemoteHost as Host, RemoteGrant as Grant } from '../../../packages/ui/contracts'
import type { GrantPage } from '../../../packages/ui/models.generated'
import ListPager from './ListPager.vue'
import UserPicker from './UserPicker.vue'
const props = defineProps<{ request: AdminRequest; hosts: Host[]; hostId: string }>()
const emit = defineEmits<{ 'update:hostId': [value: string]; changed: [] }>()
const selectedHost = computed(() => props.hosts.find(h => h.id === props.hostId))
const availablePermissions = computed(() => permissions.filter(p => (selectedHost.value?.capabilities ?? 0) & p.bit))
const page = ref<GrantPage>({ grants: [], users: {}, next_after: '' }), after = ref('')
const form = ref({ user_id: '', capabilities: 1, expiry: '', edit_token: '' })
const selectedGrant = ref<Grant>(), selectionReady = ref(false)
const selectedLabel = ref('')
const error = ref(''), notice = ref(''), busy = ref(false), loading = ref(false)
const stale = computed(() => selectionReady.value && form.value.edit_token !== (selectedGrant.value?.edit_token ?? ''))
const label = (bits: number) => permissions.filter(p => bits & p.bit).map(p => p.name).join('、')
const base = () => '/netgo/v2/admin/remote/grants?host_id=' + encodeURIComponent(props.hostId)
const query = new LatestQuery(signal => props.request<GrantPage>(base() + '&after=' + encodeURIComponent(after.value), 'GET', undefined, signal), value => { page.value = value })
const selection = new LatestQuery(signal => props.request<GrantPage>(base() + '&user_id=' + encodeURIComponent(form.value.user_id), 'GET', undefined, signal), value => {
  selectedGrant.value = value.grants[0]
  selectedLabel.value = value.users[form.value.user_id] || ''
  if (!selectionReady.value) reloadGrant()
  selectionReady.value = true
})
let alive = true, revision = 0, timer: ReturnType<typeof setInterval> | undefined
function reloadGrant() {
  form.value.capabilities = remoteCapabilities(selectedGrant.value?.capabilities ?? 1, selectedHost.value?.capabilities ?? 0)
  form.value.expiry = expiryInput(selectedGrant.value?.expires_at ?? '')
  form.value.edit_token = selectedGrant.value?.edit_token ?? ''
}
async function refresh() {
  if (!props.hostId || busy.value || !alive || document.hidden) return
  const version = ++revision
  loading.value = true; error.value = ''
  try { await Promise.all([query.run(), form.value.user_id ? selection.run() : Promise.resolve()]) }
  catch (e) { if (alive && version === revision) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive && version === revision) loading.value = false }
}
async function navigate(cursor = '') { query.invalidate(); after.value = cursor; await refresh() }
function toggle(bit: number, checked: boolean) { form.value.capabilities = checked ? form.value.capabilities | bit : form.value.capabilities & ~bit }
function preset(bits: number) { form.value.capabilities = remoteCapabilities(bits, selectedHost.value?.capabilities ?? 0) }
function editGrant(grant: Grant) {
  form.value.user_id = grant.user_id
  document.getElementById('remote-grant')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
async function change(path: string, method: string, body?: unknown) {
  if (busy.value) return false
  busy.value = true; query.invalidate(); selection.invalidate(); error.value = ''; notice.value = ''
  let changed = false
  const selected = props.hostId + ':' + form.value.user_id
  try { await props.request(path, method, body); changed = true; notice.value = '桌面权限已更新，相关会话会按新权限处理。' }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : String(e) }
  finally {
    if (alive) {
      busy.value = false
      const actionError = error.value
      await refresh()
      if (actionError) error.value = actionError
      if (changed) { if (selected === props.hostId + ':' + form.value.user_id) reloadGrant(); emit('changed') }
    }
  }
  return changed
}
async function save() {
  if (!selectionReady.value || stale.value || loading.value || !form.value.user_id || !selectedHost.value) return
  try {
    const { user_id, expiry, edit_token } = form.value
    const capabilities = remoteCapabilities(form.value.capabilities, selectedHost.value.capabilities)
    if (capabilities) await change('/netgo/v2/admin/remote/grants', 'PUT', { user_id, host_id: props.hostId, capabilities, expires_at: expiryPayload(expiry), edit_token })
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}
async function revoke(grant: Grant) {
  if (window.confirm('撤销“' + (page.value.users[grant.user_id] || grant.user_id) + '”对此电脑的桌面权限？正在进行的远控也会结束。'))
    await change('/netgo/v2/admin/remote/grants/' + encodeURIComponent(grant.id), 'DELETE')
}
watch(() => [props.hostId, form.value.user_id], ([host], [oldHost]) => {
  selection.invalidate(); selectedGrant.value = undefined; selectionReady.value = false; selectedLabel.value = ''
  if (host !== oldHost) { query.invalidate(); after.value = ''; page.value = { grants: [], users: {}, next_after: '' } }
  reloadGrant(); void refresh()
}, { flush: 'sync' })
onMounted(() => { void refresh(); timer = setInterval(refresh, 5000) })
onUnmounted(() => { alive = false; query.invalidate(); selection.invalidate(); if (timer) clearInterval(timer) })
</script>
<template>
  <section id="remote-grant" class="card">
    <div class="panel-heading"><div><h3>桌面访问授权</h3><p>先选账号和电脑，再选择可执行的操作。仅观看适合演示和问题排查。</p></div><span v-if="selectedGrant" class="status-pill" :class="selectedGrant.active ? 'ready' : 'offline'">{{ selectedGrant.active ? '正在修改已有授权' : '原授权已到期' }}</span></div>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <p v-if="notice" class="alert" role="status">{{ notice }}</p>
    <form class="grant-form" @submit.prevent="save">
      <p v-if="stale" class="alert" role="status">授权已发生变化，草稿已保留。请载入最新设置并核对权限。<button type="button" class="secondary" :disabled="busy" @click="reloadGrant">载入最新设置</button></p>
      <div class="form-columns"><UserPicker v-model="form.user_id" :label="page.users[form.user_id] || selectedLabel" :request="request" :disabled="busy" /><label>电脑<select :value="hostId" @change="emit('update:hostId', ($event.target as HTMLSelectElement).value)" :disabled="busy" required><option value="" disabled>选择电脑</option><option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option></select></label></div>
      <template v-if="selectedHost"><div class="preset-bar"><span>快捷选择</span><button type="button" class="secondary" :disabled="busy" @click="preset(1)">仅观看</button><button v-if="selectedHost.capabilities & 2" type="button" class="secondary" :disabled="busy" @click="preset(3)">观看并操作</button></div><fieldset class="permission-list"><legend>允许的操作</legend><label v-for="permission in availablePermissions" :key="permission.bit" class="check"><input type="checkbox" :checked="!!(form.capabilities & permission.bit)" :disabled="busy || permission.bit === 1" @change="toggle(permission.bit, ($event.target as HTMLInputElement).checked)">{{ permission.name }}</label></fieldset></template>
      <div class="form-columns"><label>到期时间（可留空）<input v-model="form.expiry" type="datetime-local" :disabled="busy"><small class="muted">按当前浏览器时区填写，留空表示不设到期时间。</small></label><div class="actions"><button class="primary" :disabled="busy || loading || !selectionReady || stale || !form.user_id || !selectedHost || !(selectedHost.capabilities & 1)">{{ busy ? '处理中…' : '保存桌面授权' }}</button></div></div>
    </form>
    <div class="access-list"><div v-for="grant in page.grants" :key="grant.id" class="access-row"><div><strong>{{ page.users[grant.user_id] || grant.user_id }}</strong><small>访问 {{ selectedHost?.name || grant.host_id }}</small></div><div><span>{{ label(grant.capabilities) }}</span><small>{{ grant.active ? (expiryInput(grant.expires_at) ? '到期：' + expiryInput(grant.expires_at).replace('T', ' ') : '不设到期时间') : '授权已到期' }}</small></div><div class="actions"><button class="secondary" :disabled="busy" @click="editGrant(grant)">修改</button><button class="secondary danger-text" :disabled="busy" @click="revoke(grant)">撤销</button></div></div><p v-if="hostId && !loading && !page.grants.length" class="empty">尚未授予桌面访问权限。</p></div>
    <ListPager :after="after" :next="page.next_after" :busy="busy || loading" @navigate="navigate" />
  </section>
</template>
