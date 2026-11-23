# NetGo 构建、安装与运维

本手册对应已实现的 0.2.4-dev VPN 基线；Windows 使用 C++20，Linux 服务端/诊断 CLI 使用 Go。支持 **Debian 12/13 amd64**，当前 LAN/VPS 为 Debian 13、Windows 为 10 LTSC 1809。远控和 Android/Apple/Linux 原生端已进入 [v2.0 实施范围](REMOTE_NATIVE_PLAN.zh-CN.md)，尚未成为可用安装组件。独立安装见 [INSTALLERS](INSTALLERS.zh-CN.md)，当前接续与失败记录见 [本轮报告](evidence/2026-11-23-continuation/REPORT.zh-CN.md)。

## 从固定源码构建

Windows 需要 MSYS2 UCRT64 GCC/CMake/Ninja、MSYS2 Perl/Make、Go 1.27.4、Node 26.13.1，以及安装 cryptography 的 Python。C++ 工具链位置可传入：

    .\scripts\build.ps1 -Toolchain D:\msys64\ucrt64

如果 PATH 中有多个 Python，请显式传入 -Python D:\Python\Python313\python.exe；选择安装了 cryptography 的解释器。Python 仅用于开发测试，Windows 运行包不需要安装它或 Caddy。

prepare-native.ps1 固定并校验 OpenSSL 4.1.0、JSON 3.12.0 和官方签名 Wintun 0.14.1。首次 OpenSSL 编译需要数分钟。build-native.ps1 运行 CTest 与回环 TLS 测试。build-upstream-windows.ps1 从固定 commit 和交付补丁创建新 checkout，生成独立 NetBird 核心。

Debian 12/13 amd64 的基础构建与运行依赖：

```sh
apt-get update
apt-get install --no-install-recommends build-essential pkg-config git ca-certificates \
  python3 python3-cryptography nftables conntrack iproute2 iptables procps util-linux \
  openssl systemd curl unzip
# 仅启用 443 SNI 模板时需要：
apt-get install --no-install-recommends haproxy
# 使用 Cloudflare DNS-01 时需要：
apt-get install --no-install-recommends certbot python3-certbot-dns-cloudflare
```

另外安装并核验版本锁中的 Go 1.27.4、Node 26.13.1；不要直接用发行版自带 Go/Node 代替。GCC 的锁定 Windows 版本不适用于 Debian 的 CGO 编译；Debian GCC、glibc、内核、nftables、HAProxy、Certbot/provider 的实际包版本写入 A28/A30 环境证据。预检查及构建：

    python3 -B scripts/linux_preflight.py --mode build

    bash scripts/build.sh
    bash scripts/build-upstream.sh

`build-upstream.sh` 必须在 Debian 12/13 启用 CGO：内嵌 SQLite IdP 不支持 CGO=0。脚本还从版本锁的固定 tag 构建 Caddy、frpc、frps，输出的 SHA256SUMS 覆盖六个上游程序。安装器将它们放在 /opt/netgo/upstream；产品 TLS 网关使用 netgo-caddy，443 SNI 使用 netgo-haproxy，不覆盖发行版共享服务配置。Windows 上生成的 NetGo Linux 文件只证明交叉编译成功，不能替代 Debian 原生构建和运行验证。

依赖已准备时，Windows 的 `scripts/verify.ps1 -Output 新目录` 运行 15 段检查：工具链、源码指纹、工具回归、Go 竞态/vet、前端、脚本/证书输入、六种部署组合、原生/上游构建、产品清单与构建文件摘要。逐项日志和 schema 2 的 verification.json 一起保存。显式跳过 Native/Upstream 会将总状态标为 INCOMPLETE。

Debian 的对应入口为 `python3 -B scripts/verify-linux.py --output 新目录`；先加 `--plan` 可只查看命令。该入口执行 13 段原生检查，包含 NetBird/Dex CGO、六个上游程序以及 HAProxy 配置解析，因此完整检查环境也需安装 haproxy、OpenSSL 和 Python cryptography。它不安装服务、不启用 VPN、不修改防火墙。构建报告和实际运行验收分别保存，后者覆盖当前已提供的实验环境。

