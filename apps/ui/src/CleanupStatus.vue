<script setup lang="ts">
import { computed } from 'vue'
import type { CleanupRetry } from '../../../packages/ui/contracts'
const props = defineProps<{ retry: CleanupRetry }>()
const next = computed(() => {
  const value = props.retry.cleanup_after
  if (!value || value.startsWith('0001-')) return ''
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '' : time.toLocaleString()
})
</script>
<template>
  <small class="muted">清理失败 {{ retry.cleanup_attempts || 0 }}{{ (retry.cleanup_attempts || 0) >= 6 ? ' 次及以上' : ' 次' }}<span v-if="next"> · 下次重试：{{ next }}</span><span v-else> · 等待后台清理</span></small>
</template>
