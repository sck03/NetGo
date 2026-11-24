# LAN 与 VPS 一键安装、配置和卸载

LAN 使用独立脚本 **install-server.sh**、快捷菜单 **ng**；VPS 使用 **install-vps.sh**、快捷菜单 **ngv**。程序安装和日常维护由 Go 实现，最终用户不需要 Python、Node 或 Go 编译器。当前支持 Debian 12/13、amd64、systemd，使用 API v2 / schema 2。

## 当前下载状态

截至 2026-11-24，仓库尚未发布 GitHub Release。`releases/latest/download` 的在线安装器下载因此不可用。**当前请下载成功构建的 Actions 平台运行包，运行包内的一键脚本可以安装并打开菜单。**

1. 打开 [Linux server](https://github.com/sck03/NetGo/actions/workflows/build-linux-server.yml) 或 [VPS auxiliary node](https://github.com/sck03/NetGo/actions/workflows/build-vps.yml) 最近一次成功运行。
2. 在底部下载 Artifacts，解压外层 ZIP。
3. 找到真正的程序安装包：LAN 为 `NetGo-0.3.0-dev-linux-amd64.zip`（通常在 `dist/linux-server/`）；VPS 为 `NetGo-0.3.0-dev-vps-linux-amd64.zip`。
4. 将对应运行包复制到目标机器。以下命令假设文件放在 `/root/`，在 root 终端运行；实际版本号按下载文件替换。

如果缺少 unzip：`apt-get update && apt-get install -y --no-upgrade unzip`。安装器按需准备所缺的运行依赖，不进行整机升级或自动清理系统包。

## LAN 服务端首次安装

```sh
unzip /root/NetGo-0.3.0-dev-linux-amd64.zip -d /root/NetGo-server
cd /root/NetGo-server
bash install-server.sh
```

菜单选择 **1) 首次配置/启动 VPN**，设置控制 IP/域名、端口、LAN 网卡/网段、证书和管理员账号。初次生成私有 CA，或导入已有证书后，维护器校验证书的域名/IP、有效期、完整链和私钥配对。

独立端口模式的默认值：

| LAN 项目 | 默认值 |
| --- | --- |
| HTTPS 控制及管理网页 | TCP 10443 |
| Relay | TCP/UDP 18443 |
| STUN | UDP 3478 |

例如：控制地址和本地 Relay 都为 LAN 服务器 `192.168.18.220`，网卡 `ens32`，网段 `192.168.18.0/23`。`255.255.254.0` 对应 `/23`。实际网卡和地址以目标机器为准。

完成后打开控制网址的首页，例如 `https://192.168.18.220:10443/`，登录管理网页创建账号、资源和授权。私有 CA 站点应先在管理浏览器中信任其公共 CA。账号默认没有资源权限；管理员需明确授予 LAN 或互联网资源。

输入 `ng` 打开菜单：

| 菜单 | 用途 |
| --- | --- |
| 1) 首次配置/启动 VPN | 初次初始化；已有站点恢复启动 |
| 2) 信息和状态 | 查看控制网址、配置位置、受管服务状态 |
| 3) 修改域名/IP 与端口 | 设置 LAN 控制与 Relay 地址、端口 |
| 4) 生成/导入证书 | 维护控制/Relay 证书 |
| 5) 导出客户端配置 | 导出公共站点 JSON |
| 6) 设置旁路由 IP/DNS | 设置账号授权的互联网出口 |
| 7) VPS 设置与导出 | 配置辅助节点、导出节点包、导入 VPS 新设置 |
| 8) 诊断/恢复连接 | 检查和恢复连接 |
| 9) 卸载（保留数据） | 卸载本项目受管对象 |

控制域名或 CA 变更后，客户端重新导入维护器导出的站点文件，并确认身份变更。修改配置会建立受保护备份，校验并启动新配置，失败时恢复原配置及服务；配置版本保持递增。

## VPS 安装和与 LAN 配对

只需一台 VPS。独立端口模式可用 IP 或域名：

| VPS 项目 | 默认值 | 作用 |
| --- | --- | --- |
| 控制入口 | TCP 24443 | 客户端到 LAN 的备用控制入口 |
| frps 隧道入口 | TCP 27000 | LAN 的 frpc 与 VPS 建立隧道 |
| Relay | TCP/UDP 28443 | VPN 直连失败时的数据中继 |
| STUN | UDP 3478 | VPN 地址发现 |

先完成 LAN 初始化，在 `ng → 7) VPS 设置与导出` 中选择添加/修改 VPS，填写以上地址和端口、选择证书，导出 `/root/NetGo-vps.zip`。将该节点配置包和 VPS 程序安装包分别复制到 VPS：

```sh
unzip /root/NetGo-0.3.0-dev-vps-linux-amd64.zip -d /root/NetGo-vps
cd /root/NetGo-vps
bash install-vps.sh
```

在 `ngv` 菜单选择 **1) 导入配置包/启动节点**，输入 `/root/NetGo-vps.zip`。也可在运行包目录使用：

```sh
bash install-vps.sh install --source /root/NetGo-vps --bundle /root/NetGo-vps.zip
```

VPS 的程序安装包 `NetGo-版本-vps-linux-amd64.zip` 用来安装程序；LAN 导出的 `NetGo-vps.zip` 含节点证书私钥及通信密钥，用来配对站点。不要把后者发给客户端用户或提交到 GitHub。客户端使用 `ng → 5` 导出的公共站点 JSON。

