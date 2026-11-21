# 0.2.0-dev 实际实现与验证报告

日期：2026-11-22（Asia/Shanghai）。本报告区分源码/自动验证与真实网络发行验收。所有结论来自本轮实际运行，不能用计划或模板替代运行证据。

## 本轮交付

- Windows 界面与产品后台已全部改成 C++20 / Win32：配置列表/编辑/导入导出、账号密码、连接/断开、控制入口和数据路径、授权资源、日志/诊断、托盘、启动偏好、Credential Manager。
- 原生后台实现 OpenSSL Ed25519、PKCE、系统根/公共 CA 的 TLS 验证、双入口恢复、租约续期、身份刷新向 NetBird 核心传播、同设备重启接续、持久 generation、取消与清理失败处理。
- SID 命名管道只接受固定动作和有界 JSON。测试使用独立管道与禁用网络核心的后台模式，不对实际 VPN 发命令。
- 删除旧 Wails 桌面入口、Go Windows IPC/vault、网页 desktop 分支及旧 Windows Go 运行产物；Go 中仅保留用于开发主机执行服务端测试的不可用 stub。独立 NetBird 网络核心保持上游 Go 实现。
- 补齐 Linux 管理端账号/设备删除、并发上限、到期、管理登录刷新与按对象撤销。最后一个有效管理员不能删除/禁用；资源或账号变更不应断开无关用户。
- 管理登录使用受角色限制的临时签名身份，不占 VPN 设备限额，也不能取得网络租约；凭据过期后清理临时身份。刷新结果包含新的核心 ID token，避免续期后向 NetBird 提交空令牌。
- 保存可重放的 NetBird/Dex 补丁和版本锁；新建干净 checkout 已成功应用补丁并构建 Windows 核心。
- 修正 Caddy 模板单行花括号错误，诊断/443 模板均通过适配；frpc/frps 配置通过语法验证。Caddy 用于 Linux 服务端 HTTPS/gRPC 统一入口，Windows 运行包不含 Caddy。
- 原生安装使用独立 NetGoNetBird 服务/npipe://netgo-netbird，核心与签名驱动放在受保护 Program Files；首启禁止自动连接官方云。

## 自动验证结果

| 实际执行 | 结果 | 证据 |
| --- | --- | --- |
| npm run build | PASS，类型检查及正式静态资源生成 | frontend-build.log |
| npm test | PASS，4 项：设备证明/PKCE＋刷新签名；普通账号拒绝；外部 URL 拒绝；注销取消竞态 | frontend-tests.log |
| go test -race -count=1 ./internal/... | PASS，包括并发、替换、过期、按对象隔离、同设备新代次接续及旧代次拒绝、身份绑定、防重放、备份篡改 | go-race-tests.log |
| go vet ./internal/... | PASS | go-vet.log |
| CMake/GCC -O2 原生构建与 CTest | PASS，2 个测试入口；含 RFC8032 Ed25519、TLS/HTTP 解析、配置注入、系统凭据、存储、重启 generation、IPC 帧界限和退出 | native-build.log |
| 回环 TLS fixture | PASS，备用入口、规范 SNI/Host、签名；错误名称、未知 CA、过期证书全部拒绝 | native-build.log |
| NetBird 固定补丁重放及 Windows client 构建 | PASS；client/grpc TestNetGo 通过，client/cmd 构建验证通过 | upstream-build.log / patch-replay.log |
| Dex TestNetGo | PASS，固定 fork 内部原生入口 | dex-tests.log |
| Caddy 2.11.9 adapt | PASS，诊断/443 两种模板 | caddy-adapt.log / caddy-443-adapt.log |
| frpc/frps 0.72.0 verify | PASS | frpc-verify.log / frps-verify.log |
| Wintun 0.14.1 官方 ZIP hash 与 DLL Authenticode | PASS，签名状态 Valid；输入 hash 在版本锁 | versions.lock.json |
| Windows exe 导入表 | OpenSSL/C++ 静态链接；仅 Windows 系统 DLL，未引入 WebView/.NET/Qt 运行依赖 | native-imports.txt |
| 从 GitHub main 全新克隆 | PASS，npm ci/build/test 与所有内部 Go 包测试；验证代码提交 388efc6 | published-verification.log |
| PowerShell/Bash/Python 脚本静态检查、安装 -Plan | 结果随最终交付校验记录；没有安装或启用实际 VPN 服务 | packaging-checks.log |

自动验证使用 Windows 开发主机的隔离 Go/Node、MSYS2 GCC 16.1.0、OpenSSL 4.1.0 和指定 Python 3.13。TypeScript 7.0.2 与当前 vue-tsc 不兼容，固定 6.0.3。GCC 的 MinSizeRel 暴露静态 libstdc++ 链接问题，改用实际验证通过且不隐藏警告的 -O2。系统 Python 与 MSYS2 Python 不同，构建可用 -Python 指定带 cryptography 的解释器。

## 真实窗口检查

原生 netgo-desktop.exe 已启动，无障碍树证据为 native-accessibility.txt。Windows computer-use 截图接口返回 0x80004002“不支持此接口”，点击接口返回 coordinate input geometry is unavailable。因此没有截图、点击或像素布局通过声明。旧 Wails 窗口证据已按清理要求移除。

## 需求追踪与发行边界

| 要求 | 实现位置 | 真实验收 |
| --- | --- | --- |
| R01/R02 本地单机、无 VPS | Go 服务、NetBird/Dex 补丁、独立 Relay/STUN 模板 | A01/A02 未执行 |
| R03/R04 动态入口/可选 VPS | DDNS、C++ Transport、gRPC 候选、frp/HAProxy 模板 | A03–A15 未执行 |
| R05/R06 路径与本地兜底 | NetBird 完整网络核心、peer 状态、本地 Relay 保留 | 直连/打洞/中继及 VPS 全停未实测 |
| R07 原生账号密码 | C++ Controller、Ed25519/PKCE、Dex/Management 绑定 | 真实 Windows→Linux 登录未实测 |
| R08/R09/R10 LAN、授权、分流 | Networks 协调、租约、nft 门禁、路由选择/冲突检查 | LAN、INPUT/FORWARD、conntrack 未实测 |
| R11 安装/备份/回退 | 安装卸载脚本、加密备份、版本切换 | Linux/Windows 安装失败恢复未实测 |
| R12 状态与诊断 | 控制/数据分开显示、错误分类、白名单诊断 | NAS 实际可达性与 UI 点击仍需实测 |

全部 A01–A33、G1–G6、跨 Windows 用户、休眠/换网、Linux 原生上游 CGO 运行、72 小时和性能测试均未完成。发行门槛保持关闭。

Windows 开发包可审阅/运行普通界面；安装网络服务应放在专用实验机。Linux product development 包只含本项目的交叉编译产物，需在 Linux 原生构建带 CGO 的 NetBird/Dex 才能组成完整服务端。源码包包含完整固定 NetBird、Dex、OpenSSL 源码与本项目补丁；依赖锁/SBOM/哈希另附。

## 发布与清理

源码已推送到 https://github.com/sck03/NetGo 的 main，保留原始提交历史。首次推送漏掉 internal 目录，已在下一提交补齐；全新远端克隆验证通过。有效行为测试保留，旧 Wails 窗口材料、Python 编译缓存和无关版本查询已清理。

本地 dist/packages 包含 Windows 开发包、Linux product development 包、完整源码包、SBOM 和 SHA256SUMS；ZIP CRC 检查通过。Git 不包含这些大二进制、SDK、编译缓存或真实秘密。公开安装计划记录使用已脱敏 SID/路径。
