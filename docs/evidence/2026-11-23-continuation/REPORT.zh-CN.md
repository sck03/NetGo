# v2.2 接续：原生模块、真实验收与远控基础

日期：2026-11-23（Asia/Shanghai）。继承 Git 基线 `eb89241` 及原有未提交工作。当前可运行产品仍为 **0.2.4-dev / API v1 / schema 1**；目标 **0.3.0-dev / API v2 / schema 2** 尚未完成。本次是可审阅的开发交付，不代表远程桌面或五平台客户端已经可用。

## 方案与模块边界

主方案、REMOTE_NATIVE_PLAN、ADR-0005、任务清单和矩阵同步为 v2.2。新增 INTEGRATION_CONTRACT 的 I01–I10：站点身份、网络通道和远控会话各有独立状态机，通过明确事件协调退出、撤权、路径变化和恢复。媒体必须复用 NetGo 的身份与授权传输；优先验证窄系统适配，再比较 MiniRTC，不同时携带两套媒体引擎。

Windows 维持 C++/Win32；Android Kotlin/JNI；macOS/iOS Swift/ObjC++；Linux C++/GTK。共享 C++ 会话/协议通过纯 C ABI 衔接平台媒体、VPN 和 UI。新数据库与接口一次切换，不建设 v1 兼容桥接或双写。现有开发数据库在当前实验结束且新栈可恢复后才退役。

## 已实现与验证

- Windows 旧单体头文件/平台文件拆分为平台、配置、IPC、连接、密码适配、核心管理和 UI。UI 不链接 OpenSSL；后台独立依赖 Crypto/TLS。Release LTO、节回收、符号剥离和 2/8 MiB 预算，以及仅系统 DLL 检查进入构建。
- 构建发布使用摘要和原子替换；已加载旧映像保留在项目缓存，避免把旧程序混入发行目录。修正干净源码树的前端构建顺序，先生成 Go embed 输入再运行 Go 检查。
- 修复 VPS Relay 的公钥 controller 顺序选到不可达 LAN home 问题：客户端与受管路由节点协商客户端可达且 Management 发布的 Relay；未启用 NetGo 的路径保持上游语义。该补丁不能直接当作未来任意主机 peer-to-peer 协商。
- 正式 API 的 TLS 使用请求剩余总预算，健康探测保留短期限；延迟响应、取消与单次写入回归通过。
- 密码重置在停服务、撤凭据、写维护记录之前检验 IdP 强度；无效输入不产生维护副作用。测试密码显式满足要求。
- `packages/remote-core`：无第三方运行依赖的 C++17/C ABI 门禁，六项能力、目标绑定、本地同意、最长 30 秒租约、双时钟过期、续租收缩和终态撤销。两平台 79 项断言与 C11 调用通过。
- `internal/remote/ticket` 与 `packages/remote-ticket`：Go 标准库签发、C++ 独立 OpenSSL Crypto 验签；固定 168 字节格式和同一公开测试样本。覆盖逐字节篡改/截断、合法签名下的非法字段、绑定、并发验签和真实门禁衔接。最终 423 项断言包含编译/运行密码库版本一致性；C11 链接通过，Go race/vet 与 10 秒模糊测试（503,280 次执行）通过。
- `apps/windows/remote`：DXGI/D3D11 GPU BGRA 采集，当前 1920×1080 桌面三次关闭重开通过，探针仅记录元数据，不保存截图。常规验证只编译和检查无采集边界；未将采集接入网络或 VPN 进程。

## 构建依赖审计与源码证据

交付审计发现 MinGW `FindOpenSSL` 缓存使用 `LIB_EAY/SSL_EAY`，覆盖了之前只传入的 `OPENSSL_*_LIBRARY`。旧检查虽然通过，但实际链接 MSYS OpenSSL 3.6.3、头文件却来自项目锁定 4.1.0；旧 SBOM 的锁定版本因此不能代表实际二进制。相关旧记录原样保留，不能再当作最终交付凭据。

新增共享 `scripts/cmake/NetGoOpenSSL.cmake`，强制选择项目内库、精确版本和 CNG 系统依赖；库缺失直接失败。Windows 实际输出已确认 `Linked OpenSSL 4.1.0`，TLS/Ed25519/IPC/配置回归通过。`native-crypto-missing.txt` 证明缺少锁定库时不会退回系统库。票据 CMake 独立验证见 `remote-ticket-windows-nativecrypto.txt`。

最终源码指纹：`ca1286f12815e08fce25d688f10e7e0b4d0c250c416ff1f5d10d1e4c863cb1bf`。Windows 完整 16 段、Debian 完整 14 段均 PASS，见 `verification-windows-release-final/` 和 `verification-linux-release-final/`。两平台源码逐字节一致，所有检查日志摘要复核通过；Debian 门禁和票据 ASan/UBSan 均无报告。此前 `verification-*-ticket-final/` 属于历史指纹 `924b5b4744c13c9dd17493600979b3997758d47b4ec8ffa55e051c134f827b1a`，不可混用。

Debian 测试所需 OpenSSL 开发文件与对应运行库在实验源码 `.tools/openssl-check` 解包使用；设置多架构头文件、pkg-config 和动态库搜索路径，没有为该测试替换系统运行库。最初两个构建环境失败分别是架构头文件路径缺失和开发库软链接缺少对应运行库，保留在 `verification-linux-ticket-final-failure/` 与 `verification-linux-ticket-final2-failure/`。

