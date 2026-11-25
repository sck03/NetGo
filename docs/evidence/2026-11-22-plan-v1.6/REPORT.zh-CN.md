# 方案 v1.6 连续实施与审阅记录

> 历史提交记录；原始日志、截图与测试数据已从当前目录清理。可在 Git 提交 `9296955` 的相同路径查看，不能作为本轮修改的验证结果。

日期：2026-11-22（Asia/Shanghai）。范围：Debian 13 amd64 服务端、Windows C++20 客户端、Linux 诊断 CLI、管理网页、部署与验收工具。

## D0：基线与方案校正

已完整阅读执行方案、任务清单、条款映射、进度和最近两轮报告。进入本轮时的代码指纹为 `e618924ed08cac43982d88d4c9f47f588728b581d626b0b3262299985401dc1a`，与 v1.5 完整验证记录一致。工作区已有的未提交改动继续保留；D0 时尚未执行提交、推送或系统服务安装。

需要修正的是当前执行入口和完成定义：旧方案顶部同时存在 S0–S5、C0–C4 及两个“本轮”证据目录。v1.6 统一为 D0–D4，历史结果保留原目录；源码、自动检查、真实验收和可发行分别登记。

## D1：核心流程

| 编号 | 复现条件与影响 | 修复 | 验证 |
| --- | --- | --- | --- |
| D01 | Linux 每次 Prepare 都调用 AddProfile，假设重复名称返回 AlreadyExists；固定上游实际允许同名 profile，重连会新增身份并造成歧义 | 按当前用户列出 profile，复用唯一匹配项；歧义拒绝，不自动删除设备身份 | 两次 Prepare 只创建一次；歧义不登录/不新建 |
| D02 | 申请租约后 NetBird 路由下发异步，Linux 只查询一次而可能过早报告失败 | 与 Windows 适配一致等待最多 15 秒，受调用方取消/截止约束；全量授权资源就绪后选择并去重 | 延迟下发、取消、未完整路由和 daemon 错误回归 |
| D07 | gRPC 按顺序探测 TLS，最多八个候选中前面卡住的入口可耗尽 20 秒核心 RPC 预算 | 传递候选类别，并发直达、800ms 后备用、每候选 3 秒；保留 SNI/authority、TLS/HTTP2 校验和取消清理 | 七个卡住入口加最后一个可用入口，修复前失败，修复后通过；优先级、取消、错误 TLS 身份与输入边界回归 |

修复前实际执行 `go test ./internal/engine/netbird -run 'TestPrepare|TestRoutes' -count=1`，四项回归失败，分别为重复 profile、同名歧义、异步路由及取消错误。修复后执行 `go test -race ./internal/engine/netbird ./internal/client -count=1`，两包通过，见 [客户端专项日志](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/d1-client-tests.txt)。测试使用 daemon RPC 替身，不启用工作站 VPN。

gRPC 在独立上游工作副本增加真实回环 TLS/gRPC 测试：原实现于 2 秒调用期限内失败，见 [修复前日志](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/d1-grpc-before.txt)；修改后执行 `go test -race ./client/grpc ./client/cmd -run TestNetGo -count=1`，两包通过，见 [修复后日志](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/d1-grpc-after.txt)。补丁已从固定源码重新生成，并在 D3 用另一个干净 checkout 重放构建通过；未进行真实远程 Management/Signal/LAN 测试。

## D2：部署与交付

| 编号 | 缺口 | 实现与验证 |
| --- | --- | --- |
| D03 | 允许低位端口的 Relay/frps 以普通用户运行，却无对应绑定权限 | 增加仅 CAP_NET_BIND_SERVICE；保持 netgo 用户；systemd 运行仍待 Debian 验收 |
| D04 | 原打包只检查构建目录自身的摘要，可把已改源码与旧程序组合 | schema 2 绑定源码、全部检查/日志与构建文件；打包前后检查，ZIP 内字节再与验证输入比较，自动生成交付校验 JSON |
| D05 | 已应用补丁的上游缓存即使包含其他源码变化仍会复用 | 临时 Git index 重建固定 commit + 补丁 + Dex replacement，拒绝额外/被忽略源码及无关修改；版本从锁文件读取；归档核对 HEAD |
| D06 | 完整检查只有 Windows 入口，Linux 代码路径与 CGO 构建没有统一留证入口 | verify-linux.py 提供 13 段目标平台检查和 --plan；失败/中断留证、停止后续构建；最终校验失败不会留下 PASS |

中间验证：29 项工具测试通过，随后新增的四项上游输入测试全部通过；实际创建了新的固定上游 checkout 并完成全树核对。见 d2-tools-tests.txt、d2-upstream-inputs-tests.txt、d2-upstream-preparation.txt。归档与最终工具集合在 D3 一并运行。测试中的替身命令和 --plan 不代表 Debian 实测。

## D3：集成验证

已执行 `pwsh -NoProfile -File scripts/verify.ps1 -Output .cache/verification-20261122-plan-v16`，2026-11-22 15:15（Asia/Shanghai）完成，**15 段全部 PASS**，未跳过 native/upstream。

