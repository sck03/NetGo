# 各平台构建与 Releases 发布

2026-11-27 审查修复的构建和必要验证范围见 [交付记录](https://github.com/sck03/NetGo/blob/cc36472058c310b7812089df60abdcc5d513e625/docs/REFACTOR_REVIEW_2026-11-27.zh-CN.md)。共享管理页 DTO 使用 `npm run contracts`（等价于 `go generate ./internal/contracts`）生成；修改 Go 模型后更新已提交的 `packages/ui/models.generated.ts`，并运行 `go test ./internal/contracts` 与 `npm run build`。仅构建 Web 不要求现场生成，因为生成文件已入库。

清理允许清单包含 `.cache/review`、当前 audit/refactor 构建产物；`-IncludeTestData` 还包含审查探针和文本日志，`-IncludeToolchains` 包含审查使用的 Go/Node 及已下载 OpenSSL 源码，但 OpenSSL 存在未提交工作时拒绝删除。源码、可恢复用户配置和当前嵌入式 Web 页面不随构建缓存删除。`upstream-verified-*` 源码须先将变更保存到补丁并通过 `prepare-upstream.py` 完整核验，再单独清理，避免丢失未归档的补丁修改。

本地体积清理：在 Windows 项目根运行 `scripts/clean-workspace.ps1` 预览，追加 `-Apply` 执行。默认清除可重建的依赖与构建缓存；`-IncludeTestData` 增加安装器实验样例和旧清理统计；`-IncludeSDK` 增加 Android NDK、平台和构建工具，保留命令行管理器和许可。`-IncludeToolchains` 进一步移除项目内 Go、版本化 Node、完整 Android SDK，以及可重新下载的原生 JSON/OpenSSL/Wintun 依赖。各选项仅影响脚本允许清单，父子目录会去重，并检查路径归属、目录联接和使用中的构建进程。

完整清理示例：`./scripts/clean-workspace.ps1 -Apply -IncludeSDK -IncludeTestData -IncludeToolchains`。执行后先按 `upstream/versions.lock.json` 准备所需 Go/Node，再用 `npm ci` 恢复 Web 依赖。原生和 Android 构建脚本会重新下载相关依赖，需可用网络；已清除 AAR 时不要使用 `--skip-bind`。运行引擎、身份、站点配置、数据库、项目签名文件和嵌入服务器的当前 Web 页面均不属于自动清理范围；不要把安装目录或运行数据放入工具链目录。

macOS 构建脚本在原生构建前运行独立的连接协调器回归，覆盖取消已排队的自动恢复、安装提示不阻塞断开、断开先于随后重连。Windows 开发机不能执行这项 Swift/macOS 检查；以 macOS runner 的输出为准。

七个客户端/节点工作流保持独立。到 GitHub Actions 选择目标工作流，点击 **Run workflow** 后可以填写版本、架构和发布选项。

如果仍只看到分支选择，请刷新工作流页面并选择 main。具体入口见下表；参数定义位于 main 上的各工作流文件。

## 手动运行参数

| 参数 | 用途 | 默认行为 |
| --- | --- | --- |
| version | 程序及安装包版本，例如 `0.3.1`、`0.3.1-rc.1`；不加 `v` | 留空读取仓库根目录 VERSION，当前为 0.3.0-dev |
| architecture | 选择该平台已支持的架构 | 见下表 |
| publish | 构建和验证成功后上传到 Releases | false：只构建并保留 Actions 产物 |
| release_type | draft 草稿、prerelease 预发布、stable 正式版本 | prerelease |
| Android version_code | APK versionCode，发布更新时应递增 | 1 |
| Apple build_number | macOS/iOS Bundle 构建号 | 1 |
| iOS sdk | both、iphoneos 设备版、iphonesimulator 模拟器版 | both |

版本会传给程序内版本信息、Windows 文件属性、Android versionName、Apple Bundle 信息和包名。Apple 标准版本字段使用数字部分，NetGoBuildVersion 保留完整版本及预发布后缀。API v2/schema 6 与发行版本号分别管理。

自动 push 构建使用 VERSION 文件的版本，**不发布 Releases**。只有手动运行且勾选 publish 才进入具有 `contents: write` 权限的发布任务。失败的构建不进入发布。

LAN 服务端和 VPS 支持 `amd64` 与 `arm64`（64 位 ARM，不包括 armv7）。两种架构均原生构建并执行检查，ARM 使用 `ubuntu-24.04-arm` + Debian 13；本地 ARM 主机可运行 `bash scripts/build-linux-server.sh --arch arm64` 或 `bash scripts/build-vps.sh --arch arm64`。CGO／SQLite 及上游程序跟随原生架构，拒绝在 x86 上把产物改名为 ARM 包。包名、目录、发行元数据、下载入口和 ELF 校验保持一致。

## 架构与独立入口

| 目标 | 手动可选架构 | 构建脚本 | 工作流 |
| --- | --- | --- | --- |
| Windows | amd64 | scripts/build-windows-client.ps1 | [Windows client](https://github.com/sck03/NetGo/actions/workflows/build-windows-client.yml) |
| Linux 桌面 | amd64 | scripts/build-linux-client.sh | [Linux desktop client](https://github.com/sck03/NetGo/actions/workflows/build-linux-client.yml) |
| Debian 服务端 | amd64 / arm64 | scripts/build-linux-server.sh | [Linux server](https://github.com/sck03/NetGo/actions/workflows/build-linux-server.yml) |
| VPS 辅助节点 | amd64 / arm64 | scripts/build-vps.sh | [VPS auxiliary node](https://github.com/sck03/NetGo/actions/workflows/build-vps.yml) |
| Android | arm64（arm64-v8a） | scripts/build-android-client.py | [Android client](https://github.com/sck03/NetGo/actions/workflows/build-android-client.yml) |
| macOS | arm64、x86_64 | scripts/build-macos-client.py | [macOS client](https://github.com/sck03/NetGo/actions/workflows/build-macos-client.yml) |
| iOS/iPadOS | arm64；设备/模拟器分别构建 | scripts/build-ios-client.py | [iOS client](https://github.com/sck03/NetGo/actions/workflows/build-ios-client.yml) |

架构会在构建开始前校验。Windows 和 Linux 桌面当前只支持 amd64，LAN/VPS 节点支持 amd64 / arm64，Android 当前只支持 arm64；其他架构应先完成实现与验证再加入选项。macOS x86_64 使用官方 macos-26-intel runner，arm64 使用 macos-latest。

## 发布到 Releases

例：发布一个 `0.3.1-rc.1` 预发布版，选择版本号 `0.3.1-rc.1`、对应架构，勾选 publish，release_type 选 prerelease。发布后标签为 `v0.3.1-rc.1`。

正式发布时使用不含预发布后缀的版本，例如 `0.3.1`，release_type 选 stable。正式 Release 才能供在线安装器的 `releases/latest` 使用；草稿或预发布不会开启该入口。

各平台可以分别运行并加入同一个版本：

- 使用相同版本号、源码提交和 release_type。
- 同版本已有标签指向其他提交时拒绝发布，防止混入不同源码的程序。
- 同名文件摘要相同则复用；不同则保留已发布文件并报错，应使用新版本号。
- 不同平台使用各自的文件名和 `.sha256` 校验文件，独立发布不会相互覆盖公共校验清单。
- 已有 Release 的类型不会被另一个平台的默认选项改写。如需将草稿/预发布改为正式，可在 Releases 页面明确修改状态。
- 草稿也可由各平台分别追加产物；发布器通过有权限的 Releases 列表查找草稿，不会因草稿尚未公开而重复创建。

发布任务只上传该平台的运行包、必要的独立安装器、BUILDINFO/SBOM 和逐文件摘要。原始 App 目录、构建日志、开发报告及发布暂存清单留在 Actions，不上传 Releases。

LAN/VPS 在线安装分别需要 `netgo-server-installer-linux-<arch>` / `netgo-vps-installer-linux-<arch>`（`<arch>` 为 amd64 或 arm64） 及其 `.sha256`，以及对应运行 ZIP 和 ZIP 的 `.sha256`。若希望两类节点都在线安装，应将两个独立工作流发布到同一正式版本。

## 本地构建和计划

版本与架构也可从脚本参数设置。以下计划命令只展示参数，不安装工具或编译：

```powershell
./scripts/build-windows-client.ps1 -Version 0.3.1-dev -Arch amd64 -Plan
```

```sh
bash scripts/build-linux-client.sh --version 0.3.1-dev --arch amd64 --plan
bash scripts/build-linux-server.sh --version 0.3.1-dev --arch amd64 --plan
bash scripts/build-vps.sh --version 0.3.1-dev --arch amd64 --plan
python3 scripts/build-android-client.py --version 0.3.1-dev --arch arm64 --version-code 2 --plan
python3 scripts/build-macos-client.py --version 0.3.1-dev --arch x86_64 --build-number 2 --plan
python3 scripts/build-ios-client.py --version 0.3.1-dev --arch arm64 --sdk iphoneos --build-number 2 --plan
```

去掉 Plan/--plan 即执行构建。环境变量 NETGO_VERSION、NETGO_ARCH 可传入公共构建设置，脚本参数优先；具体架构仍以支持表为准。

Windows 输出 Setup 和绿色版，文件名包含 windows-amd64；日常仍只有 NetGo.exe。Linux 桌面包保留3个程序、desktop入口、摘要和许可证；LAN/VPS运行维护使用Go，最终用户不需要Python。Android输出调试及未签名APK，iOS输出模拟器ZIP/未签名设备IPA，macOS输出App ZIP。

## 工具链和验证范围

Actions 固定官方稳定版提交 SHA。当前锁定 Go 1.27.4、Node 26.13.1 LTS、OpenSSL 4.1.0、AGP 9.5.2、Gradle 9.9.2、Kotlin 2.4.21、Compose BOM 2026.11.01、SDK 37.2、NDK 30.0.16248370；Apple 使用稳定 Xcode。锁定输入见 upstream/versions.lock.json。

Windows 构建需要 UCRT64 GCC/CMake/Ninja 和 Python cryptography；Linux桌面需要 GTK4/OpenSSL/FFmpeg/libcurl、GStreamer core/base、gio-unix、X11/XTest 开发包，运行依赖与硬件插件见 [Linux 说明](../apps/linux/README.md)。Python用于开发侧构建、打包和发布，不是最终用户安装维护的依赖。

构建和发布成功不代表完整设备验收。未签名 APK/IPA 仍需签名；Apple 真机、跨设备远控、UAC/锁屏/多屏和长期运行状态见 [进度](PROGRESS.zh-CN.md)。安装使用说明见 [INSTALLERS](INSTALLERS.zh-CN.md)。

历史参数、产物及发布控制验证见 [工作流验证记录](https://github.com/sck03/NetGo/blob/cc36472058c310b7812089df60abdcc5d513e625/docs/evidence/2026-11-24-workflows/REPORT.zh-CN.md)。

2026-11-25 补记：Android 构建要求本机已安装 JDK 17（设置 JAVA_HOME），CI 通过 setup-java 明确提供。已移除多余的 Foojay 自动下载工具链插件，避免插件仓库解析失败阻断已有 JDK 的构建。其他锁定工具链不变。