上游准备目录按固定 commit 和补丁摘要区分。准备器用临时 Git index 核对锁定源码加补丁和唯一的 Dex replacement；发现无关修改或额外文件（含 Git 忽略的源码）即拒绝，保留原文件供审阅。不要通过清空未知修改来使构建通过；确认来源后可用新的 `--output` 目录重放。源码归档也核对 NetBird/Dex/OpenSSL 的固定 HEAD。

Windows 开发交付流程：

    pwsh -NoProfile -File scripts/verify.ps1 -Output .cache/verification-example
    python -B scripts/package.py --version 0.2.4-dev --verification .cache/verification-example/verification.json

打包前后核对当前源码、完整检查日志及构建文件；旧 schema 1、缺少检查、INCOMPLETE、改过的源码或二进制均不能沿用。ZIP 再逐项对照已验证的代码/构建字节、包内摘要与 CRC，生成 `dist/packages/NetGo-0.2.4-dev-delivery-verification.json`。文档补记后可以沿用相同代码验证重新打包；代码/脚本/测试或上游补丁改变则必须重新验证。该入口产出 Windows 包、仅产品程序的 Linux 开发包与完整源码包。**完整 Linux 安装包**另在 Debian 原生检查通过后执行 `python3 -B scripts/package-server.py --verification 验证目录/verification.json`，包含六个上游程序。

## Windows 安装和使用

以预期桌面用户的提升权限 PowerShell 执行：

    .\deploy\windows\install.ps1 -Plan
    .\deploy\windows\install.ps1

安装前校验四个运行文件的 SHA-256 和 Wintun 签名。C++ 界面/后台放在当前用户 LOCALAPPDATA/NetGoNative/bin；特权网络核心与驱动放在 Program Files/NetGoNative/engine，普通用户只有读取/执行权限。网络配置在 ProgramData/NetGoNativeEngine，使用管理员/SYSTEM ACL。独立服务名为 NetGoNetBird、管道为 npipe://netgo-netbird，仅授权安装时的用户 SID。

首次配置禁止自动连接，使用不可达回环管理地址；导入并验证站点后，受权 Login RPC 才配置实际站点。正常使用界面和后台不需要管理员权限。

专用服务使用 `NB_STATE_DIR` 隔离 profiles、运行状态和日志，不能仅靠 `--config` 假定隔离。Windows 为 ProgramData/NetGoNativeEngine，Linux 路由节点为 /var/lib/netgo-router。专用服务设置 `NB_LAZY_CONN=false`，主动建立到路由节点的可用路径；导入的公共 CA 也传递给 Relay 的 WS/QUIC，保持域名验证，不修改 Windows 证书库。

从开始菜单打开 NetGo，导入管理员在网页导出的站点 JSON，核对规范 HTTPS 域名与 CA，然后填写账号密码。默认不记住密码；勾选后用当前用户 Credential Manager 保存。启用“启动后连接”还需显式保存该站点的凭据。关闭窗口默认保留托盘，托盘退出会清理连接并关闭后台。

新版主窗口使用连接、VPN 信息、可访问资源和日志四个分页；普通配置用表单编辑，复杂多入口保留高级编辑。自动连接绑定所选站点，Ctrl+N/O/E/L/Q 分别用于新建、导入、编辑、日志和退出。见 [Windows 使用说明](WINDOWS_CLIENT.zh-CN.md)。

状态分别显示控制入口与数据路径。路径取自 NetBird 到配置路由节点 IP 的实际 peer 状态。租约不能续期、权限失效或取消时执行撤销与 Down；Down 无法确认时报告清理失败，不允许切站点。

连接 generation 先原子保存到当前用户目录，再发送请求。持有相同设备密钥、采用更新 generation 的重新认证可接续本设备旧租约；旧 generation 无权替换新连接。刷新身份令牌同时更新上游核心登录，网络服务重启后用内存中的有效令牌恢复。是否达到换网/重启的时间目标仍需实测。

