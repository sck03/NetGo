# 远程桌面与原生多平台实施方案

版本：2.2。日期：2026-11-23（Asia/Shanghai）。与 EXECUTION_PLAN v2.2 同时生效。

2026-11-24 范围修订：用户已要求同时推进可运行的 macOS/iOS 客户端。SwiftUI、Keychain、原生媒体桥接和 iOS Packet Tunnel 已实现并通过 GitHub 构建；设备签名和实机 VPN/媒体仍需相应环境。五个客户端、LAN 服务端和 VPS 使用七个独立脚本/工作流，见 BUILD_CLIENTS。共享核心、平台网络/媒体适配和节点维护分模块，最终节点维护无需 Python。

v2.2 增加 [统一集成契约](INTEGRATION_CONTRACT.zh-CN.md)：单客户端体验、三套独立状态机、共享身份/传输、主机侧撤权和有界媒体工作者。I01–I10 必须覆盖跨模块组合，不能以每个模块各自通过推断整体融合。若候选媒体引擎无法复用 NetGo 身份/传输或不能移除重复依赖，替换媒体适配，不复制一套账号与信令系统。

## 1. 授权、基线与交付边界

用户在会话 `01a2075c-f461-7531-b0ff-0d0de8d06a52` 最后明确要求：参考远控分析增加远程桌面和各原生客户端；目前未生产部署，可以用新数据库与新接口，不兼容旧开发数据；最后清理并推送 GitHub main。本文件据此覆盖旧方案中“其他客户端以后再做”的限制。

2026-11-24 当前源码为 0.3.0-dev、API v2、SQLite schema 2；服务端主机/授权/票据/会话已实现，Windows 已接媒体与输入/音频，Linux GTK 查看器已编译。旧 0.2.4-dev 报告仅提供历史 VPN 证据。Android/Apple、完整跨设备媒体与各平台验收未完成，不能将源码实现或阶段包写成五平台全部交付。

用户再次确认未正式部署，允许重构及新数据/接口。因此 v1 只作为可运行的开发基线与切换前实验对象；不开发 v1↔v2 桥接、不双写、不增加旧 DTO/接口兼容分支。新版本一次采用新库和契约；完成空库初始化/回退演练并经运行验证后，旧开发程序与数据库可按归属清理。归档用于恢复，不是数据迁移要求。

v2.1 将 RD1 拆成可独立推进的 RD1a（零媒体依赖的 C ABI 会话门禁）和 RD1b（真实媒体原型）。RD1a 先固定绑定、能力、同意、过期、续租、防重放及撤销语义；它不验证真实屏幕、视频、网络或密码学适配。RD2 的纯领域/契约可在 RD1a 后实现，媒体格式与设备网络策略仍须经 RD1b 实测冻结。SDK 缺失只阻断对应适配，不阻止共享领域代码前进。

统一一个 NetGo 产品，保留网络接入，远控按需启用；不划普通版/高级版。控制端与被控端是可分别安装的组件；主机默认不开放远控。外部 RDP/Moonlight 启动入口可用于诊断，但不能替代 NetGo 原生内嵌查看器的完成条件。

## 2. 平台、功能与需求

| 平台 | 原生产品层 | 隧道适配 | 远控职责与验收 |
| --- | --- | --- | --- |
| Windows 10 LTSC 1809 / Windows 11 x64 | C++20 / Win32；现有 UI/agent | 保留独立受限 NetBird 服务 | 首个控制端与被控端；采集、音视频、键鼠、锁屏/UAC、用户切换分别验证 |
| Android | Kotlin / Jetpack Compose，JNI | VpnService、前台服务、授权/重连/进程重建 | 原生控制端；触摸/鼠标、软键盘/中文、音频、后台恢复；先真机，不以 APK 文件存在代替 |
| macOS | Swift / AppKit、SwiftUI，ObjC++ | Network Extension / Packet Tunnel | 原生控制端及被控端；录屏、辅助功能权限、显示器和睡眠恢复 |
| iOS/iPadOS | Swift / SwiftUI，ObjC++ | Network Extension、签名与 entitlement | 原生控制端；后台限制、触摸、键盘与方向变化；不承诺系统级无人值守被控 |
| Linux 桌面 | C++ / GTK 原生控件 | 受限 NetBird daemon、Unix IPC | 原生控制端及被控端；X11 与 Wayland/portal 分别验证；不能把 Go CLI 计为桌面应用 |
| Debian 服务端 | Go + Vue 管理网页 | 本地控制/Relay、路由节点及可选 VPS | 账号、主机、授权、租约、审计与部署；仍可无 VPS 运行 |

