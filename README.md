# Go Music DL Fork: Windows Desktop Converter

这是一个基于 [guohuiyuan/go-music-dl](https://github.com/guohuiyuan/go-music-dl) 的二次开发版本。上游项目提供聚合音乐搜索、下载、播放、歌单/专辑解析和本地音乐管理能力；本 fork 在尽量不改动原有 Web 业务的前提下，补齐 Windows 桌面体验，并新增独立的批量音频转换模块。

当前版本的重点不是重写前端，也不是把项目迁移到 Electron，而是复用上游已有的 Go Web Server、Web 模板和 `desktop_go` WebView 桌面壳，在其上增加：

- Windows 桌面 EXE：自动启动本机 Web 服务并打开 WebView2 窗口。
- 批量音频转换：FLAC / WAV / M4A / AAC / OGG / WMA 转 MP3。
- 任务队列：固定 3 个并发 Worker，避免一次性启动大量 FFmpeg。
- 实时进度：SSE 推送转换状态、进度和错误。
- Windows 原生选择器：选择音频文件、音频目录和 MP3 输出目录。
- Windows EXE 图标：由 `internal/web/templates/static/icon/favicon.ico` 生成。
- 桌面内置 FFmpeg：Windows EXE 启动时自动释放 `ffmpeg.exe` 和 `ffprobe.exe`。

## 与上游的关系

本 fork 遵守“先保留原功能，再做增强”的改造原则：

| 能力 | 状态 |
| --- | --- |
| 多平台搜索、试听、下载 | 保留上游实现 |
| Web 播放页、歌单、专辑、本地音乐 | 保留上游实现 |
| TUI 模式 | 保留上游实现 |
| Cookie / 管理员认证 / 设置接口 | 保留上游实现 |
| `desktop_go` WebView 桌面架构 | 保留，仅调整构建资源 |
| FFmpeg 批量转 MP3 | 本 fork 新增 |
| 转换任务队列、取消、重试、SSE | 本 fork 新增 |
| Windows 桌面原生文件选择器 | 本 fork 新增 |
| Windows EXE 图标 | 本 fork 调整 |

上游可能持续更新。同步上游时，应优先保留 `internal/converter`、`internal/nativedialog`、`internal/mediaassets`、`internal/web/converter.go` 和转换 UI 的改动，再合并上游对音乐源、下载链路和 Web 界面的更新。

## 快速开始

### Windows 桌面版

桌面版推荐使用 Windows 10/11 和 WebView2 Runtime。双击 EXE 后，程序会启动只监听 `127.0.0.1:37777` 的本地服务，然后打开桌面窗口。

从源码构建：

```powershell
git clone https://github.com/<your-account>/go-music-dl.git
cd go-music-dl
go generate ./winres
go build -trimpath -ldflags="-H windowsgui -s -w" -o music-dl-desktop-go.exe ./desktop_go
```

也可以执行：

```powershell
.\package_go.bat
```

生成的文件是 `music-dl-desktop-go.exe`。前端模板、静态资源、转换脚本和 Windows FFmpeg 资源都会嵌入 EXE；首次启动时，内置 FFmpeg 会释放到：

```text
%LOCALAPPDATA%\MusicDL\bin\
```

### Web 模式

```powershell
go build -trimpath -ldflags="-s -w" -o music-dl.exe ./cmd/music-dl
.\music-dl.exe web
```

默认访问：

```text
http://127.0.0.1:8080/music/
```

普通 Web 模式的搜索、播放、下载等公开能力保持上游行为。涉及 Cookie、系统设置等敏感配置时仍需要管理员登录；首次初始化管理员账号时，启动终端会输出一次性 Token。

### TUI 模式

```powershell
.\music-dl.exe -k "关键词"
```

### Docker

仓库中的 `docker-compose.yml` 沿用上游 Web 服务方式：

```bash
docker compose up -d
```

Docker 镜像内使用系统安装的 FFmpeg；本 fork 新增的 Windows EXE 内置 FFmpeg 逻辑主要面向 Windows 桌面发行物。

## 音频转换

在桌面窗口右侧工具栏点击 **音频转换** 打开转换面板。它位于现有 Web UI 内，不改变下载页面的使用方式。

### 支持格式

| 输入 | 输出 |
| --- | --- |
| FLAC | MP3 |
| WAV | MP3 |
| M4A | MP3 |
| AAC | MP3 |
| OGG | MP3 |
| WMA | MP3 |

MP3 编码器为 FFmpeg `libmp3lame`。比特率支持 `128k`、`192k`、`256k`、`320k`，默认 `320k`。

### 使用流程

1. 点击 **添加文件** 或 **添加文件夹**。
2. 选择比特率和输出目录。
3. 选择重名策略：
   - **自动改名**：输出 `A.mp3` 已存在时生成 `A (1).mp3`。
   - **跳过已存在文件**：保留已有 MP3，不覆盖。
4. 点击 **开始转换**。
5. 在任务列表查看状态、进度、取消或重试。

默认输出目录是源文件所在目录。指定输出目录后，程序会自动创建目录；如果目录权限不足或 FFmpeg 启动失败，错误会写回任务记录。

转换时会尽量保留原音频元数据，包括标题、歌手、专辑、封面等。FFmpeg 参数使用 `-map_metadata 0`，并将可用的内嵌封面流复制到 MP3。

## 任务模型

任务状态包括：

| 状态 | 含义 |
| --- | --- |
| `pending` | 已入队，等待 Worker |
| `running` | FFmpeg 正在转换 |
| `success` | 转换成功 |
| `failed` | 转换失败，`error` 保存原因 |
| `cancelled` | 用户取消 |

核心约束：

- 固定 3 个 Worker，最多同时运行 3 个 FFmpeg 进程。
- 单次最多创建 1000 个任务。
- 转换请求立即返回，不会等待音频转完。
- FFmpeg 通过 `context` 启动；取消任务会同时终止对应进程。
- 任务保存在内存中，应用重启后不恢复。
- 进度来自 FFmpeg `out_time_us`，表示输入时间轴已处理百分比；任务结束时固定为 100%。

## 转换 API

Web 路由默认挂载在 `/music` 下。以下接口在普通 Web 模式受管理员认证保护；桌面本地模式下由 Go 后端按启动模式放行。

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/music/converter/tasks` | 创建批量任务 |
| `GET` | `/music/converter/tasks` | 获取任务列表 |
| `GET` | `/music/converter/tasks/:id` | 获取单个任务 |
| `POST` | `/music/converter/tasks/:id/cancel` | 取消任务 |
| `POST` | `/music/converter/tasks/:id/retry` | 重试失败/取消任务 |
| `DELETE` | `/music/converter/tasks/:id` | 删除已完成任务 |
| `GET` | `/music/converter/events` | SSE 任务事件流 |
| `GET` | `/music/converter/picker/files` | 打开 Windows 文件选择器 |
| `GET` | `/music/converter/picker/folder` | 打开 Windows 目录选择器 |
| `POST` | `/music/converter/files/from-folder` | 展开 Windows 目录中的音频文件 |

创建任务示例：

```json
{
  "files": [
    "D:/Music/晴天.flac",
    "D:/Music/七里香.m4a"
  ],
  "format": "mp3",
  "bitrate": "320k",
  "outputDir": "D:/Music/MP3",
  "conflictPolicy": "rename"
}
```

SSE 使用命名事件：

```js
const source = new EventSource("/music/converter/events");
source.addEventListener("task", event => {
  const task = JSON.parse(event.data);
  console.log(task.id, task.status, task.progress);
});
```

## 实现结构

```text
cmd/music-dl/
  CLI / Web / TUI 入口

desktop_go/
  Windows WebView2 桌面入口

internal/appshell/
  桌面本地服务健康检查和启动地址

internal/web/
  Gin 路由、Web 模板、认证、静态资源
  converter.go：转换 HTTP Handler
  templates/static/js/converter.js：转换 UI

internal/converter/
  转换任务模型、队列、Worker、FFmpeg 调用

internal/nativedialog/
  Windows 原生文件 / 目录选择器

internal/mediaassets/
  Windows FFmpeg / ffprobe 嵌入与释放

winres/gen/
  生成 Windows 图标、版本和清单资源
```

转换调用链保持独立：

```text
转换页面（现有模板 UI）
        ↓
Gin HTTP Handler
        ↓
converter.Service
        ↓
Task Queue / 3 Workers
        ↓
FFmpeg
        ↓
进度事件 / SSE / 任务列表
```

当前 Web UI 继承上游的 Go 模板与原生 JavaScript 结构，不是独立 Vue 工程。README 和注释中提到“前端”时，指的是 `internal/web/templates` 中的现有 Web UI。

## Desktop 与安全边界

桌面模式沿用上游 `StartDesktop` 思路：

1. `desktop_go` 启动进程。
2. `internal/appshell` 准备内置 FFmpeg 并启动 Web Server。
3. Web Server 只监听 `127.0.0.1`。
4. WebView2 打开 `http://127.0.0.1:37777/music/`。
5. 本地桌面模式关闭管理员认证流程，避免用户无法拿到一次性 Token。

普通 Web 模式认证没有删除：

- 搜索、播放、下载等原有公开能力保持不变。
- Cookie 和系统设置仍受管理员登录保护。
- 转换配置接口挂在受保护的配置路由组下。
- 浏览器中的桌面状态不通过 `?desktop=true`、Header 或 `localStorage` 决定。
- 原生文件选择器只在服务监听 `127.0.0.1` / `::1` 时注册，并且只响应回环客户端。

请勿把桌面模式端口直接通过反向代理暴露到公网；它按本机桌面应用设计，认证模型与公网 Web 部署不同。

## FFmpeg

Windows 桌面 EXE 内嵌了：

```text
internal/mediaassets/binaries/windows/amd64/ffmpeg.exe
internal/mediaassets/binaries/windows/amd64/ffprobe.exe
internal/mediaassets/binaries/windows/amd64/LICENSE.txt
```

启动时释放到 `%LOCALAPPDATA%\MusicDL\bin\`，随后通过现有 `MUSIC_DL_FFMPEG` / `MUSIC_DL_FFPROBE` 环境变量复用上游的媒体工具解析逻辑。

如果没有嵌入资源，程序会按以下顺序查找：

1. `MUSIC_DL_FFMPEG` / `MUSIC_DL_FFPROBE` 指定路径。
2. 系统 `PATH`。

缺少 FFmpeg 不会阻止程序启动，但音频转换会失败；本地音乐信息探测和下载内嵌元数据会按上游逻辑降级。发布包含 FFmpeg 的版本时，请保留并随程序分发对应的 `LICENSE.txt`。

## 数据目录

默认数据仍写入工作目录：

```text
data/settings.db
data/downloads/
data/video_output/
```

桌面版建议放在有写权限的用户目录使用；如果 EXE 位于只读位置，请在设置中把下载目录改为用户可写路径。

## 开发与测试

推荐 Go 版本以 `go.mod` 为准，当前为 Go 1.25.1。

格式化和核心测试：

```powershell
gofmt -w ./internal/converter ./internal/mediaassets ./internal/nativedialog ./internal/appshell ./internal/web/converter.go ./winres/gen
go test ./cmd/music-dl ./core ./internal/appshell ./internal/cli ./internal/converter ./internal/mediaassets ./internal/nativedialog ./internal/web
```

转换模块测试覆盖：

- 批量任务并发不超过 3。
- 等待中任务取消。
- 无效路径/不支持的输入变成失败任务。
- 输出重名策略。
- WAV 到 MP3 的真实 FFmpeg 集成测试（本机无 FFmpeg 时自动跳过）。

重新生成 Windows 资源：

```powershell
go generate ./winres
```

资源生成器会直接读取 `favicon.ico`，将其作为资源 ID `2` 嵌入，同时写入版本信息和 Windows manifest。

## 与上游同步

```bash
git remote add upstream https://github.com/guohuiyuan/go-music-dl.git
git fetch upstream
git merge upstream/main
```

同步后重点检查：

1. `internal/web/server.go` 中转换路由是否仍挂载。
2. `bindAuthMiddleware` 的桌面/普通 Web 行为是否变化。
3. `templates/pages/index.html` 和 `partials/modals.html` 的转换入口是否仍存在。
4. `desktop_go/webview_windows.go` 的启动流程是否仍兼容 `internal/appshell`。
5. `go generate ./winres` 后重新构建 Windows EXE。

## License

本项目继承上游的 AGPL-3.0 许可证；对上游代码的修改同样受 AGPL-3.0 约束。感谢上游作者和所有贡献者。

内嵌的 FFmpeg / ffprobe 二进制遵循其自身许可证，详见 `internal/mediaassets/binaries/windows/amd64/LICENSE.txt`，发布时不得移除该授权文件。
