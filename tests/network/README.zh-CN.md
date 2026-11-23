# 验收证据工具

`run.py` 和 `evidence.py` 采集、校验和汇总实验结果，不自动修改防火墙或切换公网。完整用例见 `docs/NETWORK_ACCEPTANCE.zh-CN.md`。它们使用 Python 3.11+ 标准库；下方真实实验脚本还需要 cryptography，且会在明确授权的实验环境执行所述操作。

## 准备

```sh
python3 tests/network/run.py init --output /root/netgo-acceptance
```

生成环境模板和 A01–A33 的 NOT-RUN 记录。填写 `environment.json` 中的主机、版本、配置 SHA-256、网络拓扑和故障恢复步骤，再将 `kind` 设为 `real`、`ready` 设为 `true`。国内动态公网默认端口示例为控制 TCP 10443、Relay TCP/UDP 18443、STUN UDP 3478；按运营商和上级映射调整。自动化工具自测只能使用 `kind=local-automation`，不能审阅为真实验收 PASS。

## 采集与检查

每个实际用例用一份可复现的验证脚本完成操作、业务校验和恢复。下面的 `verify-a01.sh` 指实验者按 A01 编写的真实环境脚本，仓库不会以一个健康探测代替整个用例：

```sh
python3 tests/network/run.py collect --case A01 --environment /root/netgo-acceptance/environment.json \
  --output /root/netgo-acceptance/A01/runs/attempt-01 --timeout 600 \
  --attach /root/captures/a01-sanitized.pcap -- bash /root/lab/verify-a01.sh
python3 tests/network/run.py verify --input /root/netgo-acceptance/A01/runs/attempt-01
```

保留原始 stdout/stderr 字节、带分隔符的 verification.log、环境快照、附件摘要、开始/结束时间、命令和退出状态。超时返回 124，输出超限返回 125，程序不存在返回 127。默认每个输出流保留 16 MiB，可用 `--max-log-bytes` 调整；超限、超时及失败均保存结果，并停止本次命令及其子进程。长时间用例可将 `--timeout` 调整至 72 小时以上，上限七天。现有输出目录一律拒绝覆盖。

采集目录含真实网络细节，按私密证据保存。命令行和环境文件不得包含密码、令牌或私钥；发布前检查日志和附件。SHA-256 用于发现后续改动，不证明证据来源；需要可信归档时由实验者签名。

## 人工审阅与汇总

退出码零仅得到 REVIEW-REQUIRED。审核完整用例、身份、路径、权限、恢复和脱敏后，才记录 PASS；不足之处记录 FAIL 或保留未审阅。审阅结果绑定 `result.json` 摘要，改动后的证据必须重新采集，不能沿用旧 PASS。

```sh
python3 tests/network/run.py review --input /root/netgo-acceptance/A01/runs/attempt-01 \
  --status PASS --reviewer 实验者 --notes-file /root/review-a01.txt \
  --confirm case-complete --confirm identity --confirm path --confirm permissions \
  --confirm recovery --confirm redaction
python3 tests/network/run.py report --input /root/netgo-acceptance --output /root/netgo-acceptance/report-01
```

汇总包含全部 A01–A33、R01–R12 追踪及完整性错误。以最新一次执行为准；旧 PASS 不能覆盖后来的失败或未审阅结果。无执行证据为 NOT-RUN；自测为 AUTOMATION-ONLY；任何未通过项保持发行门槛 CLOSED。

## 恢复指标

将逐次真实测量保存为 JSON 数组，例如 `[{"path":"direct","fault":"wan","success":true,"business_seconds":8.3}]`，运行：

```sh
python3 tests/network/run.py metrics --input /root/recovery-samples.json
```

按数据路径和故障类型分别报告样本数、失败率、成功样本 P50/P95、最大值；少于 20 次不会标记达到样本目标。DNS 传播时间单独记录，不计入应用可控的恢复起点。数值汇总也不是验收结论。

工具回归：`python -B -m unittest discover -s tests/tools -v`。

## 真实实验脚本

这些脚本只用于独立实验站点。网络/运维脚本要求显式 `--run`，创建输出时拒绝覆盖已有目录；凭据从保护文件的标准输入或明确的本机凭据文件读取，不放到命令行。仅公开脱敏的 result.json，完整实验目录可能含设备状态、日志和恢复密码。

| 脚本 | 操作与证据 |
| --- | --- |
| live_api.py | 真实 TLS/Dex、签名、防重放、刷新、账号/资源、禁用/到期和清理；创建临时对象 |
| live_network.py | 三个独立网络/挂载命名空间，真实 TCP/UDP、双用户端口隔离、已有连接撤销、替换与冻结服务后的内核租约到期；最终清理命名空间及临时账号 |
| live_paths.py --mode relay | 阻断实验客户端全部 UDP 出站，验证本地中继的 TCP 承载、应用数据和拒绝规则 |
| live_paths.py --mode auxiliary-relay | 限定到一个明确公网 IPv4 VPS 的实验 SNAT 回程，阻断客户端全部 UDP 和直达地址，验证备用控制及 VPS 中继；结束时删除精确的临时 SNAT 规则 |
| live_paths.py --mode concurrency | 十个独立客户端、十个租约、并发内容校验及续租；遵守每账号认证限速，内存不足时拒绝启动 |
| live_operations.py | 仅带 NetGo 实验归属标记的 Linux：配置应用/恢复、加密备份提取校验、密码维护、产品升级/回退、故障镜像自动回退；会暂停受管服务 |
| live_windows.py | 当前 Windows 用户的真实安装服务与 C++ IPC；数据传输、端口拒绝、凭据重连、取消、路由/DNS 恢复；拒绝覆盖已有同名站点 |

本轮拓扑使用无 VPN 的独立目标命名空间，地址 198.19.240.2，HTTP 18080/18082、流式 TCP 18083、UDP 18081，程序为 `lab_target.py`。它不代表用户的生产 LAN。实验客户端阻断物理接口直接访问该目标，防止未经过隧道的流量造成假通过；每个核心都必须配置独立 NB_STATE_DIR。原生站点 CA 通过协议传递，测试客户端不借助 SSL_CERT_FILE 掩盖 Windows 信任差异。

示例入口（须先准备上述已授权拓扑及公共站点）：

```sh
python3 -B tests/network/live_api.py --site /etc/netgo/client-site.json --alias labadmin --password-stdin --output /root/lab/api.json < /root/lab/protected-password.txt
python3 -B tests/network/live_network.py --site /etc/netgo/client-site.json --alias labadmin --password-stdin --run --output /root/lab/network-01 < /root/lab/protected-password.txt
python3 -B tests/network/live_paths.py --site /etc/netgo/client-site.json --alias labadmin --password-stdin --mode relay --run --output /root/lab/relay-01 < /root/lab/protected-password.txt
```

公网 DDNS/NAT、独立 VPS、实际 443 出站限制、Windows 休眠/跨用户、其他 OS 版本及 72 小时运行仍需要分别执行完整用例。