Android 最低 API、Apple deployment target、Linux 发行版与显示协议矩阵在 RD1/RD5 按实际 SDK 和依赖可编译版本冻结；不能照抄上游支持列表宣布兼容。Windows 10 当前设备与 Windows 11 验证分开。Android/iOS 控制端同时交付 VPN 接入与桌面查看，两项都必须通过。

| 需求 | 必须实现 | 完成证据 |
| --- | --- | --- |
| R13 | 单产品、网络与远控分权；观看/输入/音频/剪贴板/文件独立授权 | 无权限、降权及已连接撤权的负向测试 |
| R14 | 上表五个平台使用各自原生 UI | 对应工具链编译、包、设备操作录像或截图/结构化记录 |
| R15 | 共享远控核心及稳定跨语言边界 | C ABI 契约、Win32/JNI/ObjC++/GTK 适配与同组协议测试 |
| R16 | 主机注册、设备拓扑、最小网络策略和主机侧强制撤销 | 任意地点两端经 overlay 的实际连接；攻击/过期/重放拒绝 |
| R17 | v2 API/schema 2 一次切换，不维护 v1 兼容 | 新目录初始化、旧版拒绝、归档/恢复演练 |
| R18 | 各平台完整构建与发布，许可/签名材料齐全 | 固定输入、实际构建日志、包摘要、安装卸载及许可证 |
| R19 | E 盘项目内测试与有依据的清理 | 运行/缓存路径清单、清理记录、Git 凭据扫描及 main 提交对应 |

## 3. 媒体内核选择与复用边界

v2.2 先验证操作系统提供的采集、编码、解码与渲染接口，再与 MiniRTC 候选比较总代码量、包体、延迟、稳定性和维护成本；不能同时捆绑两套媒体引擎。CrossDesk 用于理解原生移动桥接和媒体集成，Aspia 用于主机服务及权限设计参考，RustDesk 保留备选。原讨论中的候选顺序没有证明可以直接嵌入。以下是此前实际查询的固定源码，不代表最新版或正式产品依赖：

| 项目 | 固定输入 | 本轮结论 |
| --- | --- | --- |
| CrossDesk | tag `v1.7.6-20261112`，commit `5f526cd70e48818ee98d153c54c08d4ce95f463b` | 可定位 Android NativeSession、iOS ObjC++；根 LICENSE 标识 `GPL-3.0-only` |
| MiniRTC | 上述版本子模块 `9b1acc206b84ad8f31b9466a213ea6777ca8aa18` | API 位于 `src/api/minirtc.h`；须先编译与测试，不将源码存在视为可用 |

本次读取的固定版本已提供 `MiniRtcDirectOptions`：调用者提供已认证的信令、local/remote address、send_signal 回调，直连模式不登录云会话或请求 STUN/TURN。可作为 NetGo overlay 适配入口，但尚未编译/运行。其头文件虽然采用 extern C，仍含 C++ 类型，不能直接当作我们的稳定纯 C ABI；必须增加独立包装层。MiniRTC 固定 LICENSE 为 LGPLv3，与 CrossDesk GPLv3 分开登记。

Windows 构建指南要求 MSVC/Windows SDK 和 clang-cl，现有 NetGo 主要使用 MinGW/UCRT；当前 PATH 未发现 xmake、cl、clang-cl、Android adb/gradle 或 Swift，仅发现 JDK 17。RD1 先核实已安装但未入 PATH 的工具，再把缺少工具链放项目盘；不能静默宣称现有 GCC 就能构建 MiniRTC。Apple 构建需要可用 macOS/Xcode 执行环境和签名条件，当前 Windows 主机不能完成该验收。

