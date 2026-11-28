# 执行方案与源码追踪

修订：2026-11-28。当前 API v2 / schema 8。本轮补齐原生刷新恢复屏障、创建异常查询、生成契约、定向授权事务、请求取消、事件调度和视频复制优化；定向验证与限制见 [当前重构记录](REFACTOR_CURRENT.zh-CN.md)。下表历史平台证据只对应各自提交，不代表本次改动已完成平台验收。

| 需求 | 实现入口 | 已有验证证据（对应各自提交） | 仍须验收 |
| --- | --- | --- | --- |
| 身份、设备签名和新数据库 | identity、model、store、api；Dex/NetBird补丁 | Go回归、Debian13新站点与28项真实TLS/IdP/API | 全设备登录、禁用、恢复矩阵 |
| 时钟偏差与会话期限 | controlpath、protocol、client；Windows transport；ui/auth.ts | Go ±24小时、Windows/前端 ±1小时及修改本机日期回归 | 设备休眠、长时间断网与服务端校时 |
| 账号身份隔离 | site/account.go、client、vault；Windows controller/remote | 独立设备种子/WireGuard profile、真实VPS客户端跨账号连接 | 多系统用户与设备迁移 |
| 控制候选和VPN路由保护 | controlpath/dial_*、上游保护socket、Windows物理接口绑定、macOS网络脚本 | TLS候选/回退、互联网模式下公网控制与中继 | 多WAN、IPv6、TCP-only/NAT完整场景 |
| 旁路由与内核撤权 | session、gateway、privileged、engine/netbird | 192.168.19.254 DNS/Fake-IP HTTPS、显式LAN HTTPS、未授权拒绝和撤权断流 | 完整TCP/UDP、IPv6及非合作客户端矩阵 |
| 服务器本机LAN服务权限 | gateway input、reconcile、server unit | 精确服务器IP资源生成主机策略；AF_NETLINK修复与真实HTTPS | 更多服务/端口和IPv6 |
| LAN/VPS一键维护 | internal/node、netgoctl、netgo-vpsctl；独立安装脚本 | Go菜单、证书/端口/VPS导入、失败恢复回归和实机生命周期 | 新系统/发行包下载和完整故障演练 |
| Windows | apps/windows、系统网络准备器 | NetGo.exe编译、10个定向CTest目标；管道取消、Agent恢复、站点引导、异步文件和编解码 | Win10/11实机崩溃/休眠/显示切换、跨设备音视频；UAC/多屏功能仍待实现 |
| Linux图形客户端 | apps/linux、desktopinstall | GTK/FFmpeg、6 CTest、Xvfb；普通用户IPC、更新失败恢复、卸载/重装 | 物理桌面、Linux主机/X11/Wayland和音频 |
| Android | apps/android、mobile/android.go、共享C ABI | VPN AAR/JNI/Kotlin/APK/lint及独立工作流 | Android实机VPN/远控/IME/恢复 |
| macOS/iOS | apps/apple、mobile/ios.go、vault/darwin.go | SwiftUI/ObjC++/Packet Tunnel；独立App/模拟器/未签名设备构建 | 签名、权限、macOS/iPhone/iPad实机 |
| 本轮一致性与性能 | accounts、store/queries、session、remote、ui/auth、原生客户端 | 容量/排序/刷新竞态、审计分页、连接池、域事务、站点取消、媒体缓冲回归；详见本轮记录 | 移动真机生命周期和跨设备长期媒体测试 |
| 远控会话与媒体 | internal/remote、packages/remote-*、平台适配 | 主机/授权/持钥证明/一次消费/撤权/票据/C ABI/TLS回归；采集和媒体接入 | 完整overlay音视频、输入、多屏、硬件负载、剪贴板/文件实机及RD-A01–A14 |
| 交付、许可和仓库 | 七份脚本/工作流、package-*、THIRD_PARTY_NOTICES | 运行文件白名单、Go维护、秘密/缓存忽略和稳定工具链锁定 | 发行签名、72小时和完整设备证据 |

局部VPN、API和生命周期成功不代表所有远控、设备或A01–A33完成。最新事实见 [进度](PROGRESS.zh-CN.md) 与 [当前重构记录](REFACTOR_CURRENT.zh-CN.md)，历史索引保留原提交的验证结果。

| 本轮新增维护边界 | 源码入口 | 定向证据 |
| --- | --- | --- |
| 原生刷新恢复屏障 | client/control、windows/controller、remote-client/remote_control | 丢响应后立即恢复、持续失败屏障、撤权不重试、取消与共享错误分类 |
| 创建异常与管理契约 | accounts/cleanup_admin、store/account_cleanup_admin、api/admin_dto、ui/AccountCreations | 服务器等待边界、慢请求复用、翻页取消、错误恢复；生成类型与生产构建 |
| 有界查询与事务 | store/admin、remote_policy、remote_dependencies、model/network_policy | 查询取消、相关实体隔离、回滚/撤权与历史保留、展示编辑不触发策略重算 |
| 远控调度与视频 | remote-client/async_exchange、windows/remote/video_pixels、video_codec | 异步文件往返/取消/未启动清理、帧行距/颜色边界、Windows 30 帧合成编解码 |
