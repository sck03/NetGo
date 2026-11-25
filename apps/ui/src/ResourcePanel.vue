<script setup lang="ts">
import { ref } from 'vue'
import { resourcePayload, resourcePresets, type Resource, type ResourceForm } from '../../../packages/ui/resources'
const props = defineProps<{ resources: Resource[]; busy: boolean; save: (resource: Resource) => Promise<boolean>; remove: (resource: Resource) => Promise<boolean> }>()
const form = ref<ResourceForm | null>(null), preset = ref('web'), error = ref(''), editing = ref(false)
function add() {
  editing.value = false; error.value = ''; preset.value = 'web'
  form.value = { id: 'resource-' + crypto.randomUUID(), name: '', cidr: '', protocol: 'tcp', ports: '443', enabled: true }
}
function edit(resource: Resource) {
  editing.value = true; error.value = ''
  form.value = { ...resource, cidr: resource.cidr.endsWith('/32') ? resource.cidr.slice(0, -3) : resource.cidr, ports: resource.ports.join(',') }
  preset.value = resourcePresets.find(p => p.protocol === resource.protocol && p.ports === form.value!.ports)?.id || 'custom'
}
function applyPreset() {
  const selected = resourcePresets.find(p => p.id === preset.value)
  if (selected && form.value) { form.value.protocol = selected.protocol; form.value.ports = selected.ports }
  else if (form.value?.protocol === 'internet') { form.value.protocol = 'tcp'; form.value.ports = '' }
}
async function save() {
  if (!form.value) return
  error.value = ''
  try { if (await props.save(resourcePayload(form.value))) form.value = null } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}
async function remove(resource: Resource) {
  if (!window.confirm('删除资源“' + resource.name + '”？相关访问权限和连接将被撤销。')) return
  if (await props.remove(resource) && form.value?.id === resource.id) form.value = null
}
</script>
<template>
  <section class="card">
    <div class="panel-heading"><div><h3>允许访问哪些设备和服务</h3><p>选择常用服务并填写目标地址，再在“账号与权限”中授权给用户。</p></div><button class="primary" :disabled="busy" @click="add">添加资源</button></div>
    <p class="muted">NetGo 自带的屏幕控制请在“远程桌面”中设置；这里的 RDP 指设备上已有的 Windows RDP 服务。</p>
    <div v-for="resource in resources" :key="resource.id" class="resource"><strong>{{ resource.name }}</strong><code>{{ resource.protocol === 'internet' ? '旁路由出口' : resource.cidr }}</code><span>{{ resource.protocol }} {{ resource.ports.join(', ') }} · {{ resource.enabled ? '启用' : '停用' }}</span><button class="secondary" :disabled="busy" @click="edit(resource)">修改</button><button class="secondary" :disabled="busy" @click="remove(resource)">删除</button></div>
    <div v-if="!resources.length" class="empty">尚未开放资源。添加资源后，只向指定账号授权。</div>
  </section>
  <section v-if="form" class="card">
    <h3>{{ editing ? '修改资源' : '添加资源' }}</h3>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <form class="resource-form" @submit.prevent="save">
      <label>资源名称<input v-model="form.name" required placeholder="例如：办公室文件服务器"></label>
      <label>服务类型<select v-model="preset" @change="applyPreset"><option v-for="item in resourcePresets" :key="item.id" :value="item.id">{{ item.name }}</option><option value="custom">自定义协议和端口</option></select></label>
      <label v-if="form.protocol !== 'internet'">设备 IP 或网段<input v-model="form.cidr" required placeholder="例如：192.168.18.20"></label>
      <p v-else class="muted">需要先在服务器菜单“高级网络与证书设置 → 互联网出口”配置旁路由，再向账号授权。</p>
      <template v-if="preset === 'custom'"><label>协议<select v-model="form.protocol"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="icmp">ICMP</option></select></label><label v-if="['tcp', 'udp'].includes(form.protocol)">端口<input v-model="form.ports" required placeholder="例如：8080,8443"></label></template>
      <p v-else-if="form.ports" class="muted">将开放 {{ form.protocol.toUpperCase() }} {{ form.ports }}。需要其他端口时选择自定义。</p>
      <label class="check"><input v-model="form.enabled" type="checkbox">启用此资源</label>
      <div class="actions"><button class="primary" :disabled="busy">保存资源</button><button type="button" class="secondary" :disabled="busy" @click="form = null">取消</button></div>
    </form>
    <details><summary>技术信息</summary><p>资源编号：{{ form.id }}（自动生成）</p></details>
  </section>
</template>
