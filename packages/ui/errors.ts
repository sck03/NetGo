const messages: Record<string, string> = {
  edit_conflict: '此账号已被其他操作修改。请重新打开编辑，核对最新权限后保存。',
  control_unavailable: '暂时无法连接身份或控制服务，请稍后重试。',
  site_not_ready: '站点网关尚未就绪，请先完成服务器初始化。',
  last_permanent_administrator: '必须保留至少一个启用且不过期的管理员。请先设置另一位管理员，再修改此账号。',
  remote_user_inactive: '此账号已停用、到期或正在删除，无法授予远程桌面权限。',
  account_creation_pending: '此用户名或邮箱仍有待核对的创建任务，请等待后台完成清理后重试。',
  account_creation_uncertain: '身份服务未返回完整的创建结果。后台会核对并清理未完成的账号，请勿重复创建。',
}

export function requestError(code: unknown): string {
  if (typeof code !== 'string' || !code) return '请求失败'
  return messages[code.split(':', 1)[0]] || code
}

export class RequestError extends Error {
  readonly code: string
  readonly status: number
  constructor(code: string, status: number) {
    super(requestError(code))
    this.code = code
    this.status = status
  }
}