域名或 CA 改变时使用新的站点 ID。公共站点导出不含账号密码、令牌或私钥。日志只记录状态和错误码，诊断导出使用字段白名单。

0.2.2-dev 在发送密码前检查协议号和必要能力；服务端拒绝缺少/不匹配 X-NetGo-Protocol 的产品 API 请求。旧开发客户端和服务端应成套更新；上游 NetBird 核心也必须包含本次 ConfigRevision RPC 字段。协议不匹配会明确提示升级，不自动降低验证要求。

站点文件增加 ConfigRevision。Windows 与 Linux 在每次成功认证后同步服务端配置，并在验证同一 Origin/CA/site_id 和探测新候选成功后原子保存；本地显示名称保留。旧文件可从隐含版本 0 迁移到版本 1，版本化后拒绝倒退或同版本不同内容。连接期间沿用本次配置快照；新增候选在下次连接生效，DDNS 解析及已有候选的多 WAN 故障恢复不受此限制。配置回退须以更高版本重新发布旧内容，不能直接减少版本号。

卸载：

    .\deploy\windows\uninstall.ps1 -Plan
    .\deploy\windows\uninstall.ps1

更新先在 Program Files/NetGoNative/engine/.install-transaction 保存旧程序、核心配置和快捷方式，再停止服务并替换文件。安装失败时恢复旧文件和原服务运行状态；进程被强制终止或恢复失败时保留受保护 journal.json，人工检查后恢复，不覆盖现场。服务可执行路径必须精确匹配，owner.sid 必须属于当前安装用户。

卸载删除已识别的程序和独立服务，保留受保护配置用于恢复。common.ps1 必须与安装/卸载脚本同目录。清除密码/设备密钥时，在卸载前使用“忘记密码”或“删除服务器”。A25/A28 仍需在专门 Windows 实验机测试；本轮没有在开发工作站安装 VPN 服务。

## Linux 本地站点

1. 原生构建上游，核对 dist/upstream-linux-amd64/SHA256SUMS 和 dist/linux-amd64/SHA256SUMS，先执行 bash deploy/linux/install.sh --plan，再以 root 安装。安装写文件/单元和 CLI 符号链接，不自动改变转发或启用隧道。install.manifest 登记受管单元和上游文件，失败撤掉本轮新建文件，保留账号/数据和恢复目录。重复安装相同版本会检查已有文件。
2. 以 examples/site.json 为模板配置真实 LAN 接口、网段、overlay、域名和路由节点，按验收手册准备 Linux、LAN 目标和两客户端。
3. 使用 scripts/render-deploy.py --config /etc/netgo/site.json --output /root/netgo-render 生成配置。保管 secrets.json；重复生成时传入 --secrets。控制 TLS 私钥和 native.secret 始终留在本地。
4. 将 local/netbird.yaml、relay.env、native.secret 放入 /etc/netgo，提供 netgo 组必要读取权限，文件权限 0640 或更严格。存储路径须与服务单元的 /var/lib/netbird-server 一致。准备控制证书/密钥，运行 Caddy 配置验证后再启用 TLS 网关。
5. 创建 NetBird 本地管理员，在本地生成 PAT，写入 /etc/netgo/management.token；启动本地身份、Management、Signal 与独立 Relay/STUN。专用路由服务为 netgo-router，配置位于 /var/lib/netgo-router/config.json，RPC 位于 /run/netgo-router/netbird.sock。安装器预置禁止自动连接的离线配置；登记前改为已验证的本地 ManagementURL，禁止连接官方云。为 Routing Peer 单独注册设备，记录实际 peer ID/overlay IP 并填回配置。路由节点可用专用设备密钥，普通用户不能共享 setup key。
6. 确保 site.json 为 root:netgo、0640，管理 token/native.secret 也由 netgo 组读取。用 `runuser -u netgo -- netgoctl init-admin --config /etc/netgo/site.json --admin-id 实际ID --admin-alias admin --admin-email 实际邮箱` 建立别名，使首次元数据库由服务用户创建。再启动 netgo-helper/netgo-server，校验并应用资源策略。新用户默认无资源权限。
7. 记录原有 net.ipv4.ip_forward 值后开启 IPv4 转发，验证 NetGo 门禁、NetBird 路由/SNAT、INPUT/FORWARD/conntrack 和主机其他规则。

