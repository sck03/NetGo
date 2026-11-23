# NetGo

当前实现提供 Linux 局域网远程接入与原生 Windows 客户端。账号、权限和控制服务保留在本地 Linux；公网入口可达时不需要 VPS，预先配置 VPS 后提供备用控制入口和 NetBird 中继。执行方案 v2.2 已将内置远程桌面和 Windows/Android/macOS/iOS/Linux 原生客户端纳入下一功能版本。

可选 VPS 只需一台：同时运行 frps（备用登录/控制）和 NetBird Relay/STUN（直连失败时的 VPN 业务中继）。多个示例域名可指向同一 VPS IP；不要求两台 VPS。

**Windows 界面与产品后台已改为 C++20 / Win32。** 旧 Wails 桌面代码、网页桌面模式、Go Windows IPC/凭据实现及旧运行产物已清理。Linux 管理服务使用 Go，管理网页使用 Vue，独立 NetBird 核心保留上游语言。

当前开发版本：**0.2.4-dev**。服务端支持 Debian 12/13 amd64；Windows 客户端采用紧凑原生窗口，连接、VPN 信息、资源、日志分页。当前实机为 Debian 13 与 Windows 10 LTSC 1809 x64，Debian 12 结果作为历史证据保留。完整服务安装、真实认证、权限及公网 VPS 的最新结果见 [本轮报告](docs/evidence/2026-11-23-continuation/REPORT.zh-CN.md)。Windows 11、跨用户 Windows、休眠、真实多 WAN/DNS 与 72 小时验收分别登记。

下一目标 **0.3.0-dev / API v2 / schema 2** 尚未实现。统一产品、按需启用远控；Windows 使用 C++/Win32，Android 使用 Kotlin，Apple 使用 Swift，Linux 桌面使用 C++/GTK，共享会话/协议核心通过 C ABI 衔接。允许重建开发数据库与接口，不维护旧接口兼容层。已有独立会话门禁、[Go/C++ 签名票据适配](docs/REMOTE_TICKET.zh-CN.md)和 Windows DXGI GPU 采集原型，主机协调器、真实媒体链路及其他原生客户端仍未完成。任务见 [远控与原生多平台方案](docs/REMOTE_NATIVE_PLAN.zh-CN.md)，融合约束和跨模块验收见 [统一集成契约](docs/INTEGRATION_CONTRACT.zh-CN.md)，不能把当前 VPN 包当成已含远控的版本。最新构建、实机复验和体积记录见 [接续报告](docs/evidence/2026-11-23-continuation/REPORT.zh-CN.md)。

## 独立安装入口

| 安装对象 | 独立脚本 | 操作 |
| --- | --- | --- |
| Debian 服务端 | [install-server.sh](install-server.sh) | 安装、首次配置、状态、诊断、卸载与保留数据重装 |
| Windows 客户端 | [install-client.ps1](install-client.ps1) | 安装、状态、卸载；普通用户运行桌面客户端 |

脚本提供菜单、包摘要校验、离线安装及本机维护入口。先下载审阅，再执行；两端参数和首次配置见 [安装说明](docs/INSTALLERS.zh-CN.md)。默认下载对应版本的开发包。

```sh
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh -o install-server.sh
bash install-server.sh install --plan
bash install-server.sh
```

Windows 在预期桌面用户的管理员 PowerShell 中执行：

```powershell
Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/sck03/NetGo/main/install-client.ps1 -OutFile install-client.ps1
.\install-client.ps1 -Action Install -Plan
.\install-client.ps1
```

面向国内动态公网线路，本地默认使用控制 TCP 10443、Relay TCP/UDP 18443，可按运营商允许的端口调整。无需开放本地 443；VPS 可独立使用高位端口或 443。HTTPS 域名证书不绑定端口，使用 DNS-01 可避免依赖入站 80/443。

H3C 多宽带场景同时考虑按连接负载均衡与主备切换：路由器管理各 WAN 的拨号、端口映射和回程，NetGo 管理多入口与恢复。见 [多 WAN 部署说明](docs/MULTI_WAN.zh-CN.md)及 examples/site-multiwan.json。

