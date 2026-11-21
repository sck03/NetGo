# NetGo

Linux 局域网远程接入服务与原生 Windows 客户端。账号、权限和控制服务保留在本地 Linux；公网入口可达时不需要 VPS，预先配置 VPS 后提供备用控制入口和 NetBird 中继。

**Windows 界面与产品后台已改为 C++20 / Win32。** 旧 Wails 桌面代码、网页桌面模式、Go Windows IPC/凭据实现及旧运行产物已清理。Linux 管理服务使用 Go，管理网页使用 Vue，独立 NetBird 核心保留上游语言。

当前版本：0.2.0-dev。源码、自动测试、Windows 可执行文件和 NetGo Linux 交叉编译文件已生成；真实 LAN/NAT/VPS、跨用户安装、权限阻断和 72 小时验收尚未完成，不能按生产发行版宣称通过。

## 审阅入口

- [完整实施方案 v1.1](docs/EXECUTION_PLAN.zh-CN.md)
- [分阶段进度](docs/PROGRESS.zh-CN.md)
- [任务状态与实施清单](docs/IMPLEMENTATION_BACKLOG.zh-CN.md)
- [实际验证报告](docs/evidence/implementation/REPORT.zh-CN.md)
- [构建、安装与运维](docs/OPERATIONS.zh-CN.md)
- [网络验收手册](docs/NETWORK_ACCEPTANCE.zh-CN.md)
- [Windows 原生架构决策](docs/adr/0002-native-windows-client.md)
- [固定版本](upstream/versions.lock.json)与[第三方通知](THIRD_PARTY_NOTICES.md)

## 构建

Windows：安装 MSYS2 UCRT64 编译工具和带 cryptography 的 Python，准备锁定 Go/Node，执行：

    .\scripts\build.ps1 -Toolchain D:\msys64\ucrt64

Linux：执行 bash scripts/build.sh 与 bash scripts/build-upstream.sh。内嵌 SQLite IdP 必须在 Linux 使用 CGO 构建。安装前先运行 Windows install.ps1 -Plan 或审阅 Linux 配置；默认不在开发工作站安装或启用 VPN。

核心源码位于 apps/windows、cmd、internal、apps/ui；补丁位于 upstream/patches。客户端包括服务器列表、账号密码认证、连接/断开、控制/数据路径状态、资源列表、托盘、密码保护、自动恢复和脱敏诊断。

旧 ARCHITECTURE_PLAN 保留为历史参考；第一版不包括 OpenWrt 服务端、互联网出口代理或多个站点同时连接。