安装前可独立执行 `python3 -B scripts/linux_preflight.py --mode install`。它只读检查 Debian 12/13/amd64 和必要命令，不安装包、不启用服务、不改变防火墙。将生成的 local/Caddyfile 放到 /etc/netgo/Caddyfile，以 /opt/netgo/upstream/caddy validate 检查后启用 netgo-caddy。443 模式另将 haproxy.cfg 放到 /etc/netgo/haproxy.cfg，检查 /usr/sbin/haproxy -c 后启用 netgo-haproxy；证书 hook 会重载 netgo-caddy。不要同时让发行版共享 Caddy/HAProxy 占用相同端口。

`netgoctl init` 初次生成的文件为仅当前用户可读；部署到 /etc/netgo 后执行 `chown root:netgo /etc/netgo/site.json` 和 `chmod 0640 /etc/netgo/site.json`。既有文件的原子配置更新保留所有者与权限。数据库目录应归 netgo；路由节点状态目录仍归 root，不对整个 /var/lib 递归改属主。

国内动态公网线路的新站点默认使用高位端口，可以这样初始化：

```sh
netgoctl init --config /etc/netgo/site.json --site-id lab --domain control.site.example \
  --control-port 10443 --relay-port 18443 --stun-port 3478
```

Site.Origin 保留规范 HTTPS authority（包括初始选定端口），Candidates[].Address 是实际拨号地址。Listen/ManagementURL 决定 Caddy 的内部反向代理目标；Deployment.LocalRelayPort/LocalSTUNPort 配置本地监听。路由器对外端口映射与候选须一致。443 受限不影响使用合法的其他端口，TLS 证书仍按域名校验。既有配置不被自动迁移或改写。

`deployment.json` 记录渲染输入摘要、实际端口和文件 SHA-256。先验证 `netgoctl config validate`，再渲染到新目录；重复渲染使用原 secrets.json，避免无意轮换存储和 Relay 密钥。新部署须显式审查并禁用 NetBird 默认的全员互通策略；协调器发现未受管的启用策略会拒绝应用，不能为通过检查而放宽权限。

安装器现在要求 conntrack。门禁仅操作带“NetGo lease gate v1”归属标记的 inet netgo 表；发现未标记同名表或涉及隧道/LAN 网卡的 flowtable 时拒绝启动。旧开发版未标记表须先停止相关服务并人工核对归属，再迁移，不能直接删除未知表。重新授权同一虚拟 IP 时，先关闭该 IP、清理它的原始源/目标 conntrack，再开放新租约；其他会话继续按其租约受控。

配置变更与诊断：

    netgoctl doctor --config /etc/netgo/site.json
    netgoctl config validate --config /root/proposed-site.json
    netgoctl config plan --config /etc/netgo/site.json --input /root/proposed-site.json
    netgoctl config apply --config /etc/netgo/site.json --input /root/proposed-site.json

doctor 只读检查配置、依赖、接口、转发、受管防火墙、已验证控制入口、证书有效期和本地健康；有失败项退出非零。它不能证明外部数据路径或 LAN 访问通过。带 --input 的 apply 保存原配置到 site.json.transaction/previous.json，停服务后原子写入，检查健康并协调策略；失败恢复旧配置/服务/策略。身份、数据库、密钥路径或辅助/Relay/Deployment 拓扑不能通过此事务迁移，须先制作完整部署配置。配置与版本切换共用 site.json.operation-lock，避免并发停服务或回滚到过时状态；强制中断后先检查进程和事务材料，再人工清理已确认失效的锁。事务目录已存在时保留现场并拒绝覆盖。不带 --input 的 apply 与 reconcile 同样只协调数据库中的资源策略。

