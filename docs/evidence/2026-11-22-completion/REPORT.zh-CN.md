# 方案 v1.5 完整性复核与实施记录

> 历史提交记录；原始日志、截图与测试数据已从当前目录清理。可在 Git 提交 `9296955` 的相同路径查看，不能作为本轮修改的验证结果。

日期：2026-11-22（Asia/Shanghai）。基于原有 0.2.1-dev 工作区继续，产出版本 0.2.2-dev；不重置已有改动。范围：Windows C++20、Debian 13 amd64/Go、管理网页、部署与验收工具。

## 判定

需要先修正方案的完成口径，再连续分段实施。已有多数代码与自动测试，但 v1.4 的当轮完成记录未覆盖主方案全部条款。本次按 C0–C4 补齐并留证；真实 Linux/LAN/NAT/VPS、Windows 跨用户和 72 小时验收仍需实验环境。

| 编号 | 复核发现 | 对应条款 | 当前状态 |
| --- | --- | --- | --- |
| C01 | 账号修改仅删除已有凭据；已完成密码验证的在途事务仍可能兑换新凭据 | §6.1、§6.3 | 已补认证代次；迟到兑换回归通过 |
| C02 | 缺少协调 IdP 密码重置与产品凭据/租约撤销的运维入口 | §6.1、§6.4 | 已实现维护命令；故障/重试回归通过；实际系统待测 |
| C03 | Profile 没有配置版本；已认证客户端未取得并应用服务端新候选 | §5.1、§9.3 | 已实现；18 项共用契约、登录同步故障和更高版本回退测试通过 |
| C04 | 健康接口仅有协议号，网页未校验；没有能力协商和显式不兼容写入拒绝 | §9.3 | 已实现；Go/原生/前端专项验证通过 |
| C05 | 诊断导出主要为计数；管理端没有运行检查/告警入口 | §10.2、§13.1 | 已接入采样、告警与脱敏追踪；并发/无采样/陈旧/证书/脱敏回归通过 |
| C06 | 用户确认 Debian 13；旧清单遗漏 Caddy/frp 原生构建与受管 TLS/443 单元 | §2、§8、§10 | 已补基准/清单/单元，预检查与脚本自动验证通过；Debian 原生构建/系统待测 |

## 实际验证

C1：`go test -race ./internal/identity ./internal/operations ./internal/api ./internal/session` 四包通过。覆盖账号变更后旧认证拒绝、重新认证成功、先关闭门禁再调用 IdP、失败停止、恢复原服务状态、无关凭据保留及密码不写维护记录。测试使用替身，未改动主机密码或安装系统服务。

C2/C3 中间验证：Go 全部内部/命令包竞态通过；`npm test` 七项通过；`build-native.ps1 -SkipDependencies` 三项 CTest（含 18 项跨语言配置契约）、回环 TLS、签名、七个阻塞候选和多 WAN 恢复通过。上游 RPC 新字段将在完整上游构建中验证。

用户补充 Debian 13 后，Python 部署/证据/系统预检查共 20 项回归通过。实际没有安装 Debian 包或启用工作站 VPN。Go 再次完成全包竞态/vet，新增诊断、配置同步和日志脱敏测试通过；Vue 类型检查及生产构建通过。

配置事务额外修复：失败时不能直接恢复较低 ConfigRevision，因为部分客户端可能已取到新版本。现使用拟用版本 + 1 重新发布旧内容，并通过回归验证客户端可以接受恢复版本。

基线命令：`pwsh -NoProfile -File scripts/verify.ps1 -Output .cache/verification-20261122-baseline-audit -SkipNative -SkipUpstream`。

11 个运行检查 PASS；native/upstream NOT-RUN；总状态 INCOMPLETE。包含 Go 竞态/vet、工具回归、前端构建、脚本、证书、部署组合和产品交叉编译。最终修改完成后须再运行完整验证并保存本轮日志。

## 最终集成

实际执行：`pwsh -NoProfile -File scripts/verify.ps1 -Output .cache/verification-20261122-completion`，总状态 **PASS**，14 段全部通过，没有跳过 native/upstream。完成时间：2026-11-22 14:02（Asia/Shanghai）。

| 检查 | 实际结果 |
| --- | --- |
| 工具链与源码输入 | 锁定 Go/Node 通过；验证前后代码指纹一致 |
| Python 工具 | 部署、证据和 Debian 预检查共 20 项通过 |
| Go 行为与静态检查 | `go test -race ./internal/... ./cmd/...`、vet 通过 |
| 管理前端 | 七项认证测试、Vue 类型检查及 Vite 生产构建通过 |
| 脚本/证书/部署 | Bash/PowerShell/Python 检查、证书正反路径、六种配置组合通过 |
| Windows 原生 | 三项 CTest（含 18 项共用配置契约）、TLS/IPC/签名、多 WAN 恢复通过 |
| 固定上游补丁 | 新补丁摘要对应专用 checkout 重放，RPC 测试及 Windows NetBird 构建通过 |
| 产品交付检查 | 五个 Linux Go 产品交叉编译及 release manifest 校验通过 |

逐项耗时和日志摘要见 [verification.json](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-completion/verification/verification.json)；[source-inputs.txt](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-completion/verification/source-inputs.txt) 保存每个代码/构建/测试输入的 SHA-256。代码总指纹：`e618924ed08cac43982d88d4c9f47f588728b581d626b0b3262299985401dc1a`。文档、日志、生成资产与运行秘密不计入代码指纹。

仅复制验证文本日志与清单；没有把生成的部署秘密、私钥或运行数据库放入证据目录。

实际执行 `python -B scripts/package.py --version 0.2.2-dev`，输入文件摘要、产品兼容信息和 ZIP CRC 校验通过。最终文档随包更新，产物在 `dist/packages/`：

- NetGo-0.2.2-dev-windows-amd64.zip：C++ UI/后台、同步字段的独立核心、Wintun、安装脚本与许可。
- NetGo-0.2.2-dev-linux-product-development.zip：五个产品程序、Debian 预检查/部署与证书脚本、模板、工具和操作手册。
- NetGo-0.2.2-dev-source.zip：项目源码、文档、精确上游源码与当前补丁。
- SBOM.cdx.json、SHA256SUMS：依赖信息和最终文件摘要；旧版本开发包保留。

本轮 C0–C4 开发范围已完成；完整条款索引见 [IMPLEMENTATION_MATRIX](../../IMPLEMENTATION_MATRIX.zh-CN.md)。文件权限部署步骤已校正：根配置为 root:netgo/0640，首次 init-admin 由 netgo 用户创建数据库；路由节点状态仍归 root。

## 真实验收

已使用 Debian 13 环境模板重新执行 `run.py init` 与 `report`，结果：33 个 NOT-RUN、完整性错误 0、发行门槛 CLOSED；见 [新验收汇总](network-summary/REPORT.zh-CN.md)。没有已配置且获授权的 Linux/VPS/DNS 实验环境；源码或回环测试不替代真实流量、systemd/nftables、密码库恢复和设备验收。

仍需：Debian 13 原生 CGO 构建/安装、完整 IdP/Management 流程、H3C 实机多 WAN、LAN/NAT/Relay/TCP-only、真实撤销、跨 Windows 用户及休眠/换网、DNS-01 续期/重载、干净主机恢复与 72 小时稳定性。当前 Linux 包仍为产品开发包，不是完整生产服务器包。未新增提交或推送。
