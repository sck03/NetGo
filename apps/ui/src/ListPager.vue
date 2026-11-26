<script setup lang="ts">
defineProps<{ after: string; next: string; busy: boolean }>()
defineEmits<{ navigate: [after: string] }>()
</script>
<template>
  <div class="actions" aria-label="列表分页">
    <button class="secondary" :disabled="busy || !after" @click="$emit('navigate', '')">第一页</button>
    <button class="secondary" :disabled="busy || !next" @click="$emit('navigate', next)">下一页</button>
    <small class="muted">每页最多 50 项</small>
  </div>
</template>
