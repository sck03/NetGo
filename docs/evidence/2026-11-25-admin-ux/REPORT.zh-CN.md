# 管理员菜单与网页简化验证

日期：2026-11-25。实现提交：404e7cdeff5803c940d59eb4e6ec33cbe229e8d8。本轮接续远控与 ARM 工作之后的独立交互改动。产品比较与官方资料见 ../../PRODUCT_SIMPLIFICATION.zh-CN.md。

## 改动

- LAN/VPS 改用按任务分组的菜单；统一返回 / EOF 行为。LAN 首次配置自动发现 IPv4 网卡和网段，多网卡要求明确选择，随机生成站点 ID。
- VPS 推荐配置只收集一个公网地址并采用预设端口；编辑已有 VPS 时默认保留当前端口。高级设置继续支持独立端口和 443。
- 状态页区分私有 / 共享地址和待验证的公网候选，不把保存配置等同于网络已连通。
- 网页增加起步路径；资源使用模板并自动生成 ID；IP 自动按 /32，带主机位的网段拒绝静默扩大范围。
- 账号编辑采用独立草稿；权限改为复选框，限额 / 到期折叠。后台自动刷新不替换草稿；策略保留已有后台自动应用。
- 交互分别位于 node 导航 / 向导模块、ResourcePanel、UserPanel；共享资源规范化位于 packages/ui/resources.ts。

## 本地结果

| 检查 | 结果 |
| --- | --- |
| npm test | PASS，13 项；包括单机 /32、无效地址 / 端口、网段不扩大和互联网资源处理 |
| npm run build | PASS，vue-tsc 与 Vite |
| go test -race ./internal/node ./internal/site ./internal/deployment ./internal/api | PASS；deployment 无独立测试文件，由 node/site 集成覆盖 |
| 新菜单回归 | PASS，单 / 多网卡、返回和 EOF、独立站点 ID、VPS 默认 / 保留自定义端口、内网入口判断 |
| go vet（上述 Go 包）、actionlint、git diff --check | PASS |

## 交付与边界

四个服务器目标均已完成原生构建、测试和独立下载核验，详情见 CI.json：

| 目标 | 运行编号 | 独立产物检查 |
| --- | --- | --- |
| LAN amd64 | 45585754532 | PASS，8 项资产，10 个 ELF |
| LAN arm64 | 45585756608 | PASS，8 项资产，10 个 ELF |
| VPS amd64 | 45585754399 | PASS，6 项资产，4 个 ELF |
| VPS arm64 | 45585764761 | PASS，6 项资产，4 个 ELF |

已检查 GitHub 外层摘要、release-manifest 的提交号与不发布设置、全部资产大小 / SHA256、校验 sidecar、BUILDINFO 架构与版本、内部 ZIP CRC / SHA256SUMS 及 ELF64 machine。LAN 两架构都通过包含前端、Go race/vet、远控核心与部署校验的 14 段验证。native-arm-validation.txt 保留 ARM 原生日志和原始日志包摘要。

同次 push 触发的 Linux desktop（45585754664）、Android（45585754401）、macOS（45585754491）、iOS（45585754503）也全部构建成功；CI.json 保留状态和运行步骤，本轮没有重复下载这些未改客户端界面的产物。Windows 源码未变，最近独立产物验证见 ../2026-11-25-remote-arm/。

已确认仓库当前仍无 Release。成功构建和产物检查不代表真实设备或完整远控验收。

已尝试启动隔离的模拟数据页面，但 Codex Browser 插件报告缺少 required network policy，未能打开浏览器测试页。不得把前端构建、单元回归或源码审阅写成已完成实际点击、布局或真实服务器验收。

未部署真实服务，未发布 Release。首次 Linux 安装仍需终端启动安装脚本，日常使用 ng/ngv 菜单和管理网页；VPS 配对仍需复制节点包，云防火墙 / 路由器映射不由这些菜单自动管理。邀请注册、一键跨主机配对和完整网页部署不是已交付功能。

已在保留并逐份核对 CI.json 后清理两轮重复下载包（约 622 MiB）、临时回执、日志包、采集脚本和模拟页面；原始日志的必要摘录、摘要、正式证据和可复用工具链保留。
