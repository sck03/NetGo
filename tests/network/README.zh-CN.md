# 验收证据工具

这些工具采集、校验和汇总实验结果，不自动修改防火墙、切换公网或宣布网络通过。完整用例见 `docs/NETWORK_ACCEPTANCE.zh-CN.md`。Python 3.11+，仅用标准库。

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