核查输入来自官方 GitHub API、固定提交源码及 LICENSE。RD1 记录树/子模块、下载摘要、依赖与补丁。GPL/LGPL 组合、静态/动态链接方式及完整对应源码交付需形成 THIRD_PARTY_NOTICES/SBOM；未完成前不得把候选二进制放进正式安装包。

RD1 必须完成：Win32 窗口显示实时目标桌面；Kotlin 最小控制端使用同一核心；音频与键鼠确实传输；媒体只走 NetGo overlay；撤权后主机终止媒体和输入。成功才冻结依赖。若候选必须依赖它自己的公网账号、Router/Relay 或无法接入既有隧道，保存最小复现与 API 位置，评估有界适配或备选，不静默引入第二套公网控制系统。

Sunshine/Moonlight 可保留为低延迟媒体实现参考；主线是桌面管理，不能默认其具备独立剪贴板、文件权限、UAC 与无人值守语义。不得自行设计密码算法或削弱既有 TLS/WireGuard 验证。

## 4. 网络、身份与新接口

### 4.1 网络拓扑

现有系统是客户端到 Linux 路由节点再访问 LAN，并不自动支持任意两个客户端互通。新增 `host` 角色与明确的目标 peer 策略；控制端只可连获准主机的指定媒体/控制端口，其他 peer、LAN 资源与互联网出口仍拒绝。被控主机常驻身份与人的 VPN 登录配额分开，但必须绑定站点、注册设备和公钥。

控制服务签发短期远控会话授权；传输由 NetBird overlay 承载，依次尝试可用直连/既有中继。媒体库的公共 STUN/TURN、第三方账号和自动公网监听默认禁用。验证时记录两端 overlay 地址、真实 peer 路径与流量计数；单看“直连”文字不够。现有 FRP 仍只承载控制，视频不通过 FRP。

RD0 已定位并修复现有 VPS 失败机制：原 `WorkerRelay.preferredRelayServer` 按公钥排序产生 controller，再选择该侧 home Relay；网关 home 为局域网地址而外部客户端只能到 VPS 时会选到不可达地址。当前补丁对 NetGo 客户端/专用路由节点明确角色，让双方选择客户端可达且 Management 发布的 home，保留两种 Relay；两种公钥顺序竞态回归通过，网关为 controller 的公网 18 项实测通过。该客户端→网关修复不能用于任意主机 peer-to-peer；RD2 必须更换为双方可达候选协商，不能把所有新远控 hosts 都设置成 routing peer。

对于只安装在 LAN 内且不运行 overlay 的旧设备，可继续通过原网络资源使用其现有协议；这类访问不伪装成 NetGo 主机能力。需要内置远控的目标必须安装主机组件。

### 4.2 v2 服务契约

当前实现的准确端点为 `/netgo/v2/remote`（主机/授权/会话列表）、`/remote/key`、`PUT /remote/host`、`POST /remote/host/heartbeat`、`/remote/sessions` 和会话的 `consume`/`renew`/删除；管理授权为 `/admin/remote/grants`。下表记录领域要求，消费同时记录主机本地同意后的接受行为；不额外提供一套尚未实现的 enroll/decision 路由。客户端使用相同已认证设备身份和 VPN 租约登记主机。

沿用签名、防重放、站点身份与唯一 IdP 的原则，统一路径 `/netgo/v2`、`X-NetGo-Protocol: 2`。公共 health 提供真实版本及能力；受保护的 v1/缺失/错误版本明确拒绝。客户端先协商版本/时钟，再发送密码。不维护两套密码库。

