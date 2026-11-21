# NetGo 构建、安装与运维

本版为 0.2.0-dev；Windows 界面与产品后台使用 C++20。真实网络发行验收尚未通过，先在实验机验证。

## 从固定源码构建

Windows 需要 MSYS2 UCRT64 GCC/CMake/Ninja、MSYS2 Perl/Make、Go 1.27.4、Node 26.13.1，以及安装 cryptography 的 Python。C++ 工具链位置可传入：

    .\scripts\build.ps1 -Toolchain D:\msys64\ucrt64

如果 PATH 中有多个 Python，请显式传入 -Python D:\Python\Python313\python.exe；选择安装了 cryptography 的解释器。Python 仅用于开发测试，Windows 运行包不需要安装它或 Caddy。

prepare-native.ps1 固定并校验 OpenSSL 4.1.0、JSON 3.12.0 和官方签名 Wintun 0.14.1。首次 OpenSSL 编译需要数分钟。build-native.ps1 运行 CTest 与回环 TLS 测试。build-upstream-windows.ps1 从固定 commit 和交付补丁创建新 checkout，生成独立 NetBird 核心。

Linux amd64 主机安装 GCC、Go/Node、nftables、systemd、Caddy、可选 HAProxy/frp，执行：

    bash scripts/build.sh
    bash scripts/build-upstream.sh

第二条必须在 Linux 启用 CGO：内嵌 SQLite IdP 不支持 CGO=0。Windows 上生成的 NetGo Linux 文件只证明交叉编译成功，不能替代上游原生构建和运行验证。

## Windows 安装和使用

以预期桌面用户的提升权限 PowerShell 执行：

    .\deploy\windows\install.ps1 -Plan
    .\deploy\windows\install.ps1

安装前校验四个运行文件的 SHA-256 和 Wintun 签名。C++ 界面/后台放在当前用户 LOCALAPPDATA/NetGoNative/bin；特权网络核心与驱动放在 Program Files/NetGoNative/engine，普通用户只有读取/执行权限。网络配置在 ProgramData/NetGoNativeEngine，使用管理员/SYSTEM ACL。独立服务名为 NetGoNetBird、管道为 npipe://netgo-netbird，仅授权安装时的用户 SID。

首次配置禁止自动连接，使用不可达回环管理地址；导入并验证站点后，受权 Login RPC 才配置实际站点。正常使用界面和后台不需要管理员权限。

从开始菜单打开 NetGo，导入管理员在网页导出的站点 JSON，核对规范 HTTPS 域名与 CA，然后填写账号密码。默认不记住密码；勾选后用当前用户 Credential Manager 保存。启用“启动后连接”还需显式保存该站点的凭据。关闭窗口默认保留托盘，托盘退出会清理连接并关闭后台。

状态分别显示控制入口与数据路径。路径取自 NetBird 到配置路由节点 IP 的实际 peer 状态。租约不能续期、权限失效或取消时执行撤销与 Down；Down 无法确认时报告清理失败，不允许切站点。

连接 generation 先原子保存到当前用户目录，再发送请求。持有相同设备密钥、采用更新 generation 的重新认证可接续本设备旧租约；旧 generation 无权替换新连接。刷新身份令牌同时更新上游核心登录，网络服务重启后用内存中的有效令牌恢复。是否达到换网/重启的时间目标仍需实测。

域名或 CA 改变时使用新的站点 ID。公共站点导出不含账号密码、令牌或私钥。日志只记录状态和错误码，诊断导出使用字段白名单。

卸载：

    .\deploy\windows\uninstall.ps1 -Plan
    .\deploy\windows\uninstall.ps1

卸载删除已识别的程序和独立服务，保留受保护配置用于恢复。清除密码/设备密钥时，在卸载前使用“忘记密码”或“删除服务器”。A25/A28 仍需在专门 Windows 实验机测试；本轮没有在开发工作站安装 VPN 服务。

## Linux 本地站点

1. 原生构建上游，核对 dist/upstream-linux-amd64/SHA256SUMS，执行 deploy/linux/install.sh。安装只写文件/单元，不自动改变转发或启用隧道。
2. 以 examples/site.json 为模板配置真实 LAN 接口、网段、overlay、域名和路由节点，按验收手册准备 Linux、LAN 目标和两客户端。
3. 使用 scripts/render-deploy.py --config /etc/netgo/site.json --output /root/netgo-render 生成配置。保管 secrets.json；重复生成时传入 --secrets。控制 TLS 私钥和 native.secret 始终留在本地。
4. 将 local/netbird.yaml、relay.env、native.secret 放入 /etc/netgo，提供 netgo 组必要读取权限，文件权限 0640 或更严格。存储路径须与服务单元的 /var/lib/netbird-server 一致。准备控制证书/密钥，运行 Caddy 配置验证后再启用 TLS 网关。
5. 创建 NetBird 本地管理员，在本地生成 PAT，写入 /etc/netgo/management.token；启动本地身份、Management、Signal 与独立 Relay/STUN。为 Linux Routing Peer 单独注册设备，记录实际 peer ID/overlay IP 并填回配置。路由节点可用专用设备密钥，普通用户不能共享 setup key。
6. 用 netgoctl init-admin --config /etc/netgo/site.json --admin-id 实际ID --admin-alias admin --admin-email 实际邮箱建立别名，启动 netgo-helper/netgo-server，校验、审阅并应用资源策略。新用户默认无资源权限。
7. 记录原有 net.ipv4.ip_forward 值后开启 IPv4 转发，验证 NetGo 门禁、NetBird 路由/SNAT、INPUT/FORWARD/conntrack 和主机其他规则。

本地 Relay/STUN 必须始终保留。没有 VPS 时需要实际可达的公网入口；公网消失前未配置备用入口时，不承诺恢复接入。

## 可选 VPS 与 TCP 443

在 Auxiliaries 中配置辅助节点后重新渲染；--mode 443 生成 SNI 复用模板。只将 auxiliary/ 配置及必要秘密放到 VPS；不复制 IdP/Management 数据库和控制 TLS 私钥。先验证 frpc/frps、HAProxy、Caddy 配置及端口占用，再启用服务。

两种模板已通过当前 Windows 上 Caddy 适配和 frp 语法检查；HAProxy/nftables/Linux TLS 服务仍待 Linux 验收。VPS 全停后执行 A03，证明本地首次登录和 Relay/STUN 未被破坏。

## 备份与回退

备份前停止 netgo-server、netbird-server 和路由 daemon。netgoctl backup 的默认目录包括 /etc/netgo、/var/lib/netgo、/var/lib/netbird-server，还须加入实际 router 数据目录。密码从终端读取，不放在参数或日志。恢复到新目录，保持 issuer 和密钥身份。

deploy/linux/switch-release.sh 切换已校验版本，健康失败回到旧版。当前 schema 仍为 1，max_sessions 零值延续单会话默认。以后不可逆迁移须用一致快照回退。Windows 更新前停止后台及独立服务，保留配置/凭据；失败保留现场，修复后重跑安装。

tests/network/run.py 采集真实命令结果，由人工核对身份、路径、权限与脱敏内容；退出码不自动代表用例通过。
