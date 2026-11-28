<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { LatestQuery } from '../../../packages/ui/query'
import type { ResourceChoices } from '../../../packages/ui/models.generated'
import type { AdminRequest } from '../../../packages/ui/contracts'
import ListPager from './ListPager.vue'
const props = defineProps<{ modelValue: string[]; resources: { id: string; name: string }[]; disabled: boolean; request: AdminRequest }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const search = ref(''), after = ref(''), busy = ref(false), error = ref('')
const page = ref<ResourceChoices>({ resources: [], next_after: '' })
const labels = ref<Record<string, string>>({})
watch([() => props.resources, () => props.modelValue, () => page.value.resources], () => {
  const known = { ...labels.value, ...Object.fromEntries([...props.resources, ...page.value.resources].map(r => [r.id, r.name])) }
  labels.value = Object.fromEntries(props.modelValue.map(id => [id, known[id] || id]))
}, { immediate: true })
const offPage = computed(() => props.modelValue.filter(id => !page.value.resources.some(r => r.id === id)))
function toggle(id: string, checked: boolean) {
  emit('update:modelValue', checked ? [...new Set([...props.modelValue, id])] : props.modelValue.filter(value => value !== id))
}
let alive = true, version = 0, timer: ReturnType<typeof setTimeout> | undefined
const query = new LatestQuery(() => props.request<ResourceChoices>('/netgo/v2/admin/resources/choices?after=' + encodeURIComponent(after.value) + '&q=' + encodeURIComponent(search.value)), value => { page.value = value })
async function load(cursor = '') {
  const current = ++version
  query.invalidate(); after.value = cursor; busy.value = true; error.value = ''
  try { await query.run() }
  catch (e) { if (alive && current === version) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (alive && current === version) busy.value = false }
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
  <fieldset class="permission-list">
    <legend>允许访问的资源 · 已选 {{ modelValue.length }} 项</legend>
    <label>查找资源<input v-model="search" type="search" maxlength="128" :disabled="disabled" placeholder="资源名称或 IP 地址"></label>
    <label v-for="id in offPage" :key="id" class="check"><input type="checkbox" checked :disabled="disabled" @change="toggle(id, false)">{{ labels[id] }} <small class="muted">已选择</small></label>
    <label v-for="resource in page.resources" :key="resource.id" class="check"><input type="checkbox" :checked="modelValue.includes(resource.id)" :disabled="disabled" @change="toggle(resource.id, ($event.target as HTMLInputElement).checked)">{{ resource.name }} <small v-if="!resource.enabled" class="muted">已停用</small></label>
    <p v-if="!busy && !page.resources.length" class="muted">{{ search ? '没有匹配的资源。' : '尚未添加资源，请先到“局域网资源”添加。' }}</p>
    <p v-if="error" class="alert error" role="alert">{{ error }} <button type="button" class="secondary" :disabled="busy || disabled" @click="load(after)">重试</button></p>
    <ListPager :after="after" :next="page.next_after" :busy="disabled || busy" @navigate="load" />
    <p class="muted">翻页和搜索保留已选资源。仅使用远程桌面时可以不选，桌面权限在“远程桌面”中单独授予。</p>
  </fieldset>
</template>