| 操作 | 端点 | 权限与事务要求 |
| --- | --- | --- |
| 主机登记 | `PUT /remote/host` | 当前用户/设备签名与 VPN 租约；绑定本设备公钥/启动实例；管理员另行授予访问权 |
| 状态/能力 | `POST /remote/host/heartbeat` | 主机密钥签名、当前启动实例；只发布已实现能力，地址取自已验证的 NetBird peer |
| 主机列表 | `GET /remote` | 受权主机/授权/会话；在线状态独立于具备访问权 |
| 创建远控 | `POST /remote/sessions` | 用户/设备/VPN 租约/主机授权同时有效；请求能力取交集；并发限制、generation、幂等键 |
| 本机同意 | 本地窗口与 `ng_remote_gate_consent` | 默认拒绝，超时拒绝；服务端管理员身份不能代替本地操作 |
| 连接兑换 | `POST /remote/sessions/{id}/consume` | 主机同意后提交持钥证明；一次性、短时、绑定控制端/实例/权限；失败不可启动媒体 |
| 续租 | `POST /remote/sessions/{id}/renew` | 重验账号、授权、双方设备、VPN 租约与策略代次；不得扩大原权限 |
| 结束/撤销 | `DELETE /remote/sessions/{id}` | 本人、目标主机或管理员；主机关闭输入/媒体、释放按键，离线也受本地期限约束 |
| 管理授权 | `PUT /admin/remote/grants`、`DELETE /admin/remote/grants/{id}` | 最小能力列表；变更撤销受影响会话；有审计 |

以上端点省略 `/netgo/v2`。当前 HTTP 错误通过非 2xx 与 JSON `error` 返回，包括 `remote_host_unavailable`、`remote_ticket_replayed`、`remote_proof_invalid` 及统一身份/协议的 `unauthorized`、`forbidden`、`conflict` 等。客户端门禁另有明确的同意、过期、重放、时钟和能力结果；不能根据 HTTP 200 自由文本猜测成功。

### 4.3 主机侧门禁

固定二进制授权格式见 [REMOTE_TICKET](REMOTE_TICKET.zh-CN.md)：168 字节、Ed25519、六项独立能力、最长 30 秒租约。`internal/remote/ticket` 用 Go 标准库签发，`packages/remote-ticket` 验签并衔接门禁；核心不依赖 OpenSSL。主机登记、TLS exporter 持钥证明、数据库原子消费/重启防重放、v2 API 与新存储已实现并有正反/并发测试。各主机只发布其实际适配的能力；Windows 当前不开放剪贴板/文件能力。

服务端授权包含 site/user/controller-device/host-device/host-instance 的绑定摘要、随机 session ID、generation、policy revision、sequence、权限集合与过期。128 位 session ID 同时承担初次授权 nonce，续租由 sequence 区分；不得重复使用。客户端证明拥有指定设备私钥；主机校验签名、目标绑定、一次消费及期限。端到端加密也覆盖网关后的 LAN 一段，不能仅因穿过 WireGuard 就使用明文桌面连接。

连接票据目标有效期 30 秒；活动会话每 10 秒续租、主机本地租约上限 30 秒，使用单调计时防止系统时间回拨延长权限。这些是实现目标，须在慢网络及断网测试后冻结。撤权在线目标 5 秒内停止；冻结服务端/断开控制通道后最迟本地租约到期停止。VPN 旧门禁 120 秒与远控主机 30 秒是不同机制，分别记录，不靠调大时限掩盖问题。

权限默认最小：观看不含输入；音频、文本剪贴板、上传、下载独立。主机在每个敏感事件处分权，客户端隐藏按钮不是门禁。输入取消/失焦/断线释放已按键，防迟到事件。剪贴板限尺寸、关闭即清空待发队列；文件限用户同意目录、拒绝路径穿越/符号链接、临时写入后校验、禁止任意 shell。审计只记主体、动作、对象、结果与字节数，不记屏幕/剪贴板/文件正文或票据。

## 5. 新数据库与切换

schema 2 使用新的 NetGo SQLite 数据目录，规范化实体至少包括 users、devices、hosts、remote_grants、vpn_leases、remote_sessions、ticket_nonces、operation_keys、audit_events。声明外键、唯一键和状态约束，WAL/事务下串行化票据消费和并发控制。仍只保存 IdP/NetBird 对象引用，不跨库修改上游数据。

