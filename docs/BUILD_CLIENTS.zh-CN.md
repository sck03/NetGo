# 独立客户端与服务端构建

2026-11-24：Windows、Linux 桌面、Android、macOS、iOS 客户端、Linux 服务端和 VPS 分别构建。服务端与安装维护使用 Go；Python 仅作为开发构建工具，最终节点包无需 Python。

| 目标 | 独立脚本 | GitHub Actions | 产物 |
| --- | --- | --- | --- |
| Windows 客户端 | `scripts/build-windows-client.ps1` | Windows client | `dist/windows-client`：Setup、绿色版、摘要；日常只有 NetGo.exe 入口 |
| Linux 桌面客户端 | `scripts/build-linux-client.sh` | Linux desktop client | `dist/NetGo-*-linux-client-amd64.tar.gz` |
| Android 客户端 | `scripts/build-android-client.py` | Android client | `dist/android-client`：调试 APK、未签名 APK、摘要 |
| Linux 服务端 | `scripts/build-linux-server.sh` | Linux server | `dist/linux-server`：完整服务端及验证记录 |
| VPS 辅助节点 | `scripts/build-vps.sh` | VPS auxiliary node | `dist/vps-package`：frps、Relay、独立维护工具与摘要 |
| macOS 客户端 | `scripts/build-macos-client.py` | macOS client | SwiftUI App、Go 后台、NetBird 网络组件 |
| iOS/iPadOS 客户端 | `scripts/build-ios-client.py` | iOS client | 模拟器 ZIP、未签名设备 IPA；可用 --team 签名 |

在 Actions 中选择对应工作流并点击 Run workflow。七个目标各有独立工作流，共享内部工具和会话接口。

Windows 需要固定 Go、UCRT64 GCC/CMake/Ninja、Python cryptography，脚本准备 OpenSSL/JSON/Wintun。Linux 桌面需要 GTK4、OpenSSL、FFmpeg、libcurl 开发包；网络 daemon 的安装与物理桌面另验。Android 最低 Android 12/API31，构建使用 JDK17、SDK37.2、NDK30.0.16248370；Go/Gradle/SDK 缓存在项目目录，VPN 复用现有账号与设备身份，查看器使用共享 C ABI。上游 anet 0.0.5 按其文档使用 `-checklinkname=0`，并锁定 Go1.27.4；已核对其 zone-cache 结构与该版本一致。

GitHub 构建成功表示相应构建检查通过。未签名 APK/IPA 需要签名才能用于设备安装；构建不替代 VPN、远控、权限或长期运行验收。Apple 使用稳定 Xcode，macOS 14+、iOS 17+，媒体和网络接口与其他平台共享。

Actions 使用官方最新稳定版本并固定提交 SHA；Go 1.27.4、Node 26.13.1 LTS、AGP 9.5.2、Gradle 9.9.2、Kotlin 2.4.21、Compose BOM 2026.11.01 已于 2026-11-24 查询。固定版本便于重现，后续升级先查询官方稳定发布，再更新锁定输入并验证。

VPS 的安装与卸载单独使用 `install-vps.sh`，局域网 Linux 服务端使用 `install-server.sh`。流程见 [独立安装说明](INSTALLERS.zh-CN.md)。
