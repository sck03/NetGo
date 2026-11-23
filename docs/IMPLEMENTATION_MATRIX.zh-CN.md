# 执行方案与源码追踪

修订：2026-11-23（Asia/Shanghai）。对应执行方案 v2.2；当前代码 0.2.4-dev，远控/原生多平台目标 0.3.0-dev。当前 LAN/VPS 为 Debian 13，Windows 为 10 LTSC 1809；其他平台需新增实现与独立验收。跨模块融合必须逐项通过 [I01–I10](INTEGRATION_CONTRACT.zh-CN.md)，当前未完成。

## 新增范围追踪

| 条款 | 实现落点（计划，不代表文件已有） | 自动/实机门槛 | 状态 |
| --- | --- | --- | --- |
| R13/R16 远控授权与撤销 | packages/remote-core/src/session.cpp；主机适配/管理 UI 待实现 | 门禁 79 项与竞态/C ABI；完整 RD-A03–A06、A09/A10 仍需主机集成 | 独立门禁已实现；持久防重放/媒体未集成 |
| R13/R16 认证票据 | internal/remote/ticket；packages/remote-ticket；tests/contracts/remote/tickets.txt | Go race/vet/fuzz、C++ 423 项、C11；Windows/Debian 一致，Debian ASan/UBSan | 格式/签发/验签/绑定及门禁衔接已实现；主机登记/持钥证明/一次消费未实现 |
| R14/R15 共享核心与原生 UI | packages/remote-core/include/netgo_remote.h；各平台适配待实现 | Windows/Debian C/C++ 与 ASan/UBSan 通过；RD1b/平台设备另验 | 核心基础已实现；原生远控 UI 尚未实现 |
| R15/RD1b Windows 系统采集 | apps/windows/remote | DXGI/D3D11 实际 GPU 帧、CPU 探针回读与三次关闭重开；只输出元数据 | 当前 1920×1080 桌面通过；编码/网络/主机授权、锁屏/UAC/多屏仍待实现或测试 |
| R17 新接口/新库 | model/store/api/client/协议契约与安装器 | v2 空库/拒绝旧版/归档恢复，RD-A12 | 未实现 |
| R18/R19 交付与项目目录测试 | 构建/签名/安装、scripts/publish-artifact.ps1、tests/tools/windows_publish.ps1 | 锁定文件保留回归；RD-A13/A14、各平台包摘要和设备记录 | 构建锁问题已修复；全目标未完成 |

## 既有 VPN 基线追踪

本表说明代码所在位置和验证边界，供审阅定位。“自动验证”指单元、替身、回环、配置解析或构建，不代表真实网络用例通过。当前运行记录见 [本轮报告](evidence/2026-11-23-delivery/REPORT.zh-CN.md)，实时进度见 [PROGRESS](PROGRESS.zh-CN.md)。下表的 A 编号保留完整场景边界，已执行的具体子项以本轮报告为准。