配对完成后，在 LAN 再次导出客户端配置。导出内容会保留 LAN 控制身份，并加入 VPS 备用 `Candidates`、`AuxiliaryRelay`。Windows 应导入此文件，不把“控制网址”或“本地中继”直接换成 VPS。

`ngv` 菜单提供配置导入、状态、端口修改、更新证书、诊断和卸载。VPS 修改端口会生成公开设置 JSON，将其导回 LAN 的 `ng → 7 → 导入 VPS 新设置`，再导出最新客户端配置。证书更新使用 LAN 新导出的节点 ZIP。

共享 443 模式需要三个不同 DNS 域名及空闲 443，并增加受管 HAProxy 和 QUIC 重定向。已有 SSH、Docker、80/443 服务不被接管；云安全组和现有防火墙需放行实际使用的 NetGo 端口。SSH 端口不属于 NetGo 客户端配置。

## 旁路由与账号授权

在 `ng → 6` 设置旁路由 IP、首选/备用 DNS、普通 DNS 或 Fake-IP DNS。示例：

| 项目 | 示例 |
| --- | --- |
| 旁路由 IP | 192.168.19.254 |
| 首选 DNS | 192.168.19.254 |
| 所在 LAN 网段 | 192.168.18.0/23 |
| Fake-IP 范围 | 198.18.0.0/15，支持 Clash/Mihomo |

服务器原有默认网关保留，互联网出口使用独立 VPN 入站策略。

**还需在管理页创建“互联网（旁路由）”资源并授权账号。** 客户端开关只选择已授权能力；撤权关闭租约转发并清理已有连接。LAN 设备和端口访问单独授权。旁路由 IP 不填到 Windows 的“路由虚拟 IP”或中继字段。

## 导出文件和 Windows 连接

在 `ng → 5) 导出客户端配置` 保存 `/root/NetGo-site.json`；服务端当前公共配置也在 `/etc/netgo/client-site.json`。把此 JSON 复制到客户端，点击“导入”，再输入账号密码连接。

它包括站点 ID、固定控制网址、直达和备用入口、VPN 网关、公共 CA、Relay 标识及互联网 DNS，不含密码、令牌或私钥。截图字段的逐项解释见 [Windows 客户端说明](WINDOWS_CLIENT.zh-CN.md)。

客户端也支持在“新建服务器”窗口手工填写，导入不是必须。手工配置需要这些相同的真实参数；VPS 备用入口在高级配置中填写。

Windows 安装版双击 Setup；绿色版解压后打开 NetGo.exe，首次网络授权由程序调用内部准备器。普通用户不用手工运行 PowerShell 维护脚本或分别启动后台程序。

## 卸载与保留数据重装

LAN 输入 `ng`、VPS 输入 `ngv`，选择菜单中的 9 并输入 yes；或者：

```sh
# 在 LAN 服务器
ng uninstall --yes

# 在 VPS
ngv uninstall --yes
```

卸载移除本项目受管服务、快捷命令和规则，保留程序、配置、证书、身份和数据。发现外部修改的受管文件时保留现场，不清理系统包、SSH、Docker 或未知服务。

卸载后快捷命令已经移除，重装从保留的运行包目录执行：

```sh
# LAN：恢复服务文件和快捷命令，再打开菜单选择 1
cd /root/NetGo-server
bash install-server.sh install --source /root/NetGo-server --install-only
bash install-server.sh

# VPS：恢复安装并加载保留的节点配置
cd /root/NetGo-vps
bash install-vps.sh install --source /root/NetGo-vps --bundle /etc/netgo-vps/bundle.zip
```

高级安装可指定本地包 `--package FILE --sha256 HASH`；此时需使用已取得的 Go 安装器或含安装程序的运行包入口。`--source DIR` 指向已解压的完整平台运行包；`--plan` 只显示计划，不下载、不安装。`--install-only` 只安装程序/受管入口，首次站点设置仍从菜单完成。

## 正式 Release 发布后的在线入口

仓库发布包含平台程序包、独立 Go 安装器、安装器摘要和根 SHA256SUMS 的正式 Release 后，root 可使用：

```sh
# LAN
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-server.sh -o install-server.sh && bash install-server.sh

# VPS
curl -fL https://raw.githubusercontent.com/sck03/NetGo/main/install-vps.sh -o install-vps.sh && bash install-vps.sh
```

脚本从 Release 下载并校验安装器，再由 Go 下载、校验和安装平台包。当前没有 Release，以上在线下载会失败；仅发布 prerelease 也不会提供 `releases/latest` 的正式版本入口。

## 验证范围

客户端不再要求本机与服务端时差小于 30 秒，使用可信服务端时间与单调经过时间；证书、签名、防重放、撤权及租约期限仍有效。

LAN/VPS 安装维护与部分 VPN 场景已实测；七个平台构建已通过。远程桌面完整跨设备、Windows UAC/锁屏/多屏、Apple/移动设备和长期运行验收尚未完成，见 [本轮报告](evidence/2026-11-24-platforms/REPORT.zh-CN.md)。

菜单体验参考 [sing-box-yg](https://github.com/yonggekkk/sing-box-yg/tree/b13f9435c4792fccf04d4fbc73e24a1948abff1b) 与 [sing-box-vpn](https://github.com/sck03/sing-box-vpn/tree/2a046930fdad1d49823f5dd75495b1c91e152802)，安装逻辑独立实现。开发构建入口见 [构建说明](BUILD_CLIENTS.zh-CN.md)。
