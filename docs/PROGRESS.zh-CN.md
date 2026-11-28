# NetGo 当前进度

更新：2026-11-28（Asia/Shanghai）。当前 **0.3.0-dev / API v2 / schema 9**，本轮基线 `bd581dd`。实际改动、验证和边界见 [当前重构记录](REFACTOR_CURRENT.zh-CN.md)，历史结果仅对应各自固定提交。

- 修复 iOS 状态 error 与操作失败混淆；Apple 控制响应有共享解析与回归样例。
- Unix IPC 增加受控残留回收和实例锁；Linux/macOS 服务生命周期独立成模块，支持退出后退避恢复。
- Android 视频/音频拆分，独立解码完成回调、有界队列、协商宽高展示及 PCM 缓冲复用；Android/Apple 音频设备故障降级为静音。
- 管理概览、设备/会话、远控授权统一区分读取错误与操作错误，轮询保留操作失败和编辑草稿，页面设计保持一致。
- schema 9 增加授权分页组合索引，历史游标支持索引范围查找；续租 SQL 贯通取消，现有撤权/网关事务边界保留。
- 主机文件命令线程懒启动，取消传到执行者，Windows 同步 I/O 可中断；旧媒体路径、进程编排和过时实验元数据已收敛。

本轮仅做必要定向验证：前端 11 项及类型/生产构建、相关 Go store/remote/session 回归和 vet、Unix IPC 交叉编译、C++ 主机语法/文件取消回归、Android 远控 Kotlin+Compose 编译检查、Python 发布元数据 12 项。不是全平台测试；Android 编译使用真实平台库和少量 App/Go 接口替身，Apple/Linux GUI 与移动真机未验证。

Node 26.13.1、Go 1.27.4。项目未部署生产，schema 1–8 明确拒绝，使用全新开发数据目录。体积清理与 Git main 推送结果见本轮交付说明。
