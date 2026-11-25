# 远控交互、LAN 诊断与慢控制链路修复

> 历史提交记录；原始日志、截图与测试数据已从当前目录清理。可在 Git 提交 `9296955` 的相同路径查看，不能作为本轮修改的验证结果。

日期：2026-11-25（Asia/Shanghai）。接续“优化远程桌面与旁路由局域网访问”；基线 fd4535a。未增加旧数据/旧接口兼容层，未部署正式版本或发布 Release。

## 已实现

- Windows 远控列表保留已授权但忙碌、失联、VPN 离线或关闭共享的主机，连接按钮由状态控制。连接前可选择仅观看、是否收听电脑声音；增加 F11 / 工具栏全屏、窗口恢复和键鼠释放；统一工具栏布局、留白、空状态和键盘导航。
- 共享查看器将有效能力限定为本次选择与服务端库存的交集，授权响应必须与请求能力一致，拒绝权限被放大后再进入媒体连接。
- 管理网页改为主机卡片、状态筛选、搜索、连接步骤提示、快捷权限预设、到期信息及会话停止。网络资源按局域网设备/服务与旁路由互联网出口分别展示，说明授权和客户端启用的关系。
- 新增 packages/ui/query.ts，在保存/退出/卸载时作废旧响应，立即允许新查询；旧响应不能覆盖新数据或清除新查询状态。账号和资源保存后不再被正在进行的刷新跳过。
- 新增 internal/node/lan_check.go：无论旁路由是否开启，都检查 LAN 网卡状态、IPv4、每个配置网段的路由抽查以及 IPv4 转发。旁路由检查继续核对规则归属，DNS 检测显示耗时。全部是只读诊断。

## 稳定性缺陷与回归

VPS 控制入口三组串行请求实测耗时 5.515 / 8.301 / 8.679 秒（每组健康、签名状态、远控库存三次 HTTPS），促使补充慢链路回归。这不等同于单请求延迟，也不证明此前 DNS/TLS 初次失败的原因。

共享查看器原来在状态查询和续租完成后额外休眠 3 秒。当两次请求各耗时 1.8 秒时，后续轮次间隔可达到 6.6 秒，超过现有 6 秒失联门限。跨两轮续租的回归先失败，修复后 Windows / Debian 均通过。轮询现在把请求耗时计入 3 秒周期；Windows 主机监控同步修正。没有延长票据或六秒失联门限。

补充七秒阻塞用例验证仍会结束媒体，且不再发起额外续租。首次测试错误要求所有结束路径返回同一个返回码；实际传输取消可报告失败，修正为检验仅一帧、无续租、在期限内退出。失败日志与最终日志同时保留。

## 验证

| 范围 | 实际结果 |
| --- | --- |
| 前端 | npm test 18 项通过；vue-tsc / Vite build 通过 |
| Windows 原生 | 12 项 CTest 通过；共享查看器包含 18 个场景：仅观看、权限差异拒绝、慢请求持续续租、失联关闭等 |
| Windows Go | node/site/remote/api/client/gateway/session race 通过；go vet ./... 通过 |
| Debian 13 | 全 internal/cmd Go race 与 vet 通过；GTK 构建及 7 项 CTest 通过 |
| Linux 内核 | 隔离 netns 的真实 nftables 租约续期/撤权、旁路由规则归属/恢复通过 |
| 新诊断故障注入 | 未启用旁路由时正常、转发关闭、错误路由、网卡断开共 4 项实机通过 |
| LAN 实际只读检查 | ens32 / 192.168.18.0/23 / IPv4 转发 / 受管旁路由规则通过；DNS 返回 Fake-IP，样本耗时 3 ms |
| LAN / VPS TLS 控制入口 | 每入口 3 组健康、签名管理状态、远控库存查询，共 6 组通过；原始站点域名及 CA 校验 |

原始日志见 TEST-LOGS.txt；故障注入与控制入口逐次记录见 netns-results.json、control-results.json；435 个代码/构建/测试输入指纹见 SOURCE.json。Linux 临时源码首次归档后，远控共享源文件及测试有增量同步，最终原生结果对应增量后的源码。