## 审阅入口

- [完整实施方案 v2.2](docs/EXECUTION_PLAN.zh-CN.md)
- [远控与原生多平台可执行任务](docs/REMOTE_NATIVE_PLAN.zh-CN.md)
- [统一身份、网络与远控的集成契约](docs/INTEGRATION_CONTRACT.zh-CN.md)
- [v2.0 接续、修复与验证](docs/evidence/2026-11-23-continuation/REPORT.zh-CN.md)
- [Debian 13 与公网 VPS 接续交付](docs/evidence/2026-11-23-delivery/REPORT.zh-CN.md)
- [历史安装与实机验证记录](docs/evidence/2026-11-22-installers/REPORT.zh-CN.md)
- [历史 v1.6 连续实施与审阅记录](docs/evidence/2026-11-22-plan-v1.6/REPORT.zh-CN.md)
- [Windows 客户端使用说明](docs/WINDOWS_CLIENT.zh-CN.md)
- [历史 v1.5 新线程接续与交付终检](docs/evidence/2026-11-22-handoff/REPORT.zh-CN.md)
- [历史 v1.5 完整性复核与实施报告](docs/evidence/2026-11-22-completion/REPORT.zh-CN.md)
- [分阶段进度](docs/PROGRESS.zh-CN.md)
- [任务状态与实施清单](docs/IMPLEMENTATION_BACKLOG.zh-CN.md)
- [方案条款、源码与验收追踪](docs/IMPLEMENTATION_MATRIX.zh-CN.md)
- [实际验证报告](docs/evidence/implementation/REPORT.zh-CN.md)
- [0.2.1-dev 续作与审阅报告](docs/evidence/0.2.1-dev/REPORT.zh-CN.md)
- [上轮 v1.4 分段修复与验证报告](docs/evidence/2026-11-22-review/REPORT.zh-CN.md)
- [历史 33 项待验收汇总](docs/evidence/2026-11-22-plan-v1.6/network-summary/REPORT.zh-CN.md)
- [构建、安装与运维](docs/OPERATIONS.zh-CN.md)
- [网络验收手册](docs/NETWORK_ACCEPTANCE.zh-CN.md)
- [Windows 原生架构决策](docs/adr/0002-native-windows-client.md)
- [固定版本](upstream/versions.lock.json)与[第三方通知](THIRD_PARTY_NOTICES.md)

## 构建

Windows：安装 MSYS2 UCRT64 编译工具和带 cryptography 的 Python，准备锁定 Go/Node，执行：

    .\scripts\build.ps1 -Toolchain D:\msys64\ucrt64

Debian 12/13 amd64：可通过 `bash install-server.sh install --from-source` 自动准备锁定工具链、构建并安装；只构建时按运维手册执行 `scripts/bootstrap-linux.sh`、`scripts/build.sh` 与 `scripts/build-upstream.sh`。内嵌 SQLite IdP 必须原生启用 CGO，完整服务端包包含六个上游组件。安装前可运行 --plan/-Plan。

核心源码位于 apps/windows、cmd、internal、apps/ui；补丁位于 upstream/patches。客户端包括服务器列表、账号密码认证、连接/断开、控制/数据路径状态、资源列表、托盘、密码保护、自动恢复和脱敏诊断。

完成依赖准备后，Windows 使用 `scripts/verify.ps1 -Output 新目录`；Debian 使用 `python3 -B scripts/verify-linux.py --output 新目录`（可先加 --plan）。检查绑定源码、日志和产物摘要。Windows/源码打包使用 `python -B scripts/package.py --version 0.2.4-dev --verification 验证目录/verification.json`；完整 Linux 服务端使用 `python3 -B scripts/package-server.py --verification 验证目录/verification.json`。验收工具见 [验收工具说明](tests/network/README.zh-CN.md)。

旧 ARCHITECTURE_PLAN 保留为历史参考；第一版不包括 OpenWrt 服务端、互联网出口代理或多个站点同时连接。