改变候选或路由节点等站点字段时递增 Site.ConfigRevision，plan 会列出当前、拟用及失败恢复版本。失败回滚会用“拟用版本 + 1”重新发布旧内容，让已经同步过新配置的客户端也能恢复。auxiliary add/remove 自动递增版本。

管理概览显示每 30 秒采样的服务、可信控制入口/备用通道、本地 Relay TCP 监听、证书和数据库磁盘检查；90 秒没有新采样会告警。采样包含服务进程 CPU 时间、最大 RSS、Go 堆内存和请求/认证失败计数。检查通过只代表检查项成立，不代表 LAN/WS/QUIC 或公网验收通过。日志为 JSON，仅保留站点、随机 trace_id、操作类别、状态和耗时；不记录请求体、查询参数或认证头。管理页“诊断与审计”可导出管理员受权的脱敏诊断；CLI diagnostics 的进程指标属于采集进程自身，服务端指标以管理 API 为准。

## Linux 密码维护

在目标 Debian 12/13 上运行：

```sh
sudo netgoctl reset-password --config /etc/netgo/site.json --user-id 实际用户ID
```

终端两次输入新密码；自动化可显式使用 --password-stdin，不把密码放到参数或环境变量。要求 8–72 字节且无换行，具体强度仍由原 IdP 校验。命令暂时停止产品、路由节点和身份服务，所以所有客户端需要重新连接；先撤销目标用户的产品凭据、递增认证代次、关闭租约门禁，再以 netgo 用户通过原 NetBird `admin user change-password --password-file -` 命令修改唯一 IdP 密码来源。NetGo 不读取/复制密码哈希，也不直接写 IdP 数据表。

失败时服务保持停止，site.json.password-reset.json 保存不含密码的阶段与原服务状态；排除原因后对同一用户重试命令。成功后只恢复原来活动的服务并检查产品健康，不能通过恢复旧产品 token 回滚密码。强制中断后的 operation-lock 按上节恢复流程处理。已有 IdP JWT 仍遵循上游有效期；本操作保证其不能凭旧 NetGo 凭据续租或访问 LAN，不声称改写 OIDC 已签发令牌的语义。账号修改也会阻止此前已完成密码验证、尚未兑换的迟到认证事务。

## Linux 诊断客户端与卸载

Linux 诊断客户端由当前普通用户运行：

    netgo-client-service --daemon /var/run/netbird.sock
    netgoctl client import --input /path/to/public-site.json
    netgoctl client connect --site-id lab --alias alice
    netgoctl client status
    netgoctl client disconnect

密码由终端读取；--replace 才显式替换其他设备的旧会话。诊断客户端需单独安装受权 NetBird daemon；不要把服务端的 netgo-router 管道当成普通用户客户端。generation 持久化，刷新令牌仅在内存，私钥文件位于用户配置目录且权限受限。不同用户/站点不共用密钥。

0.2.3-dev 重连会复用当前用户的唯一同名 NetBird profile，保留其 WireGuard 身份；发现历史同名歧义时拒绝连接，须先核对设备和密钥再处理重复项，不能自动删除。授权路由下发采用最多 15 秒的有界等待，取消会终止等待，不选择未完整到达的资源路由。核心 gRPC 同时探测直达候选，800 毫秒后启用备用，TLS 仍验证规范站点身份；健康的已有 gRPC 流不因 HTTP 探测恢复而被强制中断。

卸载先运行 bash deploy/linux/uninstall.sh --plan。实际卸载只停止/删除归属清单中的单元、精确匹配的 CLI 链接和有标记的受管表；保留配置、身份、发行目录、上游程序和账号。重装相同已校验版本可复用这些保留文件。

本地 Relay/STUN 必须始终保留。没有 VPS 时需要实际可达的公网入口；公网消失前未配置备用入口时，不承诺恢复接入。

## 多 WAN、可选 VPS 与 TCP 443

