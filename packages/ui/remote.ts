export const remotePermissions = [
  { bit: 1, name: '观看' }, { bit: 2, name: '键鼠输入' }, { bit: 4, name: '音频' },
  { bit: 8, name: '剪贴板' }, { bit: 16, name: '上传' }, { bit: 32, name: '下载' },
]
export function hostStatusLabel(status: string): string {
  return ({ ready: '可连接', busy: '正在使用', offline: '主机失联', disabled: '共享已关闭', vpn_offline: 'VPN 未连接' } as Record<string, string>)[status] ?? '状态未知'
}
export function remoteCapabilities(selected: number, supported: number): number {
  if (!Number.isInteger(selected) || !Number.isInteger(supported) || !(supported & 1)) return 0
  return (selected | 1) & supported & 63
}

export function expiryInput(iso: string): string {
  if (!iso || iso.startsWith('0001-')) return ''
  const date = new Date(iso)
  if (!Number.isFinite(date.getTime())) return ''
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}

export function expiryPayload(local: string): string | undefined {
  if (!local) return undefined
  const date = new Date(local)
  if (!Number.isFinite(date.getTime())) throw new Error('请填写有效的到期时间')
  return date.toISOString()
}