## 最终安装版本的实机复验

| 用例 | 结果 | 证据 |
| --- | --- | --- |
| 安装、卸载、重复卸载、保留数据重装与系统归属 | 19 项 PASS | linux-lifecycle-integrated.json |
| 配置、备份恢复、弱密码无副作用拒绝、重置、升级/回退、失败升级恢复 | 34 项 PASS | operations-integrated-convergence.json |
| 全部底层 UDP 与直达控制/本地中继被阻断，走公网辅助中继 | 18 项 PASS | paths-integrated-auxiliary-relay.json |
| 底层 UDP 被阻断，走本地 TCP 中继 | 15 项 PASS | paths-integrated-relay.json |
| 十个真实核心、独立身份、并发数据、续租及清理 | 32 项 PASS | paths-integrated-concurrency.json |

公网/本地中继都验证 TCP 完整性、TCP 中继上的 UDP 应用负载、未授权端口拒绝、内核阻断计数与清理。它们不等于全部 NAT、仅 TCP443、跨 WAN 或 72 小时结果。

首次最终维护演练在最后即时 doctor 失败，随后完整 doctor 恢复；原测试没保存该次细项，不能声称已经确认具体原因。改为最多 45 秒的诊断收敛检查并保存每次完整观测，不跳过任何失败项。重跑的第一轮诊断已全部就绪，见 `doctor-recovery-integrated.json`。原密码和诊断失败分别保留在 `operations-modular-failed.json`、`operations-integrated-first.json`。

`installed-artifacts-release-final.json` 在最终源码重验后，再次核对完整构建的 12 个安装文件和 7 个运行服务可执行映像摘要一致，七服务均 active。`installed-artifacts-ticket-final.json` 是之前的审计记录。此前 API 28 项、隔离网络 48 项、Windows 23 项是历史证据，未伪装为本次新 Windows 映像的实机验证。

## 体积、开发包和清理

| Windows 运行文件 | 原字节 | 最终字节 | 减少 |
| --- | ---: | ---: | ---: |
| netgo-desktop.exe | 11,289,629 | 1,522,688 | 86.51% |
| netgo-agent.exe | 11,225,675 | 6,537,728 | 41.76% |
| netbird.exe | 56,588,800 | 39,407,104 | 30.36% |
| wintun.dll | 427,552 | 427,552 | 0% |
| 合计 | 79,531,656 | 47,895,072 | 39.78% |

摘要见 `artifact-size-final.json`。旧 `artifact-size.json` 是依赖审计前记录。这些数值不代表视频帧率、CPU 或空闲内存；远控媒体尚未进入产品包。

开发包生成要求源码、检查日志及逐个二进制摘要一致，并校验 ZIP CRC 和包内清单。Windows 包为 17,869,713 字节、Linux 产品开发包 28,236,300 字节；完整 Linux 服务端为 112,577,404 字节。源码包包含独立远控模块，运行包仍只提供当前 VPN 功能。最终各包（含源码）摘要/大小以归档完成后生成的外部回执 `package-verification-release-final.json` 和 `server-delivery-release-final.json` 为准，均对应上述最终源码指纹；完整 Linux 包已取回本地并再次核对。归档内 integrated/modular/pre-cleanup 回执只用于历史追溯；归档不可能包含它自己生成后的最终回执。清理统计/推送回执以仓库记录为准。

中途清理曾从 19.958 GiB 降至 2.991 GiB；后续构建重新产生缓存，最终清理前为 6.600 GiB。使用 `go clean -cache -testcache -modcache`、`npm cache clean --force` 后为 **3.251 GiB**（89,823 文件、含最终四个包、按文件表观字节统计），详见 `cleanup-final.json`；原任务起点 19.958 GiB。旧 ZIP/重复验证目录删除两次被自动审批以 `blocked by policy` 拒绝，已保留，未换方法绕过；运行身份/数据库仍用于验收和恢复。

## 未完成的交付条件

RD2 的 API v2/schema 2、主机登记/授权/持钥证明/原子防重放仍待实现；票据签名不是 bearer 登录，也不能单独打开媒体。RD1b 仍需真实硬件编码、授权传输、解码与原生显示，再接输入/音频/剪贴板/文件能力。Android、Apple、Linux 原生客户端尚未交付。I01–I10、RD-A01–A14、A01–A33 全量、72 小时、设备及签名条件仍未关闭。

Windows 旧前台核心 PID 50144 属于管理员权限，当前执行进程不能重启。新程序已构建发布，但新核心真实连接仍 NOT-RUN；项目内刷新脚本位于 `E:/NetGo/.cache/installer-lab/windows-refresh-project-core.ps1`，仅管理员执行后才能复验。该脚本核验项目归属，把程序/状态/日志留在 E 盘，不安装服务。

实现提交 `a6a0d44bfd1952b6734c3d2c8e841ae6ff472c77` 已普通推送 GitHub main，随后用 `ls-remote --heads` 核对远端完全一致、仅 main；本地仅 main、工作区干净，没有需要合并或删除的其他分支。该提交的 221 个代码/构建/测试输入在暂存区逐字节匹配最终验证指纹，防止换行转换造成验证漂移。后续文档回执提交不改变源码指纹。SSH 密码、令牌、私钥、运行数据库、缓存和安装包未加入 Git。