本地 Debian 加零台或一台 VPS 即为第一版拓扑。一台 VPS 同时运行 frps 与 NetBird Relay/STUN；无需两台 VPS。Host、TunnelHost、Relay 中的不同域名可解析到相同公网 IP。FRP 只用于本项目的备用登录/控制通道；实际 LAN 业务优先走 NetBird 直连，必要时才走同一 VPS 的 Relay，不让业务数据再串行穿过 FRP。

H3C MSR3610-X1-DP 的多宽带拨号、NAT、按连接负载均衡和主备策略由路由器负责；NetGo 负责多个入口的探测和恢复。参考 `docs/MULTI_WAN.zh-CN.md` 与 `examples/site-multiwan.json`。每条 WAN 使用独立 DDNS 域名，优先让路由器更新；需要 Linux 更新时，可用 `examples/ddns-wan-file.json` 消费受信管理程序提供的逐 WAN 地址文件。文件过期、私网/CGNAT、地址族错误和多个地址均拒绝发布，不把普通外网查询当作所有 WAN 的地址。

VPS 默认高位端口示例见 examples/auxiliary.json，独立 443 模式见 examples/auxiliary-443.json。使用以下命令生成含备用候选的新配置，再部署对应组件：

```sh
netgoctl auxiliary add --config /etc/netgo/site.json --input examples/auxiliary-443.json --output /root/site-with-vps.json
python3 scripts/render-deploy.py --config /root/site-with-vps.json --output /root/render-with-vps --secrets /root/netgo-render/secrets.json
```

本地 Deployment.Mode 与 VPS Mode 独立，允许本地 10443/18443 + VPS 443。VPS 的 ControlPort/TunnelPort 和 Relay/STUN URI 可配置；443 模式公共端口须统一为 443，内部后端保持分离。只将 auxiliary/ 配置及必要秘密放到 VPS，不复制 IdP/Management 数据库和控制 TLS 私钥。先验证 frpc/frps、HAProxy、Caddy 配置及端口占用，再启用服务。要将本地也设为 443，可新建站点时使用 `netgoctl init --mode 443`；旧站点的 issuer/端口变更须单独审阅，不能直接改掉已导入的信任身份。

两种模板解析与运行实测分别记录。已有本地 Relay/TCP、公网备用控制/刷新以及一次 VPS Relay 成功记录；随后最终 Relay 复测失败仍需关闭。TCP 443-only、运营商线路、多 WAN 和 VPS 全停冷启动分别验收，不能由高位端口实验代替。

Relay 和 frps 单元以 netgo 用户运行，仅授予 `CAP_NET_BIND_SERVICE`，以支持配置中明确允许的低位监听端口；无需将整个进程改为 root。具体端口占用、内核 capability 和 systemd 行为仍须在 Debian 上验证。

## DNS-01 与证书更新

本地不开放 80/443 时，使用 DNS-01 签发域名证书。第一版说明使用目标 Linux 提供的 Certbot 与 Cloudflare DNS 插件；安装前记录 `certbot --version` 和插件包版本，并纳入该站点的依赖清单。当前未在真实 DNS 区域签发或续期，其他 provider 需要单独适配和验证。

将 Cloudflare DNS API 凭据存到仅 root 可读的 `/etc/netgo/cloudflare.ini`（0600，字段 `dns_cloudflare_api_token`）。这是 Certbot 插件格式，与 NetGo DDNS 使用的纯令牌文件不同。将 `deploy/linux/deploy-certificate.sh` 安装为 root 所有的 `/etc/netgo/deploy-certificate.sh`（0750）。示例域名须替换成实际 Site.Origin 主机名和 LocalRelay：

```sh
certbot certonly --dns-cloudflare --dns-cloudflare-credentials /etc/netgo/cloudflare.ini \
  --dns-cloudflare-propagation-seconds 90 --cert-name netgo-control -d control.site.example \
  --deploy-hook '/etc/netgo/deploy-certificate.sh control control.site.example'
certbot certonly --dns-cloudflare --dns-cloudflare-credentials /etc/netgo/cloudflare.ini \
  --dns-cloudflare-propagation-seconds 90 --cert-name netgo-relay -d relay.site.example \
  --deploy-hook '/etc/netgo/deploy-certificate.sh relay relay.site.example'
```

