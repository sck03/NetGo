# 方案 v1.4 连续实施审阅记录

日期：2026-11-22（Asia/Shanghai）。范围：Windows C++20、Linux Go、自动验证与验收工具。用户确认暂无实验环境。

## 初始状态

- 基于现有 0.2.1-dev 未提交改动继续工作，保留此前实现。
- 基线 `go test -race ./internal/... ./cmd/...` 通过；这不验证 Linux 内核或外部网络。
- 方案升级为 v1.3，将本轮源码推进和 P0–P4 真实门槛分开。

## 已识别问题与处理记录

| 编号 | 实际问题 | 状态 | 关联 |
| --- | --- | --- | --- |
| F01 | 部署渲染固定使用 8080/8081 等，可能与已校验配置不一致 | 已修复，六种组合与非默认端口验证通过 | S2、A01/A28 |
| F02 | 诊断模式 VPS Relay 发布 443 却监听 8443，且没有 TCP 转发 | 已修复，监听/发布/映射统一；本地/VPS 模式独立 | S2、A06/A09 |
| F03 | 证据采集遇超时不保存结果，记录的摘要与日志字节不一致，无完整性校验/用例汇总 | 已修复，17 项工具回归通过 | S4、A01–A33 |
| F04 | 认证后等待协调/peer 查询的请求，可在令牌撤销后继续申请租约 | 已修复并通过竞态回归 | S1；提交时核对当前凭据，续租和幂等重试同样检查 |
| F05 | 并发创建不同邮箱、相同别名的用户可通过事务外唯一性检查 | 已修复并通过 20 请求竞争测试 | S1；事务内检查并创建，写入失败清理新上游账号 |
| F06 | DDNS 可将同一探测主机计作多个来源，未固定地址族，不能识别多 WAN 的全部 IP | 已修复：不同主机、一致结果、分地址族直连；多 WAN 优先路由器 DDNS 或独立可信地址文件 | S2、A04/A32 |
| F07 | 管理页面并发请求可重复消费刷新令牌，注销时在途请求缺少主动取消 | 已修复，六项前端认证测试通过 | S3、A15/A26 |
| F08 | 原生 HTTP 解析接受含糊长度与不完整 chunk 结束 | 已修复并通过原生解析回归 | S3、A14 |
| F09 | 配置事务与版本切换可交叠，事务外读取的旧状态可能过时 | 共用操作锁并在锁内读取；竞态回归通过 | S1、A29 |
| F10 | Relay 分类使用子串匹配，可能把其他端点误报为本地中继 | Go 与上游桥接补丁按主机及必要端口精确匹配 | S3、A06/A11 |
| F11 | Go 从备用恢复时仅检查第一个直达入口；Windows 多入口串行探测且确认计数不绑定候选 | 两端均探测全部 WAN 并确认同一候选，新增三 WAN 回归 | S2/S3、A07/A08 |

## S1 服务、权限与运维

- 本线程接续原会话的工作区，没有重置既有改动。
- 核查本地身份适配、签名/刷新、租约门禁、管理 API、上游策略协调和已有配置/升级恢复事务。
- 修复 F04/F05。请求认证附加的 token 摘要只存在内存，不写入数据库或接口响应；在申请/续租的事务内再次确认其有效性。
- 实际运行（Windows，工作区 Go 1.27.4 与 UCRT64，CGO_ENABLED=1）：`go test -race ./internal/api ./internal/session ./internal/identity ./internal/operations ./internal/store ./internal/gateway`，六包全部通过。
- 这些测试使用受控管理 API 和网关替身；真实 Linux 规则、上游用户/密码流程、systemd 恢复仍需按 Axx 验收。

以下仅报告实际完成的源码、自动验证与交付材料；真实网络及生产发行条件分别记录。

## S2/S3 端口、客户端与多 WAN

