# 独立安装与维护

服务端核心与 Linux/VPS 安装维护统一使用 Go。最终平台包不需要 Python、Node 或 Go 编译器；Python 仅用于开发侧构建和验证。当前目标为 Debian 12/13 amd64、systemd，API v2、数据库 schema 2。旧开发环境须保留归档后使用新数据初始化。

## 局域网 Linux 服务端

从 GitHub Actions 下载 Linux server 平台包，解压后以 root 运行包内 `bash install-server.sh`。程序安装后打开菜单，以后输入 **`ng`** 即可。远程下载入口使用 GitHub Release；尚无对应 Release 时需下载 Actions 产物，安装器不会在用户服务器上临时安装编译器或 Python。

菜单分别提供首次初始化/启动、状态、地址与端口、证书、客户端导入文件、旁路由 IP/DNS、VPS 设置、诊断和卸载。安装初始化会询问控制/Relay 地址、端口、LAN 网卡与网段、证书选项、管理员邮箱和密码。密码不进入命令行参数。

默认控制 TCP 10443、Relay TCP/UDP 18443、STUN UDP 3478，可在菜单修改。自动签发使用站点私有 CA；已有证书可导入 `control.crt/key`、`relay.crt/key`，私有 CA 另需 `ca.crt`。证书校验域名/IP、有效期、完整链及私钥配对，CA 私钥留在 LAN。

初始化通过 NetBird API 创建本地管理员、关闭默认全员互通策略，并登记专用路由节点。账号默认没有资源权限，管理员在管理页授予明确的 LAN 或互联网资源权限。

旁路由菜单支持指定 IP、首选/备用 DNS，以及普通 DNS 或 Clash/Mihomo Fake-IP DNS（可自动检测）。Fake-IP 仅支持专用 `198.18.0.0/15` 地址范围。互联网出口使用独立的 VPN 入站策略路由表，服务器原有默认路由不变。

**必须在管理页将“互联网（旁路由）”资源授予相应账号，该账号才能使用旁路由访问互联网。** 客户端默认关闭互联网模式，用户可在连接前选择。勾选开关不增加权限；撤权关闭该账号的租约转发，并清理其已有连接。私网访问仍由单独的 LAN 资源控制，DNS 只允许已设置地址。

配置修改会保存受保护备份，停止本次受管服务，校验并启动新配置；失败时恢复原配置及服务，并保持配置版本递增。客户端重新导入更新的站点 JSON。更换地址或 CA 时由客户端确认管理员提供的新身份。

## VPS 辅助节点

VPS 使用独立 `install-vps.sh`；安装后快捷命令为 **`ngv`**。VPS 包包含 `frps`、NetBird Relay 和 Go 维护工具。`frps` 在 VPS 上负责反向控制隧道；LAN 包只需要 `frpc`，因此不重复打入 `frps`。

先在 LAN 的 `ng → VPS` 菜单填写 VPS 地址与端口，选择或导入节点证书，并导出一个 `NetGo-vps.zip`。私下复制到 VPS，再在 `ngv` 中选择导入。该 ZIP 包含节点 TLS 私钥及节点通信密钥，不含控制数据库、管理员令牌、设备密钥或站点 CA 私钥。

VPS 菜单提供配置导入、状态、端口修改、证书更新、诊断和卸载。VPS 改端口后生成公开设置 JSON，复制回 LAN 并在 `ng → VPS → 导入新设置` 更新 LAN 隧道及客户端入口。更新证书通过 LAN 新导出的节点 ZIP 导入，避免把 CA 签发私钥复制到 VPS。

独立端口模式可用 IP 或域名。共享 443 模式需要三个不同 DNS 域名及空闲 443 端口，并增加受管 HAProxy 和 QUIC 重定向。已有 Docker、SSH、80/443 服务不被接管；云安全组和现有过滤策略按实际所需端口配置。

## 卸载和离线调用

在各自菜单选择卸载。只停止与移除本次受管服务、快捷命令、路由/防火墙规则，保留配置、身份、证书、数据和程序以供重装；不执行系统包自动清理。发现受管文件被其他程序修改时保留现场。

高级或自动化调用可使用：

```sh
bash install-server.sh install --source /root/NetGo-server --install-only
ng configure
ng uninstall --yes

bash install-vps.sh install --source /root/NetGo-vps --bundle /root/NetGo-vps.zip
ngv doctor
ngv uninstall --yes
```

完整 ZIP 可用 `--package FILE --sha256 HASH` 安装，包和内部文件清单均校验。`--plan` 只显示计划。

## Windows

安装版双击 Setup，之后从开始菜单打开 **NetGo.exe**；在系统“已安装的应用”中卸载。绿色版解压后双击 NetGo.exe，首次 VPN 网络授权由客户端调用内部准备工具。正常客户端无需运行 PowerShell 维护脚本或分别启动后台 EXE。

站点 JSON、账号密码、连接/断开、互联网模式选择和远控都从客户端操作。密码与设备密钥保存在 Windows Credential Manager；导出站点文件不包含密码或私钥。

## 验证与参考

客户端不再要求本机与服务端时间差小于 30 秒。通过 TLS 和站点身份校验后，客户端采用服务端时间，并用单调时钟计算经过时间；请求签名、凭据刷新和租约期限使用这一时间基准。管理网页也采用相同方法。普通用户不需要为秒或分钟级偏差手动校时；证书验证、防重放、撤权和租约到期仍然有效。

构建、单元测试和模拟器检查不等于跨设备 VPN/远控、UAC/锁屏、多屏或长期运行验收。Apple 构建已独立接入；设备安装需要签名，实机 VPN/媒体需要设备环境。

菜单体验参考 [sing-box-yg](https://github.com/yonggekkk/sing-box-yg/tree/b13f9435c4792fccf04d4fbc73e24a1948abff1b) 与 [sing-box-vpn](https://github.com/sck03/sing-box-vpn/tree/2a046930fdad1d49823f5dd75495b1c91e152802) 的快捷命令、证书、端口和卸载流程，代码独立实现。构建入口见 [独立构建说明](BUILD_CLIENTS.zh-CN.md)。