Certbot 负责 ACME 账户、TXT 更新和签发，hook 使用其 RENEWED_LINEAGE。hook 校验域名、证书当前有效且至少剩余 14 天、密钥与证书匹配，写入 `/etc/netgo/tls/{control,relay,tunnel}.{crt,key}`（root:netgo，0640），只重载/重启原本活动的对应服务。随包 netgo-caddy 已以 netgo 用户运行；如采用自有 Caddy 服务，应单独配置所需读取权限，不放宽私钥到全员可读。失败恢复原文件并留下 `.certificate-角色.transaction`，先检查服务和旧证书再处理恢复目录。

`--plan 角色 域名` 只输出计划；`--check 角色 域名` 配合 RENEWED_LINEAGE 只校验输入，不写系统文件或操作服务。证书输入检查已用临时证书验证；实际 systemd 重载、故障回退、续期调度、DNS 权限和外部客户端复测属于 A30。Relay/frps 更新需要短暂重启，记录连接恢复时间。VPS 上分别管理 Relay/tunnel 的证书，控制私钥仍留在本地。

首次签发后按发行版配置 Certbot 的定时续期，并先运行 `certbot renew --dry-run` 检查 DNS-01。卸载 NetGo 保留配置/证书；管理员同时决定是否停止对应 Certbot 续期配置与 hook。

## 备份与回退

备份前停止 netgo-server、netbird-server 和路由 daemon。默认根目录为 /etc/netgo、/var/lib/netgo、/var/lib/netbird-server、/var/lib/netgo-router；自定义路径须通过 --sources 列出。密码从终端读取，不放在参数或日志。归档格式 2 将各根目录保存为 root-000、root-001 等，manifest.json 的 archive_root_map 映射回实际来源，避免两个 netgo 同名目录冲突；旧归档仍可解密/提取。恢复到新目录后按映射还原，保持 issuer 和密钥身份，并恢复各服务的文件属主/权限。内置归档上限 256 MB，更大数据库采用外部一致快照。

推荐使用集成的产品升级/回退命令：

    netgoctl upgrade --config /etc/netgo/site.json --input /root/new-release/linux-amd64 --sha256 已独立核对的SHA256SUMS文件摘要 --output /root/backups/pre-upgrade.netgo
    netgoctl rollback --config /etc/netgo/site.json

发行目录必须包含五个产品文件、release.json、SHA256SUMS，摘要须通过可信渠道独立核对。升级先校验所有文件和 protocol/schema，再停写、生成加密备份、切换 /opt/netgo/current 并重启检查；失败自动切回原版本。previous-release.json 记录旧版本与快照，强制中断留下 .release-transaction/journal.json。rollback 校验旧版本后切换，保留当前 schema 1 的兼容用户数据；需要恢复数据时从记录的加密快照演练恢复。该命令只升级 NetGo 产品二进制，不替换 NetBird/Dex/Caddy/frp 或执行不可逆 schema 迁移。

激活前可在开发主机只读运行 netgoctl release verify --input 发行目录 --sha256 已独立核对的清单摘要；它验证文件/兼容信息，不能证明 Linux 运行或网络通过。当前脚本检查可通过 scripts/verify-scripts.ps1 重跑。

旧 switch-release.sh 保留为已安装版本间的辅助入口；新流程优先使用 netgoctl。schema 仍为 1，max_sessions 零值延续单会话默认。以后上游升级、单元调整和不可逆迁移须另行准备匹配配置和一致快照，并实测回退。

tests/network/run.py 提供环境初始化、超时/失败留证、摘要校验、审阅绑定、33 用例汇总与分路径恢复指标。live_api、live_network、live_operations、live_paths 和 live_windows 提供本轮明确实验拓扑下的运行验证；命令见 tests/network/README.zh-CN.md。其结果不等同于全部 A01–A33、外部网络或 72 小时验收通过。
