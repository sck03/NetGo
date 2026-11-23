# NetGo

当前源码提供 Linux 控制服务、原生 Windows 客户端、Linux GTK 查看器和 Android 客户端。账号、权限和控制服务保留在本地 Linux；公网入口可达时不需要 VPS，预先配置 VPS 后提供备用控制入口和 NetBird 中继。macOS/iOS 按当前要求暂缓，保留平台扩展接口，逐项状态见执行方案。

可选 VPS 只需一台：同时运行 frps（备用登录/控制）和 NetBird Relay/STUN（直连失败时的 VPN 业务中继）。多个示例域名可指向同一 VPS IP；不要求两台 VPS。

**Windows 界面与产品后台已改为 C++20 / Win32。** 旧 Wails 桌面代码、网页桌面模式、Go Windows IPC/凭据实现及旧运行产物已清理。Linux 管理服务使用 Go，管理网页使用 Vue，独立 NetBird 核心保留上游语言。

当前源码版本：**0.3.0-dev / API v2 / SQLite schema 2**，不兼容旧开发接口和数据库。Windows 普通使用入口统一为 **NetGo.exe**，主界面、连接后台与远控共用同一个程序文件，通过内部模式隔离运行；无需分别启动多个 EXE。安装版提供 Setup，绿色版解压后启动 NetGo.exe，网络服务准备由客户端调用。Windows 尚未完成全部设备验收。

已实现主机登记、分权授权、绑定票据、持钥证明、持久一次消费、远控续租与最小 peer/端口策略。Windows 已接入 DXGI、系统 H.264 编解码、键鼠/文本输入和 WASAPI 音频；Windows 与 GTK 查看器复用 [共享 C 接口](packages/remote-client/README.md)。本地编解码、真实 TLS/持钥证明测试及编译只提供局部证据，跨设备 overlay 画面/输入/音频、锁屏/UAC、多屏、文件/剪贴板、移动/Apple 客户端与 72 小时验收仍未完成。任务见 [远控与五平台方案](docs/REMOTE_NATIVE_PLAN.zh-CN.md)和 [集成契约](docs/INTEGRATION_CONTRACT.zh-CN.md)。

## 独立安装入口

Windows、Linux 桌面、Android 客户端与 Linux 服务端的 [独立构建脚本/GitHub 工作流](docs/BUILD_CLIENTS.zh-CN.md) 已分开。按用户最新要求，macOS/iOS 暂缓开发，保留共享 C ABI 和平台适配接口。

| 安装对象 | 独立脚本 | 操作 |
| --- | --- | --- |
| Debian 服务端 | [install-server.sh](install-server.sh) | 安装、首次配置、状态、诊断、卸载与保留数据重装 |
| VPS 辅助节点 | [install-vps.sh](install-vps.sh) | 独立安装、导入 VPS 专用配置、状态、诊断、卸载与保留配置重装 |
| Windows 客户端 | NetGo-Setup.exe；[维护脚本](install-client.ps1) | 图形安装/系统卸载；日常仅启动 NetGo.exe |

脚本提供菜单、包摘要校验、离线安装及本机维护入口。参数和首次配置见 [安装说明](docs/INSTALLERS.zh-CN.md)。Linux/VPS 默认查询最新正式 Release；尚无对应发布包时解析 main 为固定 commit，再构建安装。

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

完成依赖准备后，Windows 使用 `scripts/verify.ps1 -Output 新目录`；Debian 使用 `python3 -B scripts/verify-linux.py --output 新目录`（可先加 --plan）。检查绑定源码、日志和产物摘要。Windows/源码打包使用 `python -B scripts/package.py --version 0.3.0-dev --verification 验证目录/verification.json`；完整 Linux 服务端使用 `python3 -B scripts/package-server.py --verification 验证目录/verification.json`。验收工具见 [验收工具说明](tests/network/README.zh-CN.md)。

旧 ARCHITECTURE_PLAN 保留为历史参考；第一版不包括 OpenWrt 服务端、互联网出口代理或多个站点同时连接。
