# deepseek harness shell

`deepseek harness shell` 是 [deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)
的跨平台桌面壳。它使用 Wails v3 和系统 WebView 启动 dsh 的 Web 模式，支持 macOS、
Windows 和 Linux。

桌面壳本身不实现 dsh 的产品界面。应用启动后会在本机拉起随包携带的 dsh 服务，等待服务
就绪，再把窗口导航到 `http://127.0.0.1:<随机端口>`。正式安装包已经包含目标平台的
Node.js 运行时和 dsh 生产依赖，用户不需要另行安装 Node.js，也不需要下载
deepseek-harness 源码。

## 功能

- 使用系统 WebView 提供原生桌面窗口。
- 通过 `--port 0` 自动选择空闲端口，避免固定端口冲突。
- 启动时显示内嵌状态页，服务就绪后自动进入 dsh Web 界面。
- 关闭桌面应用时回收 dsh 子进程及其进程树。
- 按目标系统和架构构建独立 payload，不混装其他平台的 Node 或原生依赖。
- macOS 支持启动检查、定时检查和菜单手动检查更新。
- CI 验证 payload，并实际启动包内 dsh、请求本地页面后才上传制品。

## 安装

从 [GitHub Releases](https://github.com/lunarroute26/deepseek-harness-shell/releases)
下载与系统匹配的制品：

| 平台 | 当前发布架构 | 制品 | 备注 |
| --- | --- | --- | --- |
| macOS | Apple Silicon (`arm64`) / Intel (`amd64`) | `-installer.dmg` | 要选择与 Mac 架构一致的文件 |
| Windows | x64 (`amd64`) | NSIS `-installer.exe` | 安装器包含 WebView2 bootstrapper |
| Linux | x64 (`amd64`) | `.AppImage` / `.deb` | DEB 面向带 GTK4、WebKitGTK 6.0 的发行版 |

Release 同时提供 `SHA256SUMS`。下载后可校验文件完整性：

```sh
sha256sum -c SHA256SUMS
```

macOS 当前 CI 生成的是 ad-hoc 签名应用，Windows CI 也未配置发行证书。系统可能显示来源或
签名警告；正式公开分发前仍应配置 Developer ID 签名、公证和 Windows Authenticode 签名。

## 工作原理

```mermaid
flowchart LR
    A[Wails 启动] --> B[显示内嵌启动页]
    B --> C[查找 Node 和 dsh 入口]
    C --> D[启动 dsh Web 模式]
    D --> E[解析 127.0.0.1 随机端口]
    E --> F[WebView 导航到本地服务]
    F --> G[应用退出时回收进程树]
```

包内或仓库入口由 Node.js 执行，参数为：

```text
node <dsh-entry> --profile web --host 127.0.0.1 --port 0
```

仅在回退到 `PATH` 中的全局命令时使用
`dsh web --host 127.0.0.1 --port 0`。

桌面壳从 dsh 标准输出解析就绪地址，最长等待 90 秒。`frontend/dist` 只是启动和错误状态页，
真正的业务界面由 dsh 本地服务提供。

正式包中的运行时目录如下：

```text
payload/
├── payload.json
├── runtime/
│   └── bin/node(.exe)
└── dsh/
    ├── package.json
    ├── pnpm-lock.yaml
    ├── lib/bin.js
    └── node_modules/
```

这里的 `node_modules` 是通过 `pnpm deploy --prod --frozen-lockfile` 得到的生产依赖闭包，
不是 deepseek-harness 开发仓库的完整依赖。构建脚本还会移除其他系统和架构的
`node-pty` 原生文件，并校验 Node 可执行文件的 PE、ELF 或 Mach-O 目标。

## 开发环境

- Go 1.25 或更高版本（CI 使用 Go 1.26）
- Node.js 24
- pnpm 10
- Wails CLI `v3.0.0-beta.8`
- 一份已构建的 deepseek-harness 源码仓库

安装 Wails CLI：

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.8
```

先在 deepseek-harness 仓库安装依赖并构建：

```sh
cd /path/to/deepseek-harness
pnpm install --frozen-lockfile
pnpm run build
```

## 本地开发

推荐显式设置 `DSH_REPO`，避免从错误的相邻目录或全局命令启动 dsh：

```sh
cd /path/to/deepseek-harness-shell
DSH_REPO=/path/to/deepseek-harness wails3 task dev
```

等价的 Wails 命令是：

```sh
DSH_REPO=/path/to/deepseek-harness \
  wails3 dev -config ./build/config.yml
```

开发模式会启动 `frontend/dev-server.mjs` 来提供已提交的 `frontend/dist`。这个目录必须保留，
因为生产构建通过 `go:embed` 把它嵌入可执行文件。

dsh 入口的查找顺序为：

1. `DSH_LAUNCH` 指定的完整命令。
2. `DSH_REPO` 或 `DSH_HOME` 指向的仓库。
3. 安装资源中的 `payload/dsh`。
4. 可执行文件旁的 `deepseek-harness` 目录。
5. 当前目录或父目录旁的 `deepseek-harness` 目录。
6. `PATH` 中的 `dsh` 命令。

仓库模式优先使用 `apps/cli/lib/bin.js`，其次回退到 `apps/cli/src/bin.ts`。源码入口依赖仓库
已有可用的 TypeScript 运行环境和已构建工作区包，因此日常开发应优先生成 `lib/bin.js`。

Node.js 的查找顺序为：

1. `DSH_NODE`。
2. 安装资源中的 `payload/runtime/bin/node(.exe)`。
3. 可执行文件旁的 `runtime/bin/node(.exe)`。
4. `PATH` 中的 `node`。

## 构建 payload

正式打包前，需要为每个目标系统和架构单独准备 payload。`DSH_NODE` 必须指向目标平台的
Node 可执行文件，不能把 macOS Node 放进 Windows 或 Linux 安装包。

```sh
wails3 task stage:payload \
  TARGET_OS=darwin \
  ARCH=arm64 \
  DSH_REPO=/path/to/deepseek-harness \
  DSH_NODE=/path/to/target/node
```

生成目录为 `build/payload/<os>-<arch>`。进一步验证生产闭包：

```sh
wails3 task verify:payload TARGET_OS=darwin ARCH=arm64

node scripts/smoke-payload.mjs \
  --root build/payload/darwin-arm64 \
  --server
```

Windows payload 使用 hoisted、无链接的物理依赖树，避免 NSIS 跟随 pnpm 链接形成递归问题；
Linux staging 会补入当前目标的 `node-pty` 编译产物；macOS 和 Windows 只保留匹配架构的
预编译文件。

## 平台打包

以下命令都要求对应的 `build/payload/<os>-<arch>` 已生成并通过验证：

| 目标 | 命令 | 输出 |
| --- | --- | --- |
| macOS | `wails3 task darwin:package:dmg ARCH=arm64` | `.app`、`.dmg` |
| Windows | `wails3 task windows:create:nsis:installer ARCH=amd64` | NSIS installer |
| Linux AppImage | `wails3 task linux:create:appimage ARCH=amd64` | `.AppImage` |
| Linux DEB | `wails3 task linux:create:deb ARCH=amd64` | `.deb` |

macOS payload 位于 `.app/Contents/Resources/payload`。Windows 将 payload 安装到 exe 旁，
Linux DEB 安装到 `/usr/local/bin/payload`，AppImage 则把 payload 放进 AppDir 后重新封装。

`wails3 task linux:package ARCH=amd64` 还会在本地尝试生成 RPM 和 Arch Linux 包，但当前
Release CI 只发布 AppImage 和 DEB。非 Linux 主机进行 Linux 构建需要 Docker 和本项目的
`wails-cross` 镜像：

```sh
wails3 task setup:docker
```

Linux 默认使用 GTK4 和 WebKitGTK 6.0。DEB 声明以下运行依赖：

- `libgtk-4-1`
- `libwebkitgtk-6.0-4`

## 验证

提交打包改动前，至少运行：

```sh
go test ./...
node --test scripts/*.test.mjs
node scripts/verify-packaging.mjs
```

`verify-packaging.mjs` 会检查产品名称、版本号、图标、payload 安装位置、更新菜单和 CI
契约。CI 还会静默安装 Windows NSIS、解包 Linux AppImage，并使用包内 Node 启动 dsh
服务进行真实请求。

## 更新与发布

应用内更新目前只在 macOS 启用，因为 Wails updater 可以替换整个 `.app`，从而同时更新
其中的 payload。macOS 的行为如下：

- 启动 5 秒后静默检查一次，有新版本时打开更新界面。
- 每 6 小时后台检查一次。
- 可从应用菜单 `Check for Updates...` 手动检查。
- macOS 应用内更新只选择同架构的 `.app.zip`，不会下载用于首次安装的 DMG。
- 从本项目 GitHub Releases 获取版本，并使用 `SHA256SUMS` 校验下载内容。

`updater.key.pub` 已嵌入应用，但当前 GitHub provider 使用摘要校验；该公钥是后续接入签名
manifest 或其他 provider 的预留。Windows 和 Linux 的 payload 位于可执行文件旁，当前更新器
不能原子替换整个目录，因此必须下载新安装包升级。

发布版本前，需要同步修改 `Taskfile.yml` 中的 `APP_VERSION` 和各平台元数据。运行
`node scripts/verify-packaging.mjs` 确认版本一致，再推送 `vX.Y.Z` 标签：

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

标签会触发四个原生构建任务：macOS arm64、macOS amd64、Windows amd64 和 Linux amd64。
Release job 汇总所有制品、生成 `SHA256SUMS` 并创建或更新 GitHub Release。手动运行 workflow
只上传 Actions artifacts，不创建 Release。

## 环境变量

| 变量 | 用途 |
| --- | --- |
| `DSH_REPO` | 指定 deepseek-harness 仓库根目录 |
| `DSH_HOME` | `DSH_REPO` 的备用名称 |
| `DSH_NODE` | 指定 Node.js 可执行文件 |
| `DSH_LAUNCH` | 使用自定义完整启动命令，支持双引号路径 |

`DSH_LAUNCH` 的优先级最高，适合调试自定义 dsh 构建；普通开发建议只设置 `DSH_REPO`。

## 日志与排错

dsh 的 stdout 和 stderr 写入：

- macOS / Linux：`$TMPDIR/deepseek-harness-dsh.log`
- Windows：`%TEMP%\deepseek-harness-dsh.log`

桌面壳日志写入 `os.UserConfigDir()/deepseek harness shell/deepseek-harness.log`，常见位置为：

- macOS：`~/Library/Application Support/deepseek harness shell/deepseek-harness.log`
- Windows：`%AppData%\deepseek harness shell\deepseek-harness.log`
- Linux：`~/.config/deepseek harness shell/deepseek-harness.log`

如果应用一直停留在“正在启动本地服务…”，按以下顺序检查：

1. 查看 `deepseek-harness-dsh.log` 中的实际启动命令和 dsh 错误。
2. 正式安装包检查 `payload.json` 与目标平台 Node 是否完整；开发模式确认 `DSH_REPO` 路径正确。
3. 确认 deepseek-harness 已执行 `pnpm install --frozen-lockfile` 和 `pnpm run build`。
4. 检查安全软件是否阻止包内 Node 或 dsh 子进程启动。
5. 启动超过 90 秒会被判定失败，具体原因和日志路径会显示在错误页。

## 目录结构

```text
.
├── main.go                  # Wails 应用、窗口、菜单和更新器
├── dsh.go                   # dsh 命令解析、启动、就绪检测和日志
├── lifecycle.go             # 启动页状态、重试和关闭流程
├── app_logger.go            # 桌面壳文件日志
├── proc_unix.go             # Unix 进程组回收
├── proc_windows.go          # Windows 进程树回收
├── frontend/dist/           # 被嵌入的启动和错误状态页
├── scripts/                 # payload staging、验证、冒烟测试和打包辅助
├── build/                   # Wails 配置、图标和三平台打包配置
├── Taskfile.yml             # 统一任务入口和版本号
└── .github/workflows/       # 三平台 CI 与 Release 流程
```

## 已知限制

- 项目锁定 Wails `v3.0.0-beta.8`；升级 Wails 前需要重新验证窗口、菜单、更新器和打包 API。
- macOS 最低版本为 12.0。
- Windows 和 Linux 暂不支持应用内更新，需要下载安装新版完整安装包。
- 进程被强制终止时无法保证执行子进程清理；正常关闭会回收 dsh 进程树。
- CI 当前未配置 macOS 公证或 Windows 发行签名。

## License

本项目使用 [MIT License](LICENSE)。deepseek-harness 及其依赖仍遵循各自的许可证。
