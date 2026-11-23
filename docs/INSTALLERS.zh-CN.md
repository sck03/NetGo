# 独立安装、卸载与维护

2026-11-24：局域网 Linux 服务端使用 `install-server.sh`，VPS 辅助节点使用 `install-vps.sh`，Windows 客户端使用 Setup 或 `install-client.ps1`。各入口独立，构建脚本和 GitHub 工作流也分别维护。源码版本为 0.3.0-dev、API v2、SQLite schema 2；旧开发数据库需先归档，不能直接覆盖或作为 schema 2 使用。

Linux/VPS 支持 Debian 12/13 amd64 和 systemd。默认查询 GitHub 最新正式 Release，下载包和 SHA256SUMS 后校验；尚无对应发布包时解析 main 为一个确定的 commit，再准备锁定工具链并构建。下载或构建失败会退出，不报告安装成功。`--package FILE --sha256 DIGEST` 用于已审阅的离线包，`--source DIR` 用于本地完整已构建目录。

## 局域网 Linux 服务端

以 root 执行：

```sh
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh -o install-server.sh
bash install-server.sh install --plan
bash install-server.sh install
```

直接运行 `bash install-server.sh` 打开维护菜单。也支持 `bash <(curl -fsSL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh) install`，非交互调用需显式提供首次配置参数。源码方式可使用 `--from-source --ref 完整commit`，构建缓存保存在 `/var/cache/netgo`，不替换系统 Go/Node。

安装后提供本机维护入口：

```sh
netgo-serverctl status
netgo-serverctl doctor
netgo-serverctl configure
netgo-serverctl uninstall --yes
```

首次配置输入控制/Relay 域名或 IP、LAN 接口和网段、管理员邮箱及密码。密码从终端或 `--password-stdin` 读取，不接受命令行密码参数。无人值守示例：

```sh
bash install-server.sh install --source /root/NetGo \
  --config /root/site-input.json --tls-dir /root/netgo-certificates \
  --admin-email admin@example.com --admin-alias admin --password-stdin
```

TLS 目录需要 `control.crt/key`、`relay.crt/key`，私有 CA 场景另含 `ca.crt`。证书必须匹配域名/IP、有效期和私钥。实验室可显式选择 `--private-ca`；生产域名可提供 DNS-01 等方式取得的证书。CA 私钥留在 LAN。默认高位端口模式无需入站 80/443；安装前检查端口，不停止已有服务。

初始化使用 NetBird 官方 API 创建唯一管理员、保存受保护管理令牌、关闭默认全员互通策略、配置 overlay 和专用路由节点。普通用户以账号密码登录，管理员再授予 LAN 资源与远控权限。`--install-only` 只安装程序。配置失败保留阶段记录与私有日志，检查后使用 `configure --resume`。

同版本必须文件完全匹配才能复用；不同内容不覆盖活跃安装，升级/回滚通过 `netgoctl` 运维流程执行。卸载核对归属清单，断开受管路由节点，移除受管服务、命令链接和 NetGo 表，并恢复由本次初始化改变的转发值。保留身份、数据库、证书、备份、账号和已校验发布文件；不运行 apt autoremove，不改 Docker/SSH。

## VPS 辅助节点

一台 VPS 同时运行 frps（备用控制路径）和 NetBird Relay/STUN（业务中继）。控制数据库、管理员令牌、设备私钥及 LAN 权限服务不放到 VPS。多个 SNI 域名可以指向同一 VPS。

先在 LAN 配置辅助节点，并渲染配置：

```sh
python3 -B scripts/render-deploy.py --config /root/site-with-vps.json \
  --secrets /etc/netgo/rendered/secrets.json --output /root/netgo-vps-rendered
python3 -B scripts/export_vps.py --rendered /root/netgo-vps-rendered \
  --tls-dir /root/vps-certificates --output /root/netgo-vps-bundle
```

VPS TLS 目录需要 `relay.crt/key` 和 `tunnel.crt/key`，证书分别匹配 RelayHost 和 TunnelHost。私有 CA 场景另提供 `ca.crt`，仅为受管 Relay 的健康检查增加信任。导出器仅复制 auxiliary 配置和这些 TLS 文件，生成 VPS 专用摘要清单；不复制 `local/`、`secrets.json`、`native.secret`、控制 TLS 或 `ca.key`。用已授权的 SSH/SCP 把导出目录传到 VPS，并保持目录 0700、敏感文件 0600。

在 VPS 上以 root 执行：

```sh
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-vps.sh -o install-vps.sh
bash install-vps.sh install --plan
bash install-vps.sh install --config-dir /root/netgo-vps-bundle
netgo-vpsctl status
netgo-vpsctl doctor
netgo-vpsctl uninstall --yes
```

可使用 `--install-only` 稍后运行 `netgo-vpsctl configure --config-dir DIR`，或 `--from-source` / `--source` / `--package` 选择来源。VPS 程序在 `/opt/netgo-vps`，配置在 `/etc/netgo-vps`，服务名统一 `netgo-vps-*`，与 LAN 安装互相独立。

高位端口模式只需 frps 和 Relay。443 模式增加独立 HAProxy 服务及受管 QUIC 重定向表 `netgo_vps_relay`；HAProxy 来自 Debian 稳定软件源。控制、Relay、隧道的 TCP SNI 和端口由 LAN 配置确定。脚本不会接管 VPS 现有 80/443 监听器，也不自动修改云安全组或系统过滤策略；按 `vps.json` 开放所需公网端口，内部健康/metrics 端口保持回环绑定。

激活前检查配置摘要、TLS、端口以及 frps/HAProxy/nft 语法；逐个确认 systemd 状态、监听器和 Relay 健康接口。启动失败停止本次受管服务，保留配置及受保护诊断，退出失败状态。卸载先校验程序、配置、服务文件和维护链接，遇到外部修改拒绝删除；重复卸载无副作用。默认保留证书和配置，便于审阅后重装。

## Windows 客户端

普通使用只启动 **NetGo.exe**，主界面、后台与远控通过同一文件的内部模式运行。Setup 是安装入口；绿色版 `.netgo/NetGo-Network-Setup.exe` 是首次连接时由客户端调用的网络准备工具，用户无需分别运行。NetBird 系统服务和 Wintun 负责受限网络功能。

安装版在预期桌面用户的管理员 PowerShell 中运行 Setup 或维护脚本：

```powershell
.\install-client.ps1 -Action Install -PackageRoot C:\NetGo包目录
.\install-client.ps1 -Action Status
.\install-client.ps1 -Action Uninstall -Yes
```

本机卸载工具保存在 Program Files 的 maintenance 目录。程序校验包摘要和 Wintun 签名，绑定桌面用户 SID，日常不需管理员权限。默认保留站点及安全凭据；站点/密码删除使用明确的客户端操作。NetGo 当前程序没有付费代码签名，不要求关闭系统安全策略。

## 验证范围与参考

构建、包自检和模拟测试不替代跨设备 VPN/远控、锁屏/UAC、多屏、Linux Wayland、移动设备或 72 小时验收。macOS/iOS 按当前要求暂缓，保留共享 C ABI、平台回调和设备安全存储扩展接口。

安装器参考 [sing-box-vpn](https://github.com/sck03/sing-box-vpn/tree/2a046930fdad1d49823f5dd75495b1c91e152802) 的独立入口、归属检查和失败诊断设计，代码独立实现；该参考项目使用 GPL-3.0。具体构建入口见 [独立构建说明](BUILD_CLIENTS.zh-CN.md)。
