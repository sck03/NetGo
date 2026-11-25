# 独立平台构建、时钟修复与节点实机验证

> 历史提交记录；原始日志、截图与测试数据已从当前目录清理。可在 Git 提交 `9296955` 的相同路径查看，不能作为本轮修改的验证结果。

本轮对应0.3.0-dev / API v2 / schema 2。五个客户端、LAN服务端和VPS分别拥有构建脚本和GitHub工作流。LAN/VPS安装维护统一Go，最终用户不需要Python；Apple客户端已实现并继续推进设备验收。

## 已执行验证

| 范围 | 结果与限制 |
| --- | --- |
| Windows | 单NetGo.exe，7,055,360字节（约6.73MiB）；8项CTest、TLS/候选/账号凭据回归、系统DLL白名单通过。Setup/绿色版只有一个日常入口，内部网络组件由程序调用。 |
| 时钟 | 取消30秒客户端时差阻断，可信TLS服务端时间配合单调经过时间。Go ±24小时、Windows/前端 ±1小时回归通过；登录后更改本机日期也不能延长会话。证书有效期、签名、nonce、防重放及租约仍检查。 |
| Go/前端 | 当前工作树归档到干净Debian13目录，先构建前端再执行Go测试/vet。全部通过；前端10项测试通过。Windows本机Go测试/vet和46项开发工具测试通过（1项POSIX跳过）。 |
| Linux GTK | 原生GTK/FFmpeg构建、6项CTest通过，已有Xvfb启动/退出验证。运行包只含3个程序、desktop入口、摘要与许可证。 |
| Linux网络安装 | 实机root安装、UID1000专属0600 IPC、其他用户隔离、控制socket capability通过；更新故障注入恢复旧程序/归属记录/能力/服务；卸载、重复卸载、保留状态重装和最终卸载通过。 |
| LAN服务端 | 新schema2站点/CA初始化、真实IdP与28项TLS/API验证通过；Go菜单、证书/端口/旁路由/VPS配置已实测。受管卸载、重复卸载、保留配置/证书/数据重装、服务启动和开机启用复验通过。 |
| VPS | frps和Relay仍是必要组件，分别提供备用控制隧道及VPN中继。Go节点包导入、doctor、受管卸载/重复卸载、保留证书/身份重装及服务启动通过；保留SSH、Docker和默认路由。 |
| 旁路由/账号 | VPS隔离namespace中的真实Go客户端经公网辅助Relay连接LAN；授权账号通过192.168.19.254 DNS获得198.18.11.7，Fake-IP HTTPS、旁路由公网出口和授权LAN HTTPS通过。未授权账号启用互联网返回internet_forbidden；关闭该选项后授权LAN HTTPS通过。撤销互联网授权后TCP请求被阻断。 |
| Android/Apple | 已有Android VPN AAR/JNI/Kotlin/APK/lint、macOS App、iOS模拟器和未签名设备构建成功记录。缺设备与有效Apple签名，不能标为实机通过。 |

## 本轮修复

- 客户端从可信服务端取时间，使用单调经过时间进行签名、刷新和期限判断。
- 设备种子和WireGuard profile按站点及规范化账号隔离，修复同站点切账号复用VPN身份导致的registered peer not found；保留服务端所有权校验。Go签名种子统一为32字节，远控使用当前连接的账号身份。
- Linux/mobile/Windows控制socket避开VPN全局路由；macOS使用daemon维护的地址排除路由，修复互联网模式的控制回路。
- 私有CA备用隧道读取trust.pem；身份/路由节点启动增加有期限的就绪等待；失败证书输入可重选，不静默替换CA。
- 服务器本机LAN资源生成独立输入策略，互联网权限不额外授予本机LAN权限；server单元允许读取网卡所需AF_NETLINK。
- Linux桌面网络模块与LAN/VPS维护分离，修复IPC改属后chmod失败、更新回滚和卸载重试；LAN重装后成功启动恢复开机启用。
- GTK导入回调编译错误已修复；Linux包采用明确文件白名单。共享Go模块变化触发各相关客户端工作流。

## 仓库与交付边界

七个平台工作流使用已查询的稳定版及固定SHA。本轮七个平台的GitHub构建已全部通过。实现提交为d5299df；Linux依赖下载缓慢的首次运行已取消，加入非交互/IPv4/网络重试和超时后，3a30659的Linux客户端构建通过。两个提交之间仅工作流和文档变化，程序源码相同。

| 工作流 | 本轮通过记录 |
| --- | --- |
| Windows client | [45333101462](https://github.com/sck03/NetGo/actions/runs/45333101462) |
| Linux desktop client | [45334529838](https://github.com/sck03/NetGo/actions/runs/45334529838) |
| Android client | [45333101392](https://github.com/sck03/NetGo/actions/runs/45333101392) |
| macOS client | [45333101389](https://github.com/sck03/NetGo/actions/runs/45333101389) |
| iOS client | [45333101337](https://github.com/sck03/NetGo/actions/runs/45333101337) |
| Linux server | [45333101320](https://github.com/sck03/NetGo/actions/runs/45333101320) |
| VPS auxiliary node | [45333101403](https://github.com/sck03/NetGo/actions/runs/45333101403) |

Go连接/控制/节点竞态检查及隔离网络namespace内的内核路由归属/恢复测试通过。Linux首次网络服务启动后故障注入，保留动态生成的配置和归属记录，随后重试安装及重复卸载通过。撤销凭据后客户端以unauthorized结束连接，避免持续显示重连。

七份Actions产物均已下载并核对API提供的SHA-256、归档完整性和运行文件清单，包内SHA256SUMS逐项匹配。Windows绿色包根目录仅一个NetGo.exe，内部网络准备器位于.netgo；Linux桌面包8个文件，LAN包24个文件，VPS包10个文件，macOS包12个文件，iOS两份运行包各9个文件。Android APK仅保留arm64所需原生库，开发报告位于APK之外。运行包未发现Python脚本、私钥、数据库、源码对象或缓存。构建提交、运行链接与产物摘要见 [CI回执](https://github.com/sck03/NetGo/blob/92969551d3989395bcf2556702649ed9dc9c8e3b/docs/evidence/2026-11-24-platforms/CI.json)。

当前Git跟踪/待提交路径检查未发现缓存、工具链、node_modules、运行数据库、EXE/APK/AAR/IPA、ZIP或私钥；必要Gradle wrapper JAR保留。运行包只包含角色所需程序和许可证；LAN使用frpc，不携带VPS专用frps。开发构建/测试可用Python，终端用户维护使用Go。

原始失败记录、机器配置、SSH凭据、测试密码、证书私钥和受保护备份保存在忽略目录及测试机，未加入Git。旧开发部署已归档保留；测试namespace及带专用注释的规则按归属清理。

完整跨设备远控音视频/输入、Windows UAC/锁屏/多屏、剪贴板/文件、Linux主机/Wayland、Android/Apple实机和72小时尚未完成。当前是开发交付，不能标注为五平台全部实机验收或生产发行完成。