允许不兼容不等于允许无条件删除。实施顺序：停止本项目写入 → 归档 NetGo/上游身份/证书与所属规则清单 → 验证归档可读 → 在新目录初始化 → 成套部署 v2 → 空状态首次登录/注册/远控 → 根据摘要核验恢复旧开发栈。执行工具需显式 `--fresh-state` 与归档路径；遇到非空新目录、归档失败或外部拥有对象则停止。普通 install/upgrade 不隐式重置数据库。

新旧栈不混合写入，不同时开放共享端口；旧客户端只收到明确升级提示。回退使用完整旧程序、旧数据库及旧上游状态，不能拿 schema 2 文件交给旧程序。实验环境初始化可按现有授权执行，前提是脚本已满足上述归属和恢复条件。

## 6. 代码边界与线程约束

2026-11-23 增补约束：模块化、低依赖、体积和清理均为交付要求。基础 VPN 客户端不隐式携带远控媒体栈；远控经独立构建开关/包选择启用。Windows 使用系统 Win32/CNG/CryptoAPI；只有连接后台链接必需的 TLS/Ed25519 库。禁止为设置界面引入浏览器、Qt/Electron、Python 或重复加密库。新增媒体依赖必须列明用途、许可、锁定版本、传递依赖、各架构增量体积与移除方式；有系统采集/编解码 API 时优先使用。不得通过关闭证书验证、弱化授权或删除必要恢复路径换取体积。

用户于 2026-11-24 明确要求 Windows 单 EXE 普通使用入口。现将主界面、后台和远控链接为一个 NetGo.exe，以内部模式运行；不再交付三个需要辨认的产品程序。代码仍保留 `netgo-platform`、`netgo-connection`、共享会话/查看器、系统采集/编解码/音频等窄静态库，`common.hpp` 不暴露 OpenSSL 类型。统一文件采用 LTO/符号剥离，预算 10 MiB，当前约 6.70 MiB，仅导入系统 DLL。该产品入口要求替代旧独立 UI 2 MiB / 后台 8 MiB 的文件预算；受限 NetBird 网络服务和 Wintun 仍单独核算。

未来模块通过窄接口注入时钟、授权、信令、传输与平台媒体适配，业务层不能直接引用窗口句柄或平台 SDK。每一阶段同时记录可执行文件/压缩包/依赖字节、空闲与连接内存、CPU、帧耗时和重连延迟；未测性能不写为改善。资源使用有上限，取消/销毁必须释放线程、句柄、帧队列与按键状态。

清理先保存当前必要报告及摘要，再删除可重建缓存、重复源码 checkout、旧发行包和无效截图。旧数据库只删除已停用、属于本实验且已有有效恢复归档的副本；运行中的身份/配置不是旧测试数据。main 采用普通提交/推送，先检查其他分支独有提交，合并有效改动后才删除已合并分支，不强推改写历史。

| 目录（计划） | 负责内容 | 不应承担 |
| --- | --- | --- |
| `internal/remote` | 主机/授权/租约/票据/审计 | 屏幕采集或媒体转码 |
| `packages/remote-core` | C++ 会话门禁、媒体适配及版本化 C ABI | 平台窗口、可复用明文密码、公网协调系统 |
| `apps/windows` | Win32 UI、渲染、音频、主机服务与桌面会话代理 | 用高权限 UI 代替服务隔离 |
| `apps/android` | Kotlin UI、VpnService、Keystore、JNI/Surface | 把整个桌面业务放进 Activity 生命周期 |
| `apps/apple` | Swift UI、Keychain、Network Extension、ObjC++ | 假设签名/录屏/辅助功能授权已存在 |
| `apps/linux` | GTK UI、用户会话媒体、X11/portal、凭据存储 | 常驻 root 桌面进程 |
| `tests/contracts/remote` | 跨语言共享的正反协议用例 | 用固定模拟帧代替远控实测 |

