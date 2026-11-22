# NetGo

Linux 局域网远程接入服务与原生 Windows 客户端。账号、权限和控制服务保留在本地 Linux；公网入口可达时不需要 VPS，预先配置 VPS 后提供备用控制入口和 NetBird 中继。

可选 VPS 只需一台：同时运行 frps（备用登录/控制）和 NetBird Relay/STUN（直连失败时的 VPN 业务中继）。多个示例域名可指向同一 VPS IP；不要求两台 VPS。

**Windows 界面与产品后台已改为 C++20 / Win32。** 旧 Wails 桌面代码、网页桌面模式、Go Windows IPC/凭据实现及旧运行产物已清理。Linux 管理服务使用 Go，管理网页使用 Vue，独立 NetBird 核心保留上游语言。

当前开发版本：0.2.3-dev。服务端基准为 **Debian 13 amd64**。本轮修复 Linux 重连与异步路由、gRPC 多入口探测，补齐低位端口权限及源码/验证/开发包的关联检查；15 段完整自动检查和三个本地开发包校验已通过，真实 LAN/NAT/VPS、跨用户安装、权限阻断和 72 小时验收仍未完成。

面向国内动态公网线路，本地默认使用控制 TCP 10443、Relay TCP/UDP 18443，可按运营商允许的端口调整。无需开放本地 443；VPS 可独立使用高位端口或 443。HTTPS 域名证书不绑定端口，使用 DNS-01 可避免依赖入站 80/443。

H3C 多宽带场景同时考虑按连接负载均衡与主备切换：路由器管理各 WAN 的拨号、端口映射和回程，NetGo 管理多入口与恢复。见 [多 WAN 部署说明](docs/MULTI_WAN.zh-CN.md)及 examples/site-multiwan.json。

## 审阅入口

- [完整实施方案 v1.6](docs/EXECUTION_PLAN.zh-CN.md)
- [本轮连续实施与审阅记录](docs/evidence/2026-11-22-plan-v1.6/REPORT.zh-CN.md)
- [历史 v1.5 新线程接续与交付终检](docs/evidence/2026-11-22-handoff/REPORT.zh-CN.md)
- [历史 v1.5 完整性复核与实施报告](docs/evidence/2026-11-22-completion/REPORT.zh-CN.md)
- [分阶段进度](docs/PROGRESS.zh-CN.md)
- [任务状态与实施清单](docs/IMPLEMENTATION_BACKLOG.zh-CN.md)
- [方案条款、源码与验收追踪](docs/IMPLEMENTATION_MATRIX.zh-CN.md)
- [实际验证报告](docs/evidence/implementation/REPORT.zh-CN.md)
- [0.2.1-dev 续作与审阅报告](docs/evidence/0.2.1-dev/REPORT.zh-CN.md)
- [上轮 v1.4 分段修复与验证报告](docs/evidence/2026-11-22-review/REPORT.zh-CN.md)
- [Debian 13 的 33 项待验收汇总](docs/evidence/2026-11-22-plan-v1.6/network-summary/REPORT.zh-CN.md)
- [构建、安装与运维](docs/OPERATIONS.zh-CN.md)
- [网络验收手册](docs/NETWORK_ACCEPTANCE.zh-CN.md)
- [Windows 原生架构决策](docs/adr/0002-native-windows-client.md)
- [固定版本](upstream/versions.lock.json)与[第三方通知](THIRD_PARTY_NOTICES.md)

## 构建

Windows：安装 MSYS2 UCRT64 编译工具和带 cryptography 的 Python，准备锁定 Go/Node，执行：

    .\scripts\build.ps1 -Toolchain D:\msys64\ucrt64

Debian 13 amd64：按运维手册准备基础依赖和锁定 Go/Node，执行 `python3 -B scripts/linux_preflight.py --mode build`、`bash scripts/build.sh` 与 `bash scripts/build-upstream.sh`。内嵌 SQLite IdP 必须原生启用 CGO；后者同时构建 Caddy/frp。安装前运行对应脚本的 --plan/-Plan；开发工作站不自动启用 VPN。

核心源码位于 apps/windows、cmd、internal、apps/ui；补丁位于 upstream/patches。客户端包括服务器列表、账号密码认证、连接/断开、控制/数据路径状态、资源列表、托盘、密码保护、自动恢复和脱敏诊断。

完成依赖准备后，Windows 使用 `scripts/verify.ps1 -Output 新目录`；Debian 使用 `python3 -B scripts/verify-linux.py --output 新目录`（可先加 --plan）。检查包括源码/产物摘要、工具回归、Go 竞态/vet、前端、部署配置、原生/上游构建和产品清单。开发打包使用 `python -B scripts/package.py --version 0.2.3-dev --verification 验证目录/verification.json`，拒绝跳过检查、源码变化或未验证的新构建，并产生包校验记录。验收证据工具见 [验收工具说明](tests/network/README.zh-CN.md)。

旧 ARCHITECTURE_PLAN 保留为历史参考；第一版不包括 OpenWrt 服务端、互联网出口代理或多个站点同时连接。