- 用户确认国内动态公网存在入站 443 限制，默认初始化与桌面新站点改为控制 10443、Relay 18443；老配置不会自动修改。VPS 可独立配置高位端口或 443。
- 生成器保留 Origin 中的端口、读取已配置内部地址，校验监听冲突、SNI、Relay/STUN 和备用候选，输出 deployment.json 摘要。实际 Caddy 2.11.9 与 frpc/frps 0.72.0 校验六种组合通过；覆盖 IPv6 回环和非默认端口。
- Go DDNS 拒绝重复探测主机、不一致/私有/CGNAT/文档地址，按 A/AAAA 固定探测出口族。H3C 多 WAN 由路由器负责拨号/NAT/回程；NetGo 可消费独立的受保护地址文件并检查时效。
- Windows CTest、回环 TLS、签名、七个阻塞候选和 10443 authority 通过。管理认证六项测试通过，含 20 个并发请求共用一次刷新。
- 新增 DNS-01 证书部署 hook 的只读验证模式：正确域名/密钥通过，错误域名、错误密钥及临近到期均拒绝；实际签发与系统服务更新仍未测。
- H3C 型号为用户提供的 MSR3610-X1-DP；没有连接、修改或验证其实际 Comware 配置。完整部署约束见 docs/MULTI_WAN.zh-CN.md。

## S4 验收工具

- `python -B -m unittest discover -s tests/tools -v`：17 项通过，含日志字节摘要、超时/失败/超限、篡改/路径拒绝、审阅绑定、旧 PASS 不覆盖新失败、33 用例与需求一致性和分路径指标。
- `run.py init` 和 `report` 已实际运行，生成 network/ 和 network-summary/；33 项全部 NOT-RUN，R01–R12 均 NOT-PASSED，发行门槛 CLOSED，完整性错误 0。
- 本地自动化证据不能审阅为真实 PASS；命令成功只表示需要人工核对。真实网络脚本、抓包和故障恢复必须由实验环境提供。

## S5 集成状态

用户补充多 WAN 后执行 `scripts/verify.ps1 -Output .cache/verification-20261122-multiwan`，最终 13 段全部 PASS。逐项命令入口、日志和 SHA-256 位于 `verification/verification.json` 及同目录文本日志；没有复制部署生成目录中的秘密或测试证书。

| 验证 | 实际结果 |
| --- | --- |
| Go toolchain/Node 与版本锁 | PASS |
| Python 部署与证据工具 | 17 项 PASS |
| Go `test -race ./internal/... ./cmd/...` 与 vet | PASS；Windows 上不运行 Linux 专属程序 |
| 前端认证与生产构建 | 六项测试 PASS，Vite/Vue 构建 PASS |
| Bash/PowerShell/Python 语法与安装计划 | PASS；实际安装未执行 |
| 证书输入检查 | 正确证书通过，错误域名/密钥/有效期拒绝 |
| 六种本地/VPS 配置组合 | Go validate、Caddy adapt、frpc/frps verify PASS |
| Windows 原生 | 两项 CTest PASS；TLS/IPC/签名、10443 authority、多 WAN 回退与恢复 PASS |
| 上游固定源码与补丁 | 新专用 checkout 重放、RPC 适配测试及 Windows 核心构建 PASS |
| Linux 产品构建 | 五个 Go 产品程序交叉编译 PASS |
| 产品 release verify | 清单与兼容信息 PASS |
| `python -B scripts/package.py` | 三个当前版本开发包、SBOM、SHA256SUMS，输入哈希及 ZIP CRC 校验 PASS |

当前交付目录：`dist/packages/`：

- `NetGo-0.2.1-dev-windows-amd64.zip`：C++ UI/后台、独立核心、Wintun、安装脚本和许可。
- `NetGo-0.2.1-dev-linux-product-development.zip`：五个产品程序、部署/证书脚本、单/多 WAN 示例、验收工具和操作手册。
- `NetGo-0.2.1-dev-source.zip`：完整项目源码、文档、精确上游源码与补丁。
- `SBOM.cdx.json`、`SHA256SUMS`：依赖及产物摘要；旧 0.2.0-dev 包保留，当前交付以 0.2.1-dev 文件名为准。

本轮没有新增提交或推送。Windows/Linux **代码、自动验证和验收工具的授权范围已完成**。Linux 产品包仍需在目标 Linux 原生构建带 CGO 的 NetBird/Dex，不能当作完整生产服务端安装包。

## 待实验环境完成

G1–G6、A01–A33 均未宣告通过。H3C Comware 版本与配置、各 WAN 公网/CGNAT、NAT 回程、Linux systemd/nft/HAProxy、实际 DNS-01 续期与服务回退、跨 Windows 用户安装/休眠/换网、真实权限阻断及 72 小时稳定性仍待实测。已准备环境模板、逐项用例和证据工具；`network-summary/summary.json` 显示 33 个 NOT-RUN、0 个完整性错误、发行门槛 CLOSED。
