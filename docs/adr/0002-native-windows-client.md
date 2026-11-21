# ADR-0002：Windows 原生 C++ 客户端

日期：2026-11-22（Asia/Shanghai）。状态：接受；来自本线程用户明确要求。

Windows 产品界面和连接控制后台全部使用 C++20 / Win32，删除 Wails 桌面入口及网页客户端模式。Linux 管理服务、诊断 CLI 和管理网页沿用 Go / Vue。NetBird 是独立的上游网络核心，继续用其原始 Go 实现；不重写 WireGuard、ICE 或 Relay。

原生后台通过受当前用户 SID 保护的命名管道接受固定动作。网络核心的特权由 NetBird Windows 服务承担。C++ 后台负责站点存储、TLS 入口选择、Ed25519/PKCE 认证、会话续租、恢复、凭据与诊断。上游增加有界 JSON stdin/stdout 命令，转接现有受权 daemon RPC；密码和令牌不进入进程参数。

界面参考 sck03/ocserv-openwrt 的服务器列表、连接/断开、VPN 信息、独立日志、托盘和配置流程。自行实现 Win32 控件，不复制其 OpenConnect 协议代码。站点导出仅含公共连接信息；记住密码使用 Windows Credential Manager，设备密钥绑定当前用户与站点。证书先验证，再发送账号密码。

TLS 保持规范域名的 SNI/Host，连接目标可选直达或 VPS 地址。OpenSSL 使用 Windows 系统根和配置中的 PEM CA，校验证书链、用途、名称和有效期，不写全局 hosts。恢复失败时不得延长服务器租约，必须断开。

构建依赖固定版本与校验值。普通用户启动界面及后台；安装网络核心服务需要管理员权限。此架构仍须通过 A01–A33 的真实网络门槛才能发布。
