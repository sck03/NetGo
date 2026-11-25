# 远控功能交付记录（2026-11-26）

基线：675262fc020bf3c83c2eb176d696094fd080cc3f。此记录只覆盖基线之后新增的远控能力，上一轮重构的构建记录不能替代本轮验证。当前代码与使用方式见 [远控功能说明](REMOTE_HOST_FEATURES.zh-CN.md)。

## 实现与修复

- 提取统一被控执行器，管理登记、每次启动的 TLS 传输密钥、持钥证明、本地确认/无人值守、会话消费、心跳、续期和取消。Windows/macOS/Linux 仅保留系统适配职责。
- Windows 增加 D3D11 GPU NV12 与异步 Media Foundation 硬件 H.264，保留明确的软件回退；限制在途帧，保护共享 D3D 设备，等待首帧输入就绪，调整音视频轮询避免持续音频挤占画面。
- macOS 增加 ScreenCaptureKit / VideoToolbox / CGEvent 主机；Linux 增加 X11/XTest 与 Wayland portal/PipeWire/GStreamer 主机，按门户实际批准的设备发布输入权限。
- 五个平台控制端接入文本剪贴板与文件操作。共享传输模块统一目录/文件名/链接边界、分块、哈希、无覆盖发布、配额、取消与超时。用户明确选择的本地保存目录不套用主机接收配额。
- 修复取消后的最后一块下载发布、排队取消无完成通知、早期握手失败未清空队列、跨会话迟到事件、Android 暂存文件归属及 Apple 安全范围 URL 释放、Linux 停止后立即重开竞态和特殊文件阻塞。
- 无人值守为显式本地选择；Windows Credential Manager、macOS Keychain、Linux 受保护 vault；登录启动后重新认证 VPN 并恢复共享，主动停止/断开暂停自动恢复。Wayland 持久授权遵循系统门户并轮换恢复令牌。
- 管理页面保留六项独立授权，连接中状态文案适配无人值守；更新接口、构建、平台和第三方说明。研究来源与独立实现范围见 [ADR 0006](adr/0006-remote-host-and-exchange.md)。

## 本地自动验证

| 验证 | 结果 |
| --- | --- |
| Go 全量 go test -race ./... | 通过，包括新增无人值守和传输密钥测试 |
| Go 全量 go vet ./... | 通过 |
| Windows 原生构建 / CTest | 16/16；包含真实回环 TLS 的共享 host/client 集成、文件边界与取消测试 |
| Windows GPU 合成画面 | RX 580 2048SP、驱动 31.0.14051.5006；GPU NV12 → 硬件 H.264 → 解码 40/40 帧、500,016 字节；[命令与输出](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-26-remote-host/gpu-probe.txt) |
| 管理页面 npm test | 21/21 |
| 管理页面 npm run build | 通过 |
| Python 工具测试 | 60 项运行，59 通过、1 项 Linux 符号链接语义在 Windows 跳过 |
| Android 本地 Debug / Release / lint | 通过；最终 JNI 消息边界由对应云构建复验 |

## GitHub main 构建验证

以下构建均验证代码提交 `5bbd26dd4484aec166cbe0d41f9c057835deec0a`，发布开关关闭。最终文档提交仅补充验证证据；未启动部署或创建 Release。时间按北京时间 2026-11-26 记录，GitHub 对应 UTC 2026-11-25。七项均已完成并通过；[API 证据](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-26-remote-host/github-builds.json) 保存运行编号、代码提交、任务结果和产物摘要。产物摘要由 GitHub 提供，未在本次最终核验中下载全部压缩包重新计算。

| 目标 | 构建记录 | 结果 |
| --- | --- | --- |
| Linux 桌面 amd64 | [45671753198](https://github.com/sck03/NetGo/actions/runs/45671753198) | 通过；11/11 原生测试、Go 客户端测试与 Xvfb 生命周期 smoke |
| VPS 辅助节点 amd64 | [45671752958](https://github.com/sck03/NetGo/actions/runs/45671752958) | 通过 |
| Android arm64 | [45671752842](https://github.com/sck03/NetGo/actions/runs/45671752842) | 通过 |
| macOS arm64 | [45671752884](https://github.com/sck03/NetGo/actions/runs/45671752884) | 通过 |
| iOS arm64 设备与模拟器 | [45671752860](https://github.com/sck03/NetGo/actions/runs/45671752860) | 通过；设备与模拟器分别 BUILD SUCCEEDED；未执行模拟器 |
| Windows amd64 | [45671753029](https://github.com/sck03/NetGo/actions/runs/45671753029) | 通过；16/16 原生测试、TLS 集成检查、Setup 与便携包 |
| Linux 服务端 amd64 | [45671752862](https://github.com/sck03/NetGo/actions/runs/45671752862) | 通过；服务端 verification PASS |

本轮初次 Linux 构建因 GTK 已弃用接口在 -Werror 下失败，已改用可取消 GtkAlertDialog 并复验通过。iOS 曾因 arm64 执行器容量不足未获取执行器，改用 Intel 主机交叉构建 arm64 设备与模拟器；此工作流不启动模拟器。Android 有一次插件仓库解析失败，未修改锁定版本规避，最终 main 构建已通过。

## 边界与清理

未启动真实桌面采集，未部署 LAN/VPS，未创建 Release；不将编译和注入画面的协议测试计为真实 GPU 性能、跨设备交互或长期稳定性通过。iOS/Android 当前是控制端，Linux/macOS 主机音频未登记；登录前桌面、UAC/安全桌面、多显示器切换、自适应码率仍不在本轮已完成范围。Linux 硬件编码不等于端到端零复制。

本轮已把 6 项临时内容（一次性格式化工具、官方门户参考缓存、两份失败构建日志及 GPU 探针暂存源码/程序，共 8,322,014 字节，约 7.94 MiB）归入 .cache/manual-delete-20261125/remote-features-20261126，并保存 manifest.json。与上一轮 21 项待手动删除内容集中在同一父文件夹，可一起手动删除。运行身份、服务状态、必要构建缓存和用户数据保持保留。功能代码已合并并推送 main：5bbd26dd4484aec166cbe0d41f9c057835deec0a；临时分支已在本地与远端删除。对应 main 构建结果见上表。
