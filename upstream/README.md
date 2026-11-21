# 固定上游及补丁

查询日期：2026-11-21。官方发布数据见 releases.json；最终构建输入见 versions.lock.json、go.sum、package-lock.json。

- NetBird v0.83.2，commit 6d4814d22726c16961be79409dfbf6a2b2f7537b。
- Dex 使用该版本 NetBird 自身锁定的 fork，commit 0319b501f1a6822b9f941dcac82d0d4edca5018b；不独立升级其内部协议依赖。
- NetGo 使用公开 daemon proto。补丁通过已鉴权 Login RPC 的受限 metadata 增加 prepare/native 模式，不改变 proto 编号；没有共享 setup key。
- Dex 新增内部 secret 认证入口，只调用已有 password connector、创建标准 PKCE authorization code；客户端配置启用 MFA 时拒绝原生入口，避免绕过 MFA。
- Management 校验 JWT nonce 内的 WireGuard key 与加密注册请求的 key 相同；peer REST 返回真实 key 给已鉴权调用者。
- Management/Signal 的 gRPC 使用同一 authority/TLS 名称，在已配置地址间选择；不更改全局 hosts。

补丁文件由固定源码 diff 生成；scripts/prepare-upstream.py 核验 HEAD、检测干净状态、应用补丁和本地 Dex replacement。构建服务端必须在 Linux 启用 CGO；SQLite IdP 不支持 CGO=0 生产构建。

上游 AGPL 目录为 management、signal、relay、combined；保留原始许可证与完整补丁/源码。客户端和其余目录按上游 BSD-3-Clause。其他依赖各自许可见 THIRD_PARTY_NOTICES.md。此清单不把上游内部依赖的冻结误称为全部最新版本。
