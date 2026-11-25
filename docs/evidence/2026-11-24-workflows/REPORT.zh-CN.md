# 独立构建参数与 Releases 发布验证

> 历史提交记录；原始日志、截图与测试数据已从当前目录清理。可在 Git 提交 `9296955` 的相同路径查看，不能作为本轮修改的验证结果。

日期：2026-11-24，Asia/Shanghai。本轮只构建与验证，所有云端测试均为 publish=false，没有创建 GitHub Release。

## 交付行为

七份平台工作流继续独立。Run workflow 提供版本、当前平台支持的架构、发布开关及 draft/prerelease/stable 类型；Android 另有 versionCode，Apple 另有构建号，iOS 可分别选择设备和模拟器。

版本由 VERSION 或手动输入统一传入程序、Windows 文件属性、Android/Apple 包信息、包名及 BUILDINFO。Apple 标准版本使用数字部分，NetGoBuildVersion 保留包含预发布后缀的完整版本。API/schema 版本分别管理。

自动 push 不发布。只有手动选择 publish=true 且构建成功，发布任务才获得 contents:write。运行包使用平台白名单，逐资产生成 .sha256；发布暂存清单、开发日志和原始 App 目录不上传 Releases。同版本、同提交可分别追加各平台产物；拒绝混用提交及覆盖不同内容的同名资产。

真实 API 核验修复了两处发布问题：构建产物 ZIP 下载需要 GitHub JSON 媒体类型，使用 Release 资产的 octet-stream 类型会返回 415；草稿无法通过 published-only 标签接口发现，现使用有权限的 Releases 列表查找，并支持分页和并发创建后的复核。Apple 核验发现自定义生成 plist 设置未写入完整版本，已改为显式 plist 输入。

## 已执行检查

- actionlint：七份工作流通过；Bash/Python 语法检查通过。
- 56 项开发工具测试无失败。Windows 跳过 Linux 符号链接检查；因 Go 未加入 PATH 跳过的模块替换检查另行补跑，4 项上游输入测试全部通过。
- Go internal/cmd 测试和 vet 通过；Windows 8 项 CTest、TLS 夹具、系统 DLL 与体积检查通过。
- Windows 使用 0.3.1-dev 构建，--version、FileVersion、ProductVersion 一致。
- 手动 VPS 0.3.1-rc.1 / amd64 / publish=false 构建通过；在 Debian 运行云端二进制，version 命令返回同版本、架构及 vps 角色。临时测试二进制已移除。
- GitHub 构建产物 API 摘要、ZIP CRC、发布清单、逐文件 sidecar、运行归档和版本字段实际下载核验；具体提交、资产摘要和最终复验结果见 CI.json。

## 最终复验

版本参数实现提交 529435a 的首轮七份默认构建均成功。发布修复提交 52770d6 的七平台复跑也全部成功。943b990 仅修改 Apple plist 生成，随后 macOS ARM/Intel 和 iOS 设备/模拟器复验通过。下表采用各平台最后一份相关实现的成功记录，publish 均 skipped；各包源码提交明确记录，不能混用于同一个正式 Release。

| 平台 | 成功运行 |
| --- | --- |
| Windows | [45357490282](https://github.com/sck03/NetGo/actions/runs/45357490282) |
| Linux 桌面 | [45357490289](https://github.com/sck03/NetGo/actions/runs/45357490289) |
| Debian 服务端 | [45357490306](https://github.com/sck03/NetGo/actions/runs/45357490306) |
| VPS | [45357490367](https://github.com/sck03/NetGo/actions/runs/45357490367) |
| Android | [45357490459](https://github.com/sck03/NetGo/actions/runs/45357490459) |
| macOS ARM | [45358901070](https://github.com/sck03/NetGo/actions/runs/45358901070) |
| iOS 设备和模拟器 | [45358901175](https://github.com/sck03/NetGo/actions/runs/45358901175) |

自定义 VPS 0.3.1-rc.1：[45355844088](https://github.com/sck03/NetGo/actions/runs/45355844088)。macOS Intel 0.3.1-dev、构建号 2：[45359010505](https://github.com/sck03/NetGo/actions/runs/45359010505)，Mach-O 架构、标准版本、完整版本和构建号均已核验。Windows 云端 EXE 的 --version、FileVersion 和 ProductVersion 一致。

九份发布暂存产物实际下载核对摘要和 CRC，运行归档、版本与主程序架构检查通过。运行包未发现开发缓存、源码对象、Python 脚本、私钥或数据库。完整资产摘要、架构与 Apple plist 核验结果见 [CI 回执](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-24-workflows/CI.json)。工作流源码和回归检查已通过，但此次始终未执行真实 Release 写入。

## 验证范围

此次没有向真实 Releases 上传文件；写入流程的创建、并发、重试、错提交与同名资产保护通过回归测试，下载与摘要使用真实 GitHub API 验证。草稿、预发布不会开启 releases/latest；LAN/VPS 在线安装需要两个独立工作流发布到同一正式版本。

Android/Apple 签名、实机 VPN/远控与跨设备画面输入、Windows UAC/锁屏/多屏及长期运行继续以项目已有进度记录为准。构建成功不等于上述功能全部实机验收。