C ABI 使用 opaque handle、定宽整数、结构体 size/version 与明确的所有权释放函数；不把 STL、异常、跨模块 free 或不稳定 C++ ABI 暴露给 Kotlin/Swift。显式描述同步/异步取消和 callback 所在线程；UI 回调派发到平台主线程，销毁之后不允许迟到 callback。媒体线程有界队列、背压和帧丢弃，不能无限缓存造成延迟或内存增长。

平台网络接口保留连接/断开/状态/候选/凭据/路由等现有语义；Android 与 Apple 各自的 VPN 适配单独验收。手机控制 Windows 成功不能替代 macOS/iOS 构建或 Linux Wayland 被控成功。

## 7. 连续执行顺序与退出条件

| 阶段 | 前置 | 文件/工作 | 可验证退出条件 | 当前状态 |
| --- | --- | --- | --- | --- |
| RD0 | 已有工作区及历史记录 | 同步主方案/任务表/矩阵/进度；修复锁定构建；重新跑旧验证和失败的 VPS 场景 | 当前源码对应完整 Windows/Linux 验证；失败/未测不隐藏；原 SSH/Docker/网络保留 | 进行中 |
| RD1a | 可构建基线 | 独立门禁、C ABI、双时钟、能力缩减、同意与撤销 | 跨平台门禁与 C 调用；验证器明确为注入契约 | 核心、真实签名、持久一次消费已实现；Windows/GTK 查看器复用 C ABI |
| RD1b | RD1a；对应 SDK | 固定候选/许可证；系统采集、真实编码/传输/解码、Win32/JNI | 两控制端实际看/控/音频；直连与中继；撤权停止；选型 ADR | Windows DXGI/H.264 本地编解码及真实 TLS/持钥证明通过；音频已接入，跨设备媒体与 JNI 未验 |
| RD2 | RD1a 后领域/契约可先行；媒体网络待 RD1b 冻结 | schema 2、v2 API、主机注册/ACL/票据/租约、peer 拓扑；管理 UI | 空库初始化，重放/跨设备/越权/过期/并发拒绝；非合作主机门禁；旧版拒绝/恢复 | 源码及正反/并发测试已实现；完整实机切换/恢复待验 |
| RD3 | RD2 | Windows 原生查看器与主机服务、会话代理、安装卸载 | Win10/11 桌面、锁屏/UAC、多屏/休眠；剪贴板文件边界；输入/媒体撤销 | 单 NetGo.exe、图形安装与绿色版逻辑、交互主机/查看器已接入；完整平台退出条件未满足 |
| RD4 | RD1/RD2，Android 工具链和设备 | Kotlin App、VpnService、JNI、凭据、触摸/IME/音频 | APK 构建与安装；真实 VPN/远控、后台/杀进程/换网/中文输入与取消 | 未实现 |
| RD5 | RD2，Apple SDK/签名/设备，Linux 桌面 | Swift/NE/ObjC++；GTK/X11/Wayland；平台打包 | macOS/iOS 签名安装与 VPN/远控，Linux 两显示协议；权限拒绝及重授予 | GTK 查看器已实现/编译，物理桌面、Linux 主机和 Apple 适配/签名待完成 |
| RD6 | RD0–RD5 满足各自退出条件 | 统一测试、性能、72h、许可、签名、开发/正式包、清理与 main | 对应源码/日志/产物指纹；逐平台验收表；下载可用；仓库无凭据；本地/远端提交一致 | 未完成 |

持续记录到 PROGRESS；按 RD 依赖继续推进，不因阶段切换重复请示。RD1 未能编译时先缩到最小依赖复现；不把模板/占位方法标成客户端完成。签名/设备确实不可用时列具体缺项，继续能完成的代码与构建，不发布“全部平台完成”。

已有可执行检查（工作目录均为仓库根）：

```powershell
. ./scripts/project-environment.ps1
./scripts/verify.ps1 -Output .cache/verification-新的唯一目录
```

```sh
python3 -B scripts/verify-linux.py --output .cache/verification-新的唯一目录
```