临时测试脚本首次错误调用 netgoctl check-network 报 unknown command，改用 netgoctl server check-network 后通过；不是产品入口故障。首次清理脚本被误交给 bash 执行，只报 Python 语法错误，改用 python3 执行后清理成功，未改变产品代码。

## 设计参考

本轮检索工具未返回可用正文，通过官方站点 HTTP 读取核对：AnyDesk Display settings 的会话视图/全屏操作；Tailscale Subnet routers、Exit nodes 对指定网络与互联网流量的划分。仅借鉴交互，不引入第三方代码和依赖。

- https://support.anydesk.com/docs/display
- https://tailscale.com/docs/features/subnet-routers
- https://tailscale.com/kb/1103/exit-nodes

## 清理与边界

LAN 临时源码、程序、构建目录和归档共清理 57,933,685 字节。临时管理员设备已撤销；没有创建测试资源/普通账号，也没有修改在运行的服务器程序或配置。六项 LAN 服务、两项 VPS 服务及 SSH 正常，默认网关不变，已安装程序与安装清单摘要一致。网络故障注入位于无名 netns，退出即销毁。

前轮约 346.57 MiB 本地缓存的路径经核验后删除仍被自动审批拒绝，原因为 blocked by policy；保留 .cache/remote-lan-20261125，未改用其他方式绕过限制。本轮独立的 8 个已结束预览/测试脚本与源码归档先清理 807,633 字节；证据归档后再清理其余 24 个文件 31,537,714 字节，.cache/access-20261125 已移除，预览服务及 5194 端口已释放；结果见 CLEANUP.json。

浏览器插件返回缺少 network policy；Windows Computer Use 因无法可靠确定当前浏览器 URL 停止本轮 UI 操作。因此新布局/工具栏实际点击、响应式视觉效果尚未验收。完整两设备画面、实际键鼠、音频、UAC/锁屏、多屏、物理 ARM 和 72 小时稳定性未由本轮测试证明。前轮首次 DNS 超时 / TLS EOF 仍是开放问题。本轮没有重新进行完整 VPN 业务流测试。

## 云构建与提交

代码提交 **483a951a6b7967b5a538a24fab64b168f271d5f0** 已推送 main。以下九项构建全部成功，发布任务均跳过；详细步骤和产物摘要见 CI.json。

| 目标 | GitHub Actions Run | 结果 |
| --- | --- | --- |
| LAN amd64 | 45611468697 | success |
| LAN arm64 | 45611508706 | success |
| VPS amd64 | 45611468606 | success |
| VPS arm64 | 45611516591 | success |
| Windows amd64 | 45611468497 | success |
| Linux desktop amd64 | 45611468461 | success |
| Android arm64 | 45611468499 | success，第 2 次运行 |
| macOS arm64 | 45611468731 | success |
| iOS arm64（device + simulator） | 45611468554 | success |

Android 第一次运行在解析 org.gradle.toolchains.foojay-resolver-convention 1.0.0 时失败，尚未进入 Android 工程编译；官方插件页及固定版本 POM 仍能读取。同一提交仅重跑失败任务后成功，没有为此修改依赖、校验或项目代码。保留 ANDROID-FIRST-ATTEMPT.json 与 ANDROID-FAILURE.txt。

五个运行包（Windows、LAN amd64/arm64、VPS amd64/arm64）已独立校验 GitHub 外层摘要、清单文件及旁置校验文件摘要、源码提交、内层归档路径/CRC 和 ELF/PE 架构，结果见 ARTIFACTS.json。下载曾发生一次 GitHub 读取 TLS EOF，后续读取成功；Windows 校验文件使用 CRLF，内存校验脚本首次只接受 LF 导致误报，确认并按完整相同内容的 LF/CRLF 两种形式校验后通过。这两次失败均来自交付验证过程，未更改产品代码或放宽摘要要求。

本地 Windows 构建的 NetGo.exe 为 7,124,992 字节，其摘要和测试维护程序摘要见 LOCAL-ARTIFACTS.json。构建通过不替代设备运行或图形交互验收。
