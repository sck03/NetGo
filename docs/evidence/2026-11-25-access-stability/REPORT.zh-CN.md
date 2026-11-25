# 远控失联边界、资源范围与界面缩放

> 历史提交记录；原始日志、截图与测试数据已从当前目录清理。可在 Git 提交 `9296955` 的相同路径查看，不能作为本轮修改的验证结果。

日期：2026-11-25（Asia/Shanghai）。接续会话“优化远程桌面与旁路由局域网访问”，基线 bb3a2bc。继续使用 API v2 / schema 2，无旧数据或旧接口兼容分支。本轮没有替换已安装服务器程序或发布 Release。

## 实现

- 提取共享 Watchdog，Windows 主机与五平台共享查看器统一采用包含系统休眠时间的单调时钟。六秒失联期限一旦过期即终止；迟到响应不能恢复媒体或再触发续租。关闭与刷新串行化，防止并发恢复。保留前轮将请求耗时计入轮询周期的修复。
- Windows 远控窗口按显示器 DPI 缩放中文字体、工具栏、状态行、初始及最小窗口与画面区域。刷新后按主机 ID 保留选择；刷新失败清空旧列表，禁止根据旧状态连接。
- 新增只读网络配置摘要，只输出管理员所需的 LAN 网卡、网段、旁路由网关、DNS 和 Fake-IP 模式，不序列化完整服务器配置。资源 API 与前端要求启用的资源位于配置的 LAN 网段内，且启用互联网资源前必须配置旁路由；无效编辑不会撤销现有资源及租约。允许在网段调整后停用旧资源。
- 管理页展示 LAN / 旁路由配置卡和资源地址范围，区别配置与连通检测结果。修复小于 700px 时整条导航被隐藏、无法切换页面的问题，改为顶部横向导航；概览的 LAN 资源数不再混入互联网出口。
- 修正 README 中仍声称只展示可连接主机的过时说明；补充隔离网络实验须把测试网段加入实验站点的条件。

## 回归与本地验证

新增共享计时器确定性测试覆盖六秒边界、迟到响应、休眠、时钟倒退、溢出和并发关闭。共享查看器增加第 19 个场景：画面回调暂停八秒，控制查询七秒返回。旧代码实际失败，触发额外续租；修复后不再续租，窗口媒体会话结束。Windows 13 项 CTest 通过，包含 19 场景共享查看器和新 Watchdog 测试；TLS 夹具通过。前端 19 项测试及 vue-tsc / Vite 构建通过，Windows 相关八个 Go 包 race 和全包 vet 通过。

基线对照第一次恢复源码时 Copy-Item 保留旧时间戳，增量构建复用了基线对象，导致预期失败再次出现；刷新源码时间戳并确认重编译后全部通过，保留前后日志。新增 API 测试首次缺少租约关联的账号/设备而触发 SQLite 外键错误，补齐测试数据后通过。测试命令两次选到无 paramiko/cryptography 的 MSYS Python，改为已有 D:/Python/Python313/python.exe 后通过，没有新增或修改依赖。

Debian 实机：全 internal/cmd Go race、全包 vet、GTK 构建和 8 项 CTest 通过；隔离 netns 中真实 nftables 租约续期/撤权和旁路由规则归属/恢复通过。后续测试增量另行同步复验并核对最终代码指纹。首次临时维护器命令多写了 server 子命令；netgo-serverctl 已按可执行文件名选择角色，改用 netgo-serverctl check-network 后通过。

## LAN / VPS 观察

本轮维护器在 LAN 验证 ens32、192.168.18.0/23、IPv4 转发和受管旁路由规则；DNS 返回 Fake-IP，样本 3ms。逐次新建 TCP/TLS 连接、不自动重试：LAN 直达 6 次、LAN 经 VPS 备用入口 6 次、VPS 经自身备用入口 6 次健康查询全部通过；保留原始站点 SNI/Host、CA 和协议/站点校验。旁路由 DNS 另采样 8 次全部通过。

