# 远程桌面、入口配置与 Linux ARM64 验证

日期：2026-11-25（Asia/Shanghai）。基线提交：ade73e263741c273eb397f6b818c45ce1ac44781。实现提交：83abe51518fb2ba6dc21746b907ba7636cbb3539；Windows 最后提示文字修订：fbbc9077ccdb40a7014db89d91169783f3b8b1be。

## 本轮实现

- 五端共享有界远控输入队列：合并连续鼠标移动；失焦、过载和本地队列积压达到 1 秒时丢弃待执行动作并释放按键。NGR2 批量消息在主机端先整批校验，再逐事件检查权限；拒绝 NGR1。此期限不包含输入已进入网络后的传输时间。
- 取消连接按正常断开处理；删除废弃的主机侧控制端续租分支；SendInput 成功后才变更按键跟踪状态。
- 站点 Origin 与连接 Candidates 分离。Windows 普通表单支持多个直达/VPS 入口、中继和配置版本；未修改的导入配置保持候选 ID、顺序和版本。ng 菜单 10 独立管理直达入口，修改 Origin 只调整原来绑定该身份的直达地址。
- LAN/VPS 同时支持 amd64 与 arm64 原生 Linux 构建、工具链摘要、架构化包名、安装下载和 ELF64 门禁。此处 ARM 指 arm64/aarch64，不含 armv7。
- 首次 Release 查询不存在的标签改用 Git ref 接口，避免 GitHub commit 查询的 HTTP 422；发布路径由回归覆盖，本轮不发布 Release。

## 本地验证

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| Windows 原生构建、H.264/输入/客户端/TLS 等 12 项 CTest | PASS，12/12 | native.log |
| 站点身份与备用拨号、带端口 SNI/Host、多 WAN 恢复、错误证书拒绝、时钟偏差和慢候选 | PASS | native.log |
| Python 工具回归 | 运行 60 项，58 通过，2 项平台相关跳过 | tools.log |
| Go internal/cmd 竞态检测 | PASS | go-race.log |
| Go vet | PASS，无诊断 | 本轮命令退出码 0 |
| 工作流 actionlint、修改脚本语法、git diff --check | PASS | 本轮命令退出码 0 |
| Windows 新表单可访问性结构 | 已读取新增字段 | profile-form-tree.txt；未访问真实服务 |

当前 Windows LTSC 无法提供 UI 截图接口（0x80004002），未把控件树检查算作视觉布局或保存点击验收。隔离表单测试进程已关闭。

## 云端验证与交付边界

九个目标全部完成云端构建和独立产物核验，见 CI.json：Windows amd64、Linux desktop amd64、Android arm64、macOS arm64、iOS arm64（设备及模拟器）、LAN amd64/arm64、VPS amd64/arm64。Windows 运行 45574059696 绑定 fbbc907，其他八项绑定 83abe51。此后管理员菜单和网页改动的验证单独记录在 ../2026-11-25-admin-ux/，不沿用这些构建结果代替后续代码验证。

LAN 两个架构均在 Debian 13 容器执行完整的 14 段验证，包括共享远控核心、前端、Go race/vet、证书、上游测试及原生编译、部署配置验证、release 清单和产物绑定。ARM 主机为 ubuntu-24.04-arm，Go 日志明确为 linux/arm64；VPS ARM 执行了安装归属、节点配置和上游 relay/TLS 测试。摘要日志见 native-arm-validation.txt。

所有九项均已独立下载并复核 Artifact、manifest、资产摘要和校验文件。Windows 核对 3 个 PE，Linux desktop 核对 3 个 ELF，Android 核对 8 个 ARM64 库，macOS 核对 3 个 ARM64 Mach-O，iOS 核对 4 个 ARM64 Mach-O 并分别检查设备与模拟器平台号。服务器产物另行复核外层 Artifact 摘要、release-manifest 中每个文件和 sidecar 摘要、内部 ZIP CRC/完整 SHA256SUMS、版本及 ELF64 machine。LAN 每个架构校验 9 个包内程序和 1 个独立安装器；VPS 每个架构校验 3 个包内程序和 1 个独立安装器。server-amd64-buildinfo.json 和 server-arm64-buildinfo.json 保留源代码指纹及所有构建输入摘要。

192.168.18.220 是私有 LAN 地址，公网客户端必须有可达的公网直达入口（路由器映射等）或已部署的 VPS 隧道。保留私有 Origin 只表示保留证书和站点身份，不代表该私有地址能被公网直接访问；填写 VPS 字段本身也不会创建隧道。

ARM 物理服务器部署、五端跨设备完整远控、真实 WAN/NAT 链路和 72 小时稳定性仍需相应环境验收。云端原生 ARM 构建与测试不替代上述实机验收。