| 检查 | 实际结果 |
| --- | --- |
| Python 工具回归 | 38 项通过，包含原部署/证据/预检查和新增上游输入、验证绑定、Linux 检查失败留证、归档校验 |
| Go 产品 | 全部 internal/cmd 竞态检查与 vet 通过；五个 Linux 产品交叉编译、发行清单校验通过 |
| 管理页面 | 七项认证测试、Vue 类型检查和生产构建通过 |
| Windows 原生 | 三项 CTest、18 项共用 profile 契约、TLS/IPC、多候选/多 WAN 回环检查通过 |
| 上游 | 新固定 checkout 全树核对、补丁重放、gRPC/命令适配测试和 NetBird Windows 构建通过 |
| 部署与证书 | 脚本语法/计划、证书正反输入与六种配置组合通过；没有安装或启用系统服务 |
| 交付输入 | 验证前后源码指纹一致；构建文件摘要已记录，复制后的日志重新校验通过 |

逐项命令日志和摘要见 [verification.json](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/verification/verification.json)，绑定复核见 [verification-binding.json](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/verification-binding.json)。170 个代码/构建/测试输入的指纹为 `142184de441c3c2c2ee8f52de35ab1f5865571dc7a7ac897cd93222920a40a6d`。相对本轮开始的 v1.5 输入，修改 17 个、新增 7 个、删除 0 个；详见 [源码差异索引](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/source-delta.json)。历史未提交实现一并保留。

## D4：文档与终检

已生成并复核 0.2.3-dev 的 Windows、Linux 产品开发、完整源码三个 ZIP，SBOM、SHA256SUMS 和交付校验记录均在 `dist/packages/`；保留旧版本有效开发包。三个 ZIP 的 CRC、包内逐层摘要、已验证构建/源码字节以及外部摘要均通过，源码包包含匹配的完整 NetBird/Dex/OpenSSL 上游源码。

接续原会话「修订执行方案并分阶段完成项目」（`01a201f7-ee1f-7cc1-9a10-3c647a54700e`）因限流中断的收尾任务：170 个源码输入、15 份检查日志、12 个构建输入仍与 D3 一致，沿用其实际测试结果；重新执行包完整性核验，接续时源码 ZIP 中的 341 个已有项目文件与工作区一致。随后同步本报告、方案、任务清单与进度，并把最终文档重新纳入开发包。

推送检查发现七份历史 `.log` 被全局忽略规则遗漏，且本轮 13 份验证日志含 CRLF，默认 Git 换行转换会破坏记录的 SHA-256。已限定在 `docs/evidence/` 保留原始字节、允许提交审阅日志，代码与构建输入未变。既有历史日志仅恢复工作区保留的原始换行，结果内容不改写。

Git 暂存区实际复核通过：348 个文件与工作区逐字节一致，170 个源码输入与完整验证指纹一致，三轮记录中的 42 份检查日志摘要全部有效；证据目录无被忽略文件，Markdown 本地链接均有效。`git diff --cached --check` 通过，证据目录允许原始 CRLF。最终源码 ZIP 还需与同一 Git 文件集合核对，结果随交付回执保存。

可复跑的绑定检查为 `python -B scripts/verification.py check --platform windows --report docs/evidence/2026-11-22-plan-v1.6/verification/verification.json`；开发打包为 `python -B scripts/package.py --version 0.2.3-dev --verification docs/evidence/2026-11-22-plan-v1.6/verification/verification.json`。最终包摘要及检查记录以 `dist/packages/SHA256SUMS` 和 `NetGo-0.2.3-dev-delivery-verification.json` 为准。

## 真实验收边界

本轮重新初始化并汇总 Debian 13 环境模板：33 项 NOT-RUN、完整性错误 0、发行门槛 CLOSED，见 [网络验收汇总](network-summary/REPORT.zh-CN.md)。当前没有已配置且获授权的 Debian/VPS/DNS 实验环境。A01–A33、G1–G6 和 72 小时观察继续保留未测状态；Linux 产品开发包不能称为完整生产服务器包。后续真实测试条件见 `docs/NETWORK_ACCEPTANCE.zh-CN.md` 与环境模板。

## 用户追加：清理与 GitHub main

用户要求最后清理无用/无效文件并推送 GitHub main。已确认 origin 为 `https://github.com/sck03/NetGo.git`，fetch 后本地与远端 main 无分叉。清理仅针对已被现行实现取代的临时目录、旧验证产物及 Android/Zig 缓存；当前源码、锁定构建输入、历史有效证据和开发包保留。

批量永久删除被自动审批以 `blocked by policy` 拒绝，改用 Windows 回收站执行可恢复清理。20 项、共 1,584,006,847 字节（约 1.48 GiB）的废弃文件已移入回收站，接续复核确认这些路径均已从工作区移除；没有清空回收站。逐项目录边界与 reparse point 检查、文件数和结果见 [清理计划](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/cleanup-plan.json)与[实际结果](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-22-plan-v1.6/cleanup-result.json)。临时 gRPC 副本在清理前已确认全部修改保存在交付补丁。

开发包与清理核验已完成，当前源码、文档和有效历史证据按用户授权纳入本轮提交并正常推送 `https://github.com/sck03/NetGo.git` 的 main。实际提交与远端一致性以 GitHub main 历史和本地 `dist/packages/NetGo-0.2.3-dev-main-receipt.json` 为准；该回执在推送成功后生成，不放入源码 ZIP，避免摘要和提交号自引用。
