# 独立客户端与服务端构建

2026-11-24 最新范围：Windows、Linux 桌面、Android 客户端、Linux 局域网服务端和 VPS 辅助节点分别构建。macOS/iOS 暂缓；纯 C ABI、设备安全存储接口和媒体回调保留以后接 Swift/ObjC++。

| 目标 | 独立脚本 | GitHub Actions | 产物 |
| --- | --- | --- | --- |
| Windows 客户端 | `scripts/build-windows-client.ps1` | Windows client | `dist/windows-client`：Setup、绿色版、摘要；日常只有 NetGo.exe 入口 |
| Linux 桌面客户端 | `scripts/build-linux-client.sh` | Linux desktop client | `dist/NetGo-*-linux-client-amd64.tar.gz` |
| Android 客户端 | `scripts/build-android-client.py` | Android client | `dist/android-client`：调试 APK、未签名 APK、摘要 |
| Linux 服务端 | `scripts/build-linux-server.sh` | Linux server | `dist/linux-server`：完整服务端及验证记录 |
| VPS 辅助节点 | `scripts/build-vps.sh` | VPS auxiliary node | `dist/vps-package`：frps、Relay、独立维护工具与摘要 |

在仓库 Actions 中选择对应工作流，点击 Run workflow，可单独生成该平台产物。五个工作流各用一份文件，分别构建；共享工具处理上游固定输入、会话契约与内部打包函数。客户端不会调用服务端构建脚本。

Windows 需要固定 Go、UCRT64 GCC/CMake/Ninja、Python cryptography，脚本准备 OpenSSL/JSON/Wintun。Linux 桌面需要 GTK4、OpenSSL、FFmpeg、libcurl 开发包；网络 daemon 的安装与物理桌面另验。Android 最低 Android 12/API31，构建使用 JDK17、SDK37.2、NDK30.0.16248370；Go/Gradle/SDK 缓存在项目目录，VPN 复用现有账号与设备身份，查看器使用共享 C ABI。上游 anet 0.0.5 按其文档使用 `-checklinkname=0`，并锁定 Go1.27.4；已核对其 zone-cache 结构与该版本一致。

GitHub 构建成功表示对应源码编译、测试与包检查完成。调试 APK可安装测试，未签名 APK需要正式签名；构建记录不替代 VPN/远控的物理设备、网络路径、权限及长期运行验收。Apple 支持以后单独增加自己的平台适配、构建脚本和签名工作流，不把它们加入当前入口。

Actions 使用官方最新稳定版本并固定提交 SHA；Go 1.27.4、Node 26.13.1 LTS、AGP 9.5.2、Gradle 9.9.2、Kotlin 2.4.21、Compose BOM 2026.11.01 已于 2026-11-24 查询。固定版本便于重现，后续升级先查询官方稳定发布，再更新锁定输入并验证。

VPS 的安装与卸载单独使用 `install-vps.sh`，局域网 Linux 服务端使用 `install-server.sh`。流程见 [独立安装说明](INSTALLERS.zh-CN.md)。
