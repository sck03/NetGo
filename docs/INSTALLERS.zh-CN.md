# 独立安装入口

服务端入口是仓库根目录的 `install-server.sh`；Windows 客户端入口是 `install-client.ps1`。两份脚本独立下载、独立审阅，不在一个脚本里切换客户端与服务端。当前目标为 **0.2.4-dev 开发版**，实际验证范围见 [本轮报告](evidence/2026-11-23-delivery/REPORT.zh-CN.md)。

远控及其他平台按 [v2.0 方案](REMOTE_NATIVE_PLAN.zh-CN.md)实现；目前没有对应安装产物。规划中的新数据库切换必须显式归档和新目录初始化，当前安装器不执行 schema 2 重置。开发测试使用项目目录内的缓存/运行副本，生产服务安装模式独立验证。

## Linux 服务端

支持 Debian 12/13 amd64 与 systemd。二进制开发包包含 NetGo 五个程序，以及原生 CGO 构建的 NetBird/Dex、NetBird daemon、Relay、Caddy、frpc、frps，不需要在目标机安装 Go/Node。首次安装自动安装缺少的运行依赖，不执行发行版升级或 apt autoremove。

下载、审阅后执行（root）：

```sh
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh -o install-server.sh
bash install-server.sh install --plan
bash install-server.sh
```

直接进入安装：`bash install-server.sh install`。支持单命令入口 `bash <(curl -fsSL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh) install`；需要先审阅时使用上面的下载步骤。脚本下载指定开发版本包及 SHA256SUMS，核对摘要和包内文件后运行安装器。下载失败会明确报错，可使用下面的本地包或源码方式，不将空下载视为安装成功。

菜单提供安装、初始化/恢复、状态、诊断和卸载。安装后本机维护入口：

```sh
netgo-serverctl status
netgo-serverctl doctor
netgo-serverctl configure
netgo-serverctl uninstall --yes
```

安装同一版本且所有文件匹配时不重复改动；同版本不同内容会拒绝覆盖。升级仍使用运维手册中的 `netgoctl upgrade/rollback` 事务，不冒充已完成版本升级演练。

无 GitHub 下载依赖的本地安装：

```sh
bash install-server.sh install --package NetGo-0.2.4-dev-linux-amd64.zip --sha256 从可信发行清单取得的摘要
bash install-server.sh install --source /root/NetGo完整包目录
```

从源码完整构建并安装：

```sh
bash install-server.sh install --from-source
# 可用完整 commit 或版本 tag 固定本次输入：
bash install-server.sh install --from-source --ref v0.2.4-dev
```

源码方式将解析出的 commit 记录在 `/var/cache/netgo/source-<commit>`，自动准备固定且校验过的 Go/Node，并原生构建带补丁的身份服务。编译器仅位于该源码目录的 `.tools`，不替换全局 Go/Node。Go 模块使用 `go.sum` 和校验数据库；默认镜像为 goproxy.cn，失败回退 proxy.golang.org，可用标准 `GOPROXY` 覆盖。冷构建需要时间和足够内存；4 GiB 主机默认按包串行编译/链接，内存不足时增加容量或使用完整二进制包。

## 首次配置

程序安装后提示控制域名/IP、Relay 域名/IP、LAN 网卡/网段、管理员邮箱与密码。密码从终端读取，不接受密码命令行参数。默认本地模式，不要求 VPS。

提供 TLS 目录时，其中应有 `control.crt`、`control.key`、`relay.crt`、`relay.key`；私有根可另外提供 `ca.crt`。证书必须匹配配置域名/IP、有效且与私钥匹配。实验室可显式选择 `--private-ca`，公钥 CA 随客户端站点配置导出，私钥留在 Linux。不要使用跳过 TLS 校验的选项。

非交互示例（密码通过已保护的标准输入提供）：

```sh
bash install-server.sh install --source /root/NetGo \
  --config /root/site-input.json --tls-dir /root/netgo-certificates \
  --admin-email admin@example.com --admin-alias admin --password-stdin
```

`--install-only` 只安装程序；之后用 `configure` 初始化。自动初始化当前覆盖本地 diagnostic 模板；可选 VPS 与 443 模板按运维手册追加并单独验证。默认公开端口为控制 TCP 10443、Relay TCP/UDP 18443、STUN UDP 3478、路由节点 UDP 51820。内部 API、健康和 Caddy 管理端口在配置中可调整，安装前检查占用，已有服务不会被自动停止。

初始化通过 NetBird 本机 API 创建唯一 IdP 管理员及管理 PAT、禁用初始 Default 全员互通策略、设置 overlay、注册一次性路由节点密钥，随后撤销该注册密钥。普通用户不共享 setup key。初始用户没有 LAN 资源，管理员需显式创建资源和授权。PAT 当前为一年有效期，到期前按 NetBird 管理 API 轮换受保护的 `management.token`。

`/etc/netgo/setup-state.json` 记录阶段；失败保留身份和受保护错误日志并停止本次服务。检查原因后执行 `netgo-serverctl configure --resume`，不会静默重置管理员或数据库。证书准备阶段失败需先按日志修复保留的配置。导出 `/etc/netgo/client-site.json` 不含账号密码、PAT 或私钥。

## Windows 客户端

在预期使用客户端的 Windows 桌面用户下，以管理员身份打开 PowerShell；不要换成另一个管理员账号，否则服务会绑定另一个 SID。默认面向 Windows x64，Windows 10 LTSC 与 Windows 11 的实际测试状态分别记录。

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/sck03/NetGo/main/install-client.ps1 -OutFile "$env:TEMP\install-client.ps1"
& "$env:TEMP\install-client.ps1" -Action Install -Plan
& "$env:TEMP\install-client.ps1" -Action Install
```

也可以在完整本地包中执行：

```powershell
.\install-client.ps1 -Action Install -PackageRoot C:\NetGo包目录
.\install-client.ps1 -Action Install -Package C:\NetGo-0.2.4-dev-windows-amd64.zip -Sha256 可信摘要
.\install-client.ps1 -Action Status
.\install-client.ps1 -Action Uninstall -Yes
```

安装器校验下载包、全部包内摘要和 Wintun 的 Authenticode 签名；特权核心放在 Program Files，桌面程序在当前用户目录，服务仅授权该用户 SID。普通使用无需管理员权限。开发版 NetGo 可执行文件尚无正式发行代码签名，不要求关闭系统安全保护；系统提升权限由用户正常批准。

本机卸载入口保留在 `%ProgramFiles%\NetGoNative\maintenance\install-client.ps1`，卸载不依赖 GitHub 重新下载。默认保留站点、系统凭据和核心配置，清除密码/站点可在客户端中明确操作。

## 卸载、恢复与验证边界

Linux 卸载校验归属清单，断开路由节点并停止/移除受管单元、链接和有正确归属的 NetGo 表；记录并恢复由本次初始化改变的 IPv4 转发设置。原本已启用的转发（例如 Docker 使用）保持原值。不清空全机防火墙，不卸载共享系统依赖。配置、账号、数据库、密钥、备份和受校验发行目录保留，可以重装同一包。

Windows 卸载先确认服务路径和用户 SID，断开后才删除明确的程序文件。安装失败保留受保护事务记录并恢复已有文件/服务状态。

安装、卸载、真实认证和隧道验证须分别查看证据。单台服务器上的隔离网络可验证真实内核转发与权限；不能自动证明公网、跨运营商 NAT、VPS 故障切换、Windows 休眠或 72 小时稳定性。
