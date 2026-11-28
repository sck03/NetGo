import type { Resource } from './models.generated'
export type { Resource } from './models.generated'
export type ResourceForm = Omit<Resource, 'ports'> & { ports: string }
export type NetworkAccess = { lan_interface: string; local_cidrs: string[]; internet_enabled: boolean; gateway?: string; dns: string[]; fake_ip_cidr?: string }

export function validateResourceNetwork(resource: Resource, network: NetworkAccess): void {
  if (!resource.enabled) return
  if (resource.protocol === 'internet') {
    if (!network.internet_enabled) throw Error('请先在服务器菜单中配置互联网出口，再启用旁路由资源')
    return
  }
  const prefix = (cidr: string) => {
    const [address, length] = cidr.split('/')
    return { address: address!.split('.').reduce((n, v) => (n * 256 + Number(v)) >>> 0, 0), bits: Number(length) }
  }
  const target = prefix(resource.cidr)
  if (!network.local_cidrs.some(cidr => {
    const lan = prefix(cidr), mask = (0xffffffff << (32 - lan.bits)) >>> 0
    return lan.bits <= target.bits && ((target.address & mask) >>> 0) === lan.address
  })) throw Error('资源地址必须位于服务器配置的局域网网段内；请修改目标地址或先在服务器菜单中添加网段')
}

export const resourcePresets = [
  { id: 'web', name: '网页服务（HTTPS）', protocol: 'tcp', ports: '443' },
  { id: 'ssh', name: 'Linux 终端（SSH）', protocol: 'tcp', ports: '22' },
  { id: 'files', name: '文件共享（SMB）', protocol: 'tcp', ports: '445' },
  { id: 'rdp', name: 'Windows RDP 服务', protocol: 'tcp', ports: '3389' },
  { id: 'ping', name: '网络连通检查（Ping）', protocol: 'icmp', ports: '' },
  { id: 'internet', name: '互联网出口（旁路由）', protocol: 'internet', ports: '' },
] as const

export function resourcePayload(form: ResourceForm): Resource {
  const name = form.name.trim()
  if (!name) throw Error('请填写资源名称')
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$/.test(form.id)) throw Error('资源编号无效，请重新添加资源')
  if (!['tcp', 'udp', 'icmp', 'internet'].includes(form.protocol)) throw Error('请选择服务类型')
  let cidr = '0.0.0.0/0'
  if (form.protocol !== 'internet') {
    const parts = form.cidr.trim().split('/')
    const octets = parts[0]!.split('.')
    const bits = parts.length === 1 ? 32 : Number(parts[1])
    if (parts.length > 2 || octets.length !== 4 || octets.some(v => !/^(0|[1-9][0-9]{0,2})$/.test(v) || Number(v) > 255) || (parts.length === 2 && !/^[0-9]{1,2}$/.test(parts[1]!)) || bits < 1 || bits > 32) throw Error('请输入 IPv4 地址或网段，例如 192.168.18.20 或 192.168.18.0/24')
    const value = octets.reduce((n, v) => (n * 256 + Number(v)) >>> 0, 0)
    const masked = (value & (0xffffffff << (32 - bits))) >>> 0
    const network = [24, 16, 8, 0].map(shift => (masked >>> shift) & 255).join('.')
    if (value !== masked) throw Error('此网段应填写 ' + network + '/' + bits + '；只访问这台设备请直接填写 IP')
    cidr = octets.join('.') + '/' + bits
  }
  const ports = ['icmp', 'internet'].includes(form.protocol) ? [] : [...new Set(form.ports.split(',').map(v => {
    const text = v.trim(), port = Number(text)
    if (!/^[0-9]+$/.test(text) || !Number.isInteger(port) || port < 1 || port > 65535) throw Error('端口必须为 1–65535 的整数，多个端口用逗号分隔')
    return port
  }))]
  if (ports.length > 64) throw Error('单个资源最多配置 64 个端口')
  return { id: form.id, name, cidr, protocol: form.protocol, ports, enabled: form.enabled, edit_token: form.edit_token }
}
