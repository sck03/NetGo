# 独立平台构建与安装验证

本次按最新范围交付 Windows、Linux 桌面、Android、局域网 Linux 服务端和 VPS 辅助节点的独立脚本及五份 GitHub 工作流。macOS/iOS 暂缓，保留共享 C ABI、媒体/输入回调和安全存储接口。

## 已验证

- Windows：统一 `NetGo.exe`，8 项原生 CTest、TLS 夹具、系统 DLL 与体积检查通过；程序约 6.71 MiB。Setup 和绿色 ZIP 生成、校验通过，根目录只提供一个日常产品入口。网络准备器位于 `.netgo`，由程序调用。
- Android：Go VPN AAR、JNI、Kotlin、Debug 和未签名 Release APK、lint 及包 CRC/原生库检查通过。使用 AGP 9.5.2、Gradle 9.9.2、Kotlin 2.4.21、Compose BOM 2026.11.01、SDK 37.2、NDK 30.0.16248370，最低 API 31。没有移动设备实机结果。
- Linux GTK：此前在本次接续过程中于 Debian 13 原生构建，6 项 CTest、Go 客户端测试和 Xvfb 窗口创建/退出通过；没有物理桌面/Wayland 完整验收。
- VPS：Debian 13 原生构建 frps 0.72.0 和 NetBird Relay 0.83.2，上游针对性测试通过。真实 systemd 安装、全部受管服务状态与 HTTP 健康检查、Relay TLS/域名验证、停 Relay 后拒绝健康状态、卸载、重复卸载、保留证书、重装及最终卸载通过。测试使用独立 loopback TLS 配置；原 SSH、Docker、局域网 NetGo 的状态与 PID 保持一致。
- 安装工具：55 项本机 Python 工具测试通过，3 项因 POSIX 语义跳过；VPS 的 5 项测试另在 Debian 13 全部通过，含严格 umask、文件篡改拦截和生命周期模拟。PowerShell/Bash/Python 语法、安装计划和 actionlint 1.7.12 检查通过。
- 服务端/共享身份：此前本次接续中的 Go race 检查通过。局域网服务器现有服务保持运行；本轮没有对其执行新 schema 初始化或完整重新部署。

## 修复与边界

实机安装暴露并修复了严格 umask 导致程序/证书不可读、私有 CA 健康检查信任以及 TCP TIME_WAIT 被误判为活跃端口的问题。未解析的实验域名和不完整实验 TLS 证书曾触发失败，验证了服务停止与私有诊断保留；最终使用完整、专用测试证书通过。

VPS 配置导出不包含控制私钥、管理员凭据、原始 secrets.json 或 CA 私钥。两份 Linux 安装入口支持最新正式发布包校验、确定源码 commit 的自动构建、本地包/源码和本机卸载。GitHub Actions 的执行结果应以实际运行记录为准，语法检查与本机构建不能代替云端构建成功。

远端原 main 审查未发现缓存、构建目录、EXE/APK/AAR、数据库、压缩产物或私钥。Gradle wrapper JAR 属于必需的构建引导输入。重复历史日志属于带摘要的原始验证证据，保留其文件结构；生成的 Android AAR/APK、源码 JAR、签名文件和本地配置已忽略。两个根目录安装器临时文件移入项目 `.cache/cleanup-20261124`。

跨设备 overlay 音视频与输入、Windows UAC/锁屏/多屏、剪贴板与文件传输、Linux 主机采集与 Wayland、移动设备 VPN/远控和 72 小时验证仍未完成；当前构建包为开发交付，不标注为全部远控功能或全部设备验收完成。

原始构建、失败诊断与实机日志保存在受保护项目缓存，不把运行配置、凭据或完整机器日志提交到仓库。
