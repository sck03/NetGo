<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { LatestQuery } from '../../../packages/ui/query'
import type { UserChoice, UserChoices, AdminRequest } from '../../../packages/ui/contracts'
import ListPager from './ListPager.vue'
const props = defineProps<{ modelValue: string; label?: string; disabled: boolean; request: AdminRequest }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const search = ref(''), after = ref(''), busy = ref(false), error = ref('')
const page = ref<UserChoices>({ users: [], next_after: '' })
const selectedOnPage = computed(() => page.value.users.some(user => user.id === props.modelValue))
const selectedUser = ref<UserChoice>()
const userLabel = (user: UserChoice) => (user.name && user.name !== user.alias ? user.name + ' · ' : '') + user.alias
const selectedLabel = computed(() => props.label || (selectedUser.value ? userLabel(selectedUser.value) : props.modelValue))
watch([() => props.modelValue, () => page.value.users], () => {
  selectedUser.value = page.value.users.find(user => user.id === props.modelValue) || (selectedUser.value?.id === props.modelValue ? selectedUser.value : undefined)
}, { immediate: true })
let alive = true, version = 0, timer: ReturnType<typeof setTimeout> | undefined
const query = new LatestQuery(() => props.request<UserChoices>('/netgo/v2/admin/users/choices?after=' + encodeURIComponent(after.value) + '&q=' + encodeURIComponent(search.value)), value => { page.value = value })
async function load(cursor = '') {
  const request = ++version
  query.invalidate(); after.value = cursor; busy.value = true; error.value = ''
  try { await query.run() } catch (e) { if (alive && request === version) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive && request === version) busy.value = false }
}
watch(search, () => {
  query.invalidate(); ++version
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => { void load() }, 250)
})
onMounted(() => { void load() })
onUnmounted(() => { alive = false; query.invalidate(); if (timer) clearTimeout(timer) })
</script>
<template>
  <div>
    <label>查找账号<input v-model="search" type="search" maxlength="128" :disabled="disabled" placeholder="用户名或显示名称"></label>
    <label>账号<select :value="modelValue" :disabled="disabled || busy" required @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
      <option value="" disabled>选择账号</option>
      <option v-if="modelValue && !selectedOnPage" :value="modelValue">{{ selectedLabel }}</option>
      <option v-for="user in page.users" :key="user.id" :value="user.id">{{ userLabel(user) }}</option>
    </select></label>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <ListPager :after="after" :next="page.next_after" :busy="disabled || busy" @navigate="load" />
  </div>
</template>