两者现在包括 VPN 基线与 `remote-core` 独立阶段；Linux 还运行门禁 ASan/UBSan，Windows 编译采集适配并运行不采图的边界检查。实际桌面采集显式使用 `apps/windows/remote/README.md` 中的 `--run`，不宣称媒体或远控端到端验收。未创建 build-android、xcode 项目前，不给出伪造命令。现有真实实验入口在 `tests/network`；密码/私钥文件仅置于受保护实验目录，不提交。

## 8. 新增验收（与 A01–A33 并列，不能互相替代）

| 用例 | 场景 | 最少证据 |
| --- | --- | --- |
| RD-A01 | Windows→Windows、Android→Windows 实际桌面 | 双方包/commit、画面、输入、音频、overlay 路径与字节计数 |
| RD-A02 | 公网打洞直连、LAN 直连、UDP 阻断后的 Relay | 实际媒体、路径/规则计数、画质及 RTT；每路径重连至少 20 次 |
| RD-A03 | 观看不能注入输入；剪贴板/文件单独拒绝 | 绕过 UI 发送事件也被主机拒绝，无文件副作用 |
| RD-A04 | 账号禁用、设备删除、会话替换、权限变更 | 已建立媒体/输入停止时间、审计及新旧 generation |
| RD-A05 | 冻结服务端或断开控制；客户端不合作 | 主机本地租约到期停止，时钟回拨不能延长 |
| RD-A06 | 票据复制、重放、错误公钥、错误目标/实例 | 不创建媒体通道，原子一次消费，多线程竞态 |
| RD-A07 | 锁屏/UAC/登录前、多屏、热插拔、休眠 | 每 Windows/macOS/Linux 场景分别记录，无法支持则明确阻断 |
| RD-A08 | 中文 IME、触摸/鼠标、软键盘、横竖屏 | 实际文字/输入坐标与释放按键，延迟帧不重放输入 |
| RD-A09 | 网络/媒体取消、进程退出、JNI/ObjC++ 销毁 | 无迟到连接、线程/资源泄漏、遗留路由或按键 |
| RD-A10 | 文件逃逸/链接/超限、剪贴板超限、撤权在途传输 | 拒绝与临时文件清理；不读写授权根外路径 |
| RD-A11 | 五平台 VPN + 原生远控 | SDK、设备/OS、签名、构建日志、安装/卸载和凭据隔离 |
| RD-A12 | v2 空库/旧版拒绝/完整回退 | 归档摘要、状态重建、旧数据库未被覆盖、SSH/Docker 保留 |
| RD-A13 | 低带宽/高 RTT/丢包与 TCP Relay | 720p30、1080p30 的带宽/帧率/CPU/内存/端到端延迟实测；设置降码率策略 |
| RD-A14 | 连续 72h 与并发控制冲突 | 定时采样、断连/恢复、资源增长与崩溃报告；不能缩短后称 72h |

## 9. 环境与安全清理

已知可用实验：Windows 10 LTSC 1809 x64；LAN Debian 13.8；公网 VPS Debian 13.7。VPS 上 80/443 是既有容器，继续使用实验高位端口。Windows 11、Android 真机、Apple SDK/签名/设备、Linux 图形会话是否可用，分别在开始相应任务时核实。

所有下载、Gradle/Go/npm/CMake 缓存、测试数据与运行副本放在项目盘；运行副本放 `.cache/windows-project`，与 `dist` 分开。新程序变更时有界停止本项目进程、更新副本再启动；不得杀未知进程或通过覆盖锁定文件掩盖部署状态。

清理只针对列出且确认属于本项目的无效构建、临时测试账号/规则/会话；先保留有效证据、必要状态与源码。测试失败记录不是无效文件。递归删除前验证绝对路径和归属；不清空整个 .cache、不删除既有用户配置、Docker、SSH 或无关防火墙规则。提交前检查暂存区的密码、令牌、私钥、数据库与绝对实验凭据路径。

最终报告分开写：方案已修正、源码已实现、各平台已构建、设备已实测、已发布/推送。任何一个维度未完成都不能合并成“全部交付”。
