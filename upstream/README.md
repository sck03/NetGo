# 固定上游及补丁

查询日期：2026-11-21。官方发布数据见 releases.json；最终构建输入见 versions.lock.json、go.sum、package-lock.json。

- NetBird v0.83.2，commit 6d4814d22726c16961be79409dfbf6a2b2f7537b。
- Dex 使用该版本 NetBird 自身锁定的 fork，commit 0319b501f1a6822b9f941dcac82d0d4edca5018b；不独立升级其内部协议依赖。
- NetGo 使用公开 daemon proto。补丁通过已鉴权 Login RPC 的受限 metadata 增加 prepare/native 模式，不改变 proto 编号；没有共享 setup key。
- Dex 新增内部 secret 认证入口，只调用已有 password connector、创建标准 PKCE authorization code；客户端配置启用 MFA 时拒绝原生入口，避免绕过 MFA。
- Management 校验 JWT nonce 内的 WireGuard key 与加密注册请求的 key 相同；peer REST 返回真实 key 给已鉴权调用者。
- Management/Signal 的 gRPC 使用同一 authority/TLS 名称，在已配置地址间选择；不更改全局 hosts。0.2.3-dev 通过受权 metadata 传递候选类别，并发探测全部直达入口，800 毫秒后启动备用入口，每候选最多 3 秒；取消时关闭其他探测。建立连接后继续由上游维护该流，不强制迁移健康 gRPC 连接。

补丁文件由固定源码 diff 生成；scripts/prepare-upstream.py 从 versions.lock.json 读取 commit，在专用目录应用补丁和本地 Dex replacement，再用临时 Git index 核对完整源码。已有补丁可反向应用不再视为足够证据：无关修改、额外文件以及 Git 忽略的源码也会被拒绝，原现场保留供审阅。源码归档另核对三个固定仓库的 HEAD。构建服务端必须在 Linux 启用 CGO；SQLite IdP 不支持 CGO=0 生产构建。

Windows 完整检查使用 scripts/verify.ps1；Debian 使用 `python3 -B scripts/verify-linux.py --output 新目录`，可先加 --plan 查看命令。前者为 15 段，后者为 13 段原生检查；两者都记录源码、日志与构建文件摘要。打包必须指定完整验证的 --verification，拒绝过期证据或检查后的二进制变化。构建通过不代表真实网络或生产发行通过。

上游 AGPL 目录为 management、signal、relay、combined；保留原始许可证与完整补丁/源码。客户端和其余目录按上游 BSD-3-Clause。其他依赖各自许可见 THIRD_PARTY_NOTICES.md。此清单不把上游内部依赖的冻结误称为全部最新版本。
