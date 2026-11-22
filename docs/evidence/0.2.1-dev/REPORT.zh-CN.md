# 0.2.1-dev 续作与审阅报告

日期：2026-11-22（Asia/Shanghai）。工作区：E:/NetGo。当前范围为 C++20/Win32 Windows 桌面端与 Go Linux 服务端、诊断 CLI；macOS/Android/iOS 按用户最后决定后续扩展。

## 本次源码交付

| 阶段 | 本次补齐 | 审阅位置 |
| --- | --- | --- |
| P0/P1 权限与服务 | 网关表归属、冲突 offload 拒绝；先关闭受影响 IP、清理 scoped conntrack、再开放；失败保留关闭/重试 | internal/gateway，gateway_test.go |
| P1/P4 运维 | 配置变更计划、独占恢复日志、原子文件替换、健康/协调失败恢复；升级清单/兼容校验、停写快照、版本切换与回退 | internal/operations、internal/securefile、cmd/netgoctl |
| P1 安装 | Linux 归属清单、失败恢复、重复安装/保留身份重装、专用 netgo-router 服务、产品 CLI 链接、完整受管卸载 | deploy/linux |
| P2/P3 客户端 | Windows 并发健康探测、800ms 备用延迟、DNS 取消/请求截止、30s/3 次恢复确认；超时与清理失败显示/限制编辑；密码避免后台多份长期捕获 | apps/windows/src |
| P2 Linux CLI | 终端账号密码命令、持久 generation、错误密钥不覆盖、原生 CA 传入核心、核心重启恢复、SIGTERM 清理 | internal/client、cmd/netgo-client-service、cmd/netgoctl |
| P3/P4 安装与材料 | Windows 旧文件/服务失败恢复、精确服务路径/SID 校验；版本与 release.json、摘要、手册和方案同步 | deploy/windows、scripts、docs |
| P1/P4 配置与备份修复 | 策略版本不随租约/审计递增，服务自动重试未完成协调；修正默认备份两个 netgo 根目录同名冲突，增加来源映射和文件稳定性检查 | internal/store、internal/backup、cmd/netgo-server |
| P2 地址变化 | Linux netlink 网卡/地址/路由事件触发 DDNS，合并突发事件；定时观测保留为兜底 | internal/ddns、cmd/netgo-ddns |

## 验证记录

已完成以下自动验证；原始日志保存在同目录。Linux 的内核门禁、netlink 事件和版本符号链接事务未在 Windows 上运行，仍需目标系统实测。

| 验证 | 状态 | 证据 |
| --- | --- | --- |
| Go 竞态/行为回归 | PASS，包括授权、按对象撤销、conntrack 顺序/失败重试、配置恢复、备份同名目录、策略版本、generation、防重放和 JSON 限额 | go-race.log |
| Go vet 与 Linux 五个产品交叉编译 | PASS | build.log、product-build.log |
| Windows 原生构建、CTest、回环 TLS | PASS；含七个 TLS 卡住的直达候选与有界备用探测、SNI/Host、签名和错误证书拒绝 | native.log |
| Vue 类型检查、构建、认证测试 | PASS，4 个认证测试 | build.log |
| PowerShell/Bash/Python 检查、安装计划 | PASS；安装/回退仍未实际执行 | scripts.log |
| 当前 Linux 产品清单与兼容字段 | PASS，实际二进制清单检查 | release-verify.log |
| 开发包、源码、SBOM、摘要与 ZIP CRC | PASS；开发包已生成，不代表生产发行通过 | packaging.log |

## 未完成的真实验收

G1–G6、A01–A33 仍没有本轮真实 Linux/LAN/NAT/VPS 环境证据；Windows 实际安装、跨用户、点击、休眠/换网，Linux nft/systemd/conntrack/符号链接事务，DNS-01、72 小时和性能仍待实测。Windows 上的 Go 发行链接恢复测试会跳过，须在 Linux 重跑。没有发布通过声明。

产品升级命令当前仅支持不改变 schema 1 的 NetGo 二进制。上游组件升级/单元变更/不可逆迁移须另行制定完整一致快照恢复流程。0.2.0-dev 未标记防火墙和无安装归属清单的旧实验安装需要人工迁移，不会直接覆盖。

本轮源码在本地审阅，尚未提交或推送；此前 main 上的 0.2.0-dev 证据保留在 implementation 目录。临时移动平台代码已撤掉。工具缓存递归清理被自动安全检查以 blocked by policy 拒绝，缓存处于 Git 忽略目录，不进入交付源码。

交付位置：dist/packages/NetGo-0.2.1-dev-windows-amd64.zip、NetGo-0.2.1-dev-linux-product-development.zip、NetGo-0.2.1-dev-source.zip，以及同目录 SBOM.cdx.json、SHA256SUMS。Linux 包包含本项目产品文件，完整 NetBird/Dex 控制服务仍须在 Linux 原生启用 CGO 构建；源码包附匹配上游源码与补丁。客户端新平台未包含在这些包中。

补充记录：修正的配置输入拒绝超限 JSON 尾部、非数字端口和不合法监听/接口参数。netlink 使用既有锁定 golang.org/x/sys v0.50.0，仅将其标为直接依赖，没有升级依赖版本。git diff --check 通过。
