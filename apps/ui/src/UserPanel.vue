<script setup lang="ts">
import { computed, ref } from 'vue'
import type { User, AdminRequest } from '../../../packages/ui/contracts'
import CleanupStatus from './CleanupStatus.vue'
import ResourcePicker from './ResourcePicker.vue'
type Account = { Alias: string; Email: string; Name: string }
const props = defineProps<{ users: User[]; resources: { id: string; name: string }[]; request: AdminRequest; busy: boolean; create: (account: Account) => Promise<boolean>; save: (user: User) => Promise<boolean>; remove: (user: User) => Promise<boolean> }>()
const draft = ref<User | null>(null), creating = ref(false)
const account = ref<Account>({ Alias: '', Email: '', Name: '' })
const latest = computed(() => props.users.find(user => user.id === draft.value?.id))
const stale = computed(() => !!draft.value && (!latest.value || latest.value.edit_revision !== draft.value.edit_revision))
function edit(user: User) { draft.value = { ...user, resources: [...user.resources] } }
async function create() { if (await props.create({ ...account.value, Name: account.value.Name.trim() || account.value.Alias })) { account.value = { Alias: '', Email: '', Name: '' }; creating.value = false } }
async function save() { if (draft.value && !stale.value && await props.save(draft.value)) draft.value = null }
async function remove(user: User) { if (await props.remove(user) && draft.value?.id === user.id) draft.value = null }
function expiryValue(value: string) { if (!value || value.startsWith('0001-')) return ''; const d = new Date(value); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function setExpiry(event: Event) { const value = (event.target as HTMLInputElement).value; if (draft.value) draft.value.expires_at = value ? new Date(value).toISOString() : '0001-01-01T00:00:00Z' }
</script>
<template>
  <section class="card">
    <div class="panel-heading"><div><h3>谁可以访问</h3><p>先创建账号，再勾选允许访问的资源。设备限额和到期时间可按需调整。</p></div><button class="primary" :disabled="busy" @click="creating = !creating">{{ creating ? '收起' : '创建账号' }}</button></div>
    <form v-if="creating" class="create-user" @submit.prevent="create"><label>用户名<input v-model="account.Alias" required placeholder="用于登录"></label><label>邮箱<input v-model="account.Email" type="email" required></label><label>显示名称<input v-model="account.Name" placeholder="可留空，使用用户名"></label><button class="primary" :disabled="busy">创建账号</button></form>
    <div v-for="user in users" :key="user.id" class="resource"><div><strong>{{ user.name || user.alias }}</strong><small>{{ user.email }}</small><CleanupStatus v-if="user.delete_pending" :retry="user" /></div><span>{{ user.role === 'admin' ? '管理员' : '用户' }} · {{ user.delete_pending ? '等待上游清理' : user.disabled ? '已停用' : '正常' }} · {{ user.resources?.length || 0 }} 项资源</span><button class="secondary" :disabled="busy || user.delete_pending" @click="edit(user)">修改权限</button><button class="secondary" :disabled="busy" @click="remove(user)">{{ user.delete_pending ? '重试清理' : '删除' }}</button></div>
  </section>
  <section v-if="draft" class="card">
    <h3>{{ draft.alias }} 的访问权限</h3>
    <form @submit.prevent="save">
      <label>用户名<input :value="draft.alias" readonly><small>登录名固定；可修改下方显示名称。</small></label><label>显示名称<input v-model="draft.name" maxlength="256" placeholder="可留空，使用用户名"></label><label>角色<select v-model="draft.role"><option value="user">用户</option><option value="admin">管理员</option></select></label>
      <label class="check"><input v-model="draft.disabled" type="checkbox">停用账号</label>
      <ResourcePicker :key="draft.id" v-model="draft.resources" :resources="resources" :request="request" :disabled="busy || stale" />
      <details><summary>设备限额和到期时间</summary><div class="account-limits"><label>最多登记设备<input v-model.number="draft.max_devices" type="number" min="1" max="100" required></label><label>最多同时连接<input v-model.number="draft.max_sessions" type="number" min="1" max="100" required></label><label>到期时间（留空为不限）<input type="datetime-local" :value="expiryValue(draft.expires_at)" @change="setExpiry"></label></div></details>
      <p class="muted">修改权限会撤销此账号已有连接，重新连接后生效；仅修改显示名称不会断开连接。必须保留至少一个启用且不过期的管理员。</p>
      <p v-if="stale" class="muted">账号已发生变化，请重新载入并核对最新权限。<button v-if="latest" type="button" class="secondary" :disabled="busy" @click="edit(latest)">载入最新设置</button></p>
      <div class="actions"><button class="primary" :disabled="busy || stale">保存权限</button><button type="button" class="secondary" :disabled="busy" @click="draft = null">取消</button></div>
    </form>
  </section>
</template>
