# NetGo

当前源码提供 Linux 控制服务、Windows、Linux GTK、Android、macOS 和 iOS 原生客户端。账号与权限保留在本地 Linux；可选 VPS 提供备用控制入口和 NetBird 中继。Apple 构建已接入独立脚本，签名和实机验收需设备环境。

可选 VPS 只需一台：同时运行 frps（备用登录/控制）和 NetBird Relay/STUN（直连失败时的 VPN 业务中继）。多个示例域名可指向同一 VPS IP；不要求两台 VPS。

**Windows 界面与产品后台已改为 C++20 / Win32。** 旧 Wails 桌面代码、网页桌面模式、Go Windows IPC/凭据实现及旧运行产物已清理。Linux 管理服务使用 Go，管理网页使用 Vue，独立 NetBird 核心保留上游语言。

当前源码版本：**0.3.0-dev / API v2 / SQLite schema 2**，不兼容旧开发接口和数据库。Windows 普通使用入口统一为 **NetGo.exe**，主界面、连接后台与远控共用同一个程序文件，通过内部模式隔离运行；无需分别启动多个 EXE。安装版提供 Setup，绿色版解压后启动 NetGo.exe，网络服务准备由客户端调用。Windows 尚未完成全部设备验收。

已实现主机登记、分权授权、绑定票据、持钥证明、持久一次消费、远控续租与最小 peer/端口策略。Windows 已接入 DXGI、系统 H.264 编解码、键鼠/文本输入和 WASAPI 音频；Windows 与 GTK 查看器复用 [共享 C 接口](packages/remote-client/README.md)。本地编解码、真实 TLS/持钥证明测试及编译只提供局部证据，跨设备 overlay 画面/输入/音频、锁屏/UAC、多屏、文件/剪贴板、移动/Apple 客户端与 72 小时验收仍未完成。任务见 [远控与五平台方案](docs/REMOTE_NATIVE_PLAN.zh-CN.md)和 [集成契约](docs/INTEGRATION_CONTRACT.zh-CN.md)。

## 独立安装入口

各平台的 [构建脚本/GitHub 工作流](docs/BUILD_CLIENTS.zh-CN.md) 分别维护。Linux/VPS 的安装、证书、端口和菜单由 Go 工具实现，最终安装包无需 Python；Python 只用于开发侧构建、打包与验证。

| 安装对象 | 独立脚本 | 操作 |
| --- | --- | --- |
| Debian 服务端 | [install-server.sh](install-server.sh) | 安装、首次配置、状态、诊断、卸载与保留数据重装 |
| VPS 辅助节点 | [install-vps.sh](install-vps.sh) | 独立安装、导入 VPS 专用配置、状态、诊断、卸载与保留配置重装 |
| Windows 客户端 | NetGo-Setup.exe | 图形安装/系统卸载；日常仅启动 NetGo.exe |

LAN 快捷命令 `ng`，VPS 快捷命令 `ngv`，各自提供安装、设置、证书、端口、诊断和卸载菜单。参数见 [安装说明](docs/INSTALLERS.zh-CN.md)。远程一键下载使用 Release；未发布时，从 Actions 下载对应平台包，解压后运行包内安装脚本。安装过程无需编译器或 Python。

```sh
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh -o install-server.sh
bash install-server.sh install --plan
bash install-server.sh
```

Windows 双击 Setup 安装，从开始菜单打开 NetGo；绿色版解压后双击 NetGo.exe，首次网络授权由程序发起。普通用户无需运行 PowerShell 维护脚本。

旁路由互联网出口单独设置 IP、DNS 和 DNS 模式，支持 Clash/Mihomo 的 `198.18.0.0/15` Fake-IP。**只有获得“互联网（旁路由）”资源授权的账号才可使用**，客户端默认关闭互联网模式；撤权会关闭租约转发并清理对应连接。局域网权限继续单独控制。

客户端不再以 30 秒本机时差拒绝连接：验证 TLS 和站点身份后，采用服务端时间与单调经过时间进行签名、刷新和租约检查，保留防重放和会话到期保护。

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

Debian 12/13 amd64：开发侧执行 `scripts/bootstrap-linux.sh`、`scripts/build.sh` 与 `scripts/build-upstream.sh`。服务端包只保留四个项目程序和五个 LAN 上游程序；VPS 包独立包含 frps、Relay 和 Go 维护工具。安装前可运行 --plan。

核心源码位于 apps/windows、cmd、internal、apps/ui；补丁位于 upstream/patches。客户端包括服务器列表、账号密码认证、连接/断开、控制/数据路径状态、资源列表、托盘、密码保护、自动恢复和脱敏诊断。

开发验证使用 `scripts/verify.ps1` 或 `scripts/verify-linux.py`，各平台通过独立脚本打包；源码包使用 `scripts/package-source.py`。检查绑定源码、日志和产物摘要。验收工具见 [验收工具说明](tests/network/README.zh-CN.md)。

旧 ARCHITECTURE_PLAN 保留为历史参考。旁路由本身由外部设备提供代理能力，NetGo 管理获授权 VPN 账号到该设备的接入；目前仍不支持 OpenWrt 服务端或多个站点同时连接。