| 方案与需求 | 实现入口 | 自动验证入口 | 仍须在目标环境验收 |
| --- | --- | --- | --- |
| §2/§3：Debian 12/13 与固定依赖 | upstream/versions.lock.json；scripts/linux_preflight.py；scripts/build*.sh/ps1 | tests/tools/test_linux_preflight.py；verify.ps1 | 当前 Debian 12 版本与 CGO 已记录；其他 OS/架构单独执行 A28 |
| §4：本地完整控制与可选 VPS，R01/R02/R04/R06 | deploy/templates/local/、auxiliary/；internal/site；internal/engine/netbird | test_deploy.py；verify-deploy.py | 无 VPS 冷启动、添加后全停、反向通道，A01–A06 |
| §5：逻辑身份、动态入口、配置版本，R03/R04/R12 | internal/controlpath；internal/site/profile.go；internal/client/profile.go；apps/windows/src/transport.cpp；上游控制补丁 | transport_test.go；profile_test.go；18 项 tests/contracts/profiles.json；原生 TLS | 实际 DNS、IPv4/IPv6、多 WAN/NAT、Signal/gRPC 故障切换，A04/A05/A07/A08/A32/A33 |
| §6.1：真实用户原生认证，R07 | internal/identity；upstream/patches/dex.patch、netbird.patch | native_test.go；revocation_test.go；上游 TestNetGo | 真正 Windows→Debian IdP/Management 登录、令牌刷新与禁用，A14/A15 |
| §6.3：并发租约与非合作客户端撤销，R09 | internal/session；internal/gateway；internal/privileged | session/revocation/gateway 竞态与失败回归 | nftables/conntrack/源地址绑定、已有 TCP/UDP 阻断，A21–A24 |
| §6.4：密码维护、凭据保护，R07/R09 | internal/operations/password.go；netgoctl reset-password；internal/vault；Windows Credential Manager | password_test.go；native core/IPC；身份回归 | Debian 原 IdP 命令与服务失败恢复、Windows 多用户，A14/A15/A25/A29 |
| §7：资源、最小权限、SNAT、冲突，R08/R09/R10 | internal/engine/netbird/reconcile.go；internal/gateway；client.CheckConflicts；routeConflicts | gateway/site/api/session 测试；上游适配构建 | 真实 LAN TCP/UDP、INPUT/FORWARD、重叠网段、互联网出口，A16–A20 |
| §5.3/§8：DDNS、Relay、443、证书，R03/R04/R05 | internal/ddns；deploy_config.py；TLS/Relay/frp 模板；deploy-certificate.sh | DDNS/multiwan 测试；六种组合解析；证书错误输入回归 | 运营商端口、H3C Comware/NAT 回程、DNS-01、WS/QUIC/TCP-only，A04/A09–A13/A30/A32/A33 |
| §9：API 版本、签名、防重放与能力 | internal/protocol；api/server.go；packages/ui/auth.ts；Windows validateHealth | protocol_test.go；api 角色/签名/防重放；前端认证测试；native-core | 混合版本升级及真实 IdP 链路，A14/A15/A29 |
| §10：管理网页和 Windows 产品流程，R07/R12 | apps/ui/src/App.vue；apps/windows；internal/client；cmd/netgo-client-service | Vue 构建/认证；CTest/IPC/TLS；Go 客户端配置同步 | 实际点击、跨用户、后台/睡眠/换网/取消与残留，A25–A27 |
| §10.3：受管安装/卸载，R11 | deploy/linux/install.sh、uninstall.sh、netgo-caddy/netgo-haproxy 单元；deploy/windows | verify-scripts.ps1；Debian 预检查故障用例；Windows 服务路径/SID 检查 | Debian systemd、防火墙保留/恢复；真实 Windows 安装升级，A28 |
| §13.1：诊断、日志、告警，R12 | internal/diagnostics；api/observability.go；admin/diagnostics；管理页 | monitor_test.go；observability_test.go；Vue 构建 | 真实服务掉线/证书重载/磁盘告警与目标设备定位，A12/A13/A30/A31 |
| §13.2：一致加密备份，R11 | internal/backup；netgoctl backup/restore | 路径逃逸、篡改、根目录映射回归 | 干净 Debian 恢复旧站点、账号、设备和密钥，A29 |
| §13.3：配置/产品升级与回退，R11 | internal/operations/config.go、release.go；CLI | 故障注入、共用锁、版本清单、候选回退使用更高版本 | systemd、版本目录链接、IdP 一致恢复，A29 |
| §12/§13.4：完整交付证据 | tests/network；scripts/verify.ps1、verify-linux.py、verification.py、prepare-upstream.py、package.py | 证据/上游输入/归档回归；完整验证；源码及已验证构建与 ZIP 字节关联、逐层 SHA256SUMS | Debian 原生完整检查；A01–A33、十会话负载、20 次各类恢复、72 小时稳定性 |

v1.6 专项定位：`internal/engine/netbird/daemon.go`/`daemon_test.go` 修复 Linux 重复 profile 与异步路由；上游 `client/grpc/netgo.go`/`netgo_dial_test.go`（随 netbird.patch）覆盖卡住的 TLS 候选、优先级、取消和证书身份；Relay/frps 单元增加低位端口所需的唯一 capability。具体修复前后结果见本轮报告。

## 当前明确边界

- 站点候选在成功认证后同步，活跃连接使用配置快照。DDNS 和已知候选的动态恢复持续运行；凭据/CA/issuer 变化不走候选自动更新。
- 原生认证、管理请求、Linux 网关租约共同保护 LAN。密码维护撤销产品凭据，并调用唯一 IdP；已签 OIDC JWT 的有效期仍由上游决定。
- 告警在管理网页/JSON 诊断中提供。服务活动、TCP 监听、证书文件和可信控制健康均有各自含义，不合并成“公网/LAN 已通过”。外部通知由运维监控接入。
- 产品升级命令目前支持 schema 1 产品程序。新增 systemd 单元、上游二进制、数据库不可逆迁移仍需要完整维护部署及 A29 演练，不能仅替换产品程序就宣称升级完成。
- Windows 上的完整自动检查包括 Linux 产品交叉编译。完整 Linux 服务端由 Debian 12/13 原生 CGO 构建，经独立 Linux 验证后生成；仅含产品程序的开发 ZIP 单独命名，不能当作完整服务端安装包。
- Android/Apple/Linux 原生端与远控已纳入当前 RD 任务；OpenWrt、互联网出口、多站点并发、IPv6 LAN 等仍是后续扩展。不得沿用旧范围关闭新增要求。

本轮已有真实 API、隔离隧道、租约强制撤销、本地 Relay/TCP、运维和十会话证据；它们不能替代 G1–G6 / A01–A33 的所有外部网络、跨系统和 72 小时条件。项目仍为开发交付，未标记生产发行完成。
