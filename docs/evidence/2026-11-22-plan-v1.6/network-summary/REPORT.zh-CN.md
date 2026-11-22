# 网络验收证据汇总

生成时间（UTC）：2026-11-22T07:15:29.044700+00:00

发行门槛：**CLOSED**。完整性校验不能替代人工检查网络行为。

| 用例 | 场景 | 状态 | 最近证据 |
| --- | --- | --- | --- |
| A01 | 无 VPS 全新登录 | NOT-RUN | — |
| A02 | 单机冷启动 | NOT-RUN | — |
| A03 | 添加 VPS 后全停 | NOT-RUN | — |
| A04 | 动态公网更新 | NOT-RUN | — |
| A05 | 无公网首次登录 | NOT-RUN | — |
| A06 | 打洞与自动中继 | NOT-RUN | — |
| A07 | 公网突然消失 | NOT-RUN | — |
| A08 | 公网恢复与抖动 | NOT-RUN | — |
| A09 | UDP 全阻断 | NOT-RUN | — |
| A10 | 仅 TCP 443 | NOT-RUN | — |
| A11 | 无 VPS 本地中继 | NOT-RUN | — |
| A12 | 单点完全不可达 | NOT-RUN | — |
| A13 | 本地断网/关机 | NOT-RUN | — |
| A14 | 身份与登录保护 | NOT-RUN | — |
| A15 | 刷新与入口一致 | NOT-RUN | — |
| A16 | 普通 LAN 设备访问 | NOT-RUN | — |
| A17 | 权限与客户端出口 | NOT-RUN | — |
| A18 | 用户与管理隔离 | NOT-RUN | — |
| A19 | INPUT/FORWARD 与规则共存 | NOT-RUN | — |
| A20 | 网段冲突与 DNS | NOT-RUN | — |
| A21 | 禁用/过期/重置 | NOT-RUN | — |
| A22 | 并发与替换 | NOT-RUN | — |
| A23 | 不合作/故障到期 | NOT-RUN | — |
| A24 | 伪造和重启门禁 | NOT-RUN | — |
| A25 | 本地权限 | NOT-RUN | — |
| A26 | 取消/切站点/重启 | NOT-RUN | — |
| A27 | 睡眠和换网 | NOT-RUN | — |
| A28 | 安装卸载 | NOT-RUN | — |
| A29 | 备份/升级回退 | NOT-RUN | — |
| A30 | 证书与时间 | NOT-RUN | — |
| A31 | 稳定与性能 | NOT-RUN | — |
| A32 | 双栈与地址淘汰 | NOT-RUN | — |
| A33 | 无 hairpin NAT | NOT-RUN | — |

## 需求追踪

| 需求 | 用例 | 状态 |
| --- | --- | --- |
| R01 | A01 | NOT-PASSED |
| R02 | A01, A02, A11, A33 | NOT-PASSED |
| R03 | A02, A04, A07, A08, A15, A27, A32 | NOT-PASSED |
| R04 | A05, A07, A10, A15, A30 | NOT-PASSED |
| R05 | A06, A07, A08, A09, A10, A11, A31 | NOT-PASSED |
| R06 | A03, A33 | NOT-PASSED |
| R07 | A01, A05, A10, A14, A15, A25, A30 | NOT-PASSED |
| R08 | A01, A06, A16, A19, A20, A31 | NOT-PASSED |
| R09 | A14, A17, A18, A19, A21, A22, A23, A24, A25, A32 | NOT-PASSED |
| R10 | A17, A18, A20, A26 | NOT-PASSED |
| R11 | A19, A28, A29 | NOT-PASSED |
| R12 | A08, A09, A12, A13, A20, A26, A27, A30, A31 | NOT-PASSED |