直达查询 47–48ms；LAN 经 VPS 入口 1047–2512ms；VPS 经自身入口 549–1284ms。逐次 TCP、TLS 和请求总耗时保存在 NETWORK.json。最终本地与 Debian 源码的 440 项输入指纹相同，最终增量的 8 项 CTest 和 Go 校验通过。

云包核对补记：Git 提交的代码指纹为 56aeaee01d158a13564d95052d9cd005bcf0f51b95832c468e9d5b50f4cd2bba，与两个 LAN 构建信息完全一致；本地/实机测试目录为 8333e1cb49b8c0b540aea28f00784bfa26981f16ebf2f1b91c9eb5d39336eecf。逐文件对比 git archive 确认唯一差异为 tests/contracts/remote/possession.txt 的 5 个 CRLF/LF 换行字节，内容相同。没有把两个不同的字节指纹写成相同；SOURCE-COMMIT.json 保留完整差异和包内源码绑定结果。

这些是配置、内核和分段网络检查，本轮没有把健康查询当作完整 VPN 业务访问。前轮首次 DNS 超时 / TLS EOF 仍缺少失败时的完整路径证据，未关闭根因，也未增加掩盖错误的自动重试。

## 界面验收边界

新 Windows 程序已启动并读取到中文控件、刷新失败提示和连接/全屏按钮的正确禁用状态。截图两次失败，报 capture factory: 不支持此接口 (0x80004002)；点击失败报 coordinate input geometry is unavailable，随后关闭本轮测试进程。浏览器插件仍返回缺少 network policy，不能进行网页点击。DPI/多屏、实际视觉效果、跨设备画面、键鼠/声音、锁屏/UAC、物理 ARM 和长期稳定性未由本轮证明。

## 设计参考

通过官方 HTTPS 正文核对 Tailscale Subnet routers 与 Exit nodes 对指定网络和互联网出口的区分，用于资源配置摘要和单独授权说明。AnyDesk Display 文档读取遇到证书过期错误，未绕过验证，也不将其列为本轮新核实的依据。

- https://tailscale.com/docs/features/subnet-routers
- https://tailscale.com/kb/1103/exit-nodes

## 交付与清理

功能提交 49744ef 已推送 main。Windows、Linux、Android、macOS、iOS 客户端和 LAN/VPS 的 amd64、arm64 共九项云构建全部成功，无重跑。Windows 和四个服务器运行包的下载摘要、包内清单与摘要、ZIP 路径及 CRC、PE/ELF 架构、源码提交和 publish skipped 均独立校验通过。五个包的 BUILDINFO 也已核对源码绑定；CI.json、ARTIFACTS.json、SOURCE-COMMIT.json 保留记录。后续提交只归档文档与证据，不改变经过验证的产品源码。

原有 LAN/VPS 服务和默认网关保持。仅使用临时源码目录、隔离 netns 和只读网络探测；没有创建管理员设备、用户或授权资源。

LAN 本轮临时源码、程序、构建及两个归档共删除 58,544,500 字节；VPS 三个探测文件共删除 4,616 字节。LAN 服务、VPS 服务及 SSH 正常，默认网关不变，LAN 已安装程序摘要与安装清单一致。清理回执保存在 CLEANUP.json。

本地清理受自动审批阻止：前轮 .cache/remote-lan-20261125 的递归删除返回 blocked by policy；本轮证据归档后，对 .cache/continuation-access-20261125 中文件按限定目录逐项删除的操作也被拒绝，原因为 blocked by policy。两次均在命令执行前被拒绝，没有实际删除，也未改用其他方法绕过。只读复核显示前轮 105 个文件、363,407,071 字节（约 346.57 MiB）；本轮 92 个文件、312,875,696 字节（约 298.38 MiB），没有重解析点。两目录均被 Git 忽略。本轮缓存仅含源码、测试记录、公共站点配置和下载包，没有新增账号/设备凭据；服务器临时文件已清理。
