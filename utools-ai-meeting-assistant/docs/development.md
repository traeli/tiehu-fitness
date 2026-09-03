# 开发、调试与发布

## 1. 环境要求

- Node.js 20.19+。
- npm 10+。
- uTools 6.1+ 及“uTools 开发者工具”插件。
- 麦克风权限；macOS 系统音频还需要“屏幕与系统音频录制”权限。
- macOS 13+ 与 Xcode Command Line Tools，或 Windows 10+ 与 Visual Studio Build
  Tools 的“使用 C++ 的桌面开发”工作负载。
- 真实联调时可访问 core-service 的 HTTPS/WSS 地址。

## 2. 安装

```bash
npm install
cp .env.example .env.local
```

默认 `.env.example` 使用 Mock API。

## 3. 浏览器开发

```bash
npm run dev
```

浏览器 fallback 支持：

- 麦克风采集。
- 模拟转写和纪要。
- Blob 下载 Markdown。

浏览器不能代表真实 uTools preload、生命周期、系统音频和保存对话框行为。

### 3.1 浏览器连接本地后端

先从仓库根目录启动 PostgreSQL、Redis、Vision 和 Core：

```bash
make infra-up
make run-vision
make run-core
```

然后在当前前端目录运行：

```bash
npm run dev:api
```

访问 `http://127.0.0.1:5173/`，页面会提供邮箱注册/登录入口。此模式由
`.env.api` 显式开启，Vite 把 `/api` 代理到 `http://127.0.0.1:8000`，避免为仅
本地开发放宽 core 的跨域策略。

`dev:api` 使用合成静音 PCM，不申请浏览器麦克风权限。它仍按后端音频约束每
200ms 发送 6400 字节 PCM16LE，适合验证：

- Web 注册/登录和 Bearer token；
- 创建会议和一次性 vision ticket；
- WebSocket `start/session_ready`；
- 二进制音频、累计 ACK、心跳和百炼 ASR（静音通常没有文本片段）；
- `finish/session_finished` 和 core `:stop`。

B-800 已实现 vision outbox 到 core 的完成回调。`session_finished` 后，最终片段会先
可靠写入 core，随后会议进入 `completed/succeeded`，额度按已接受 PCM 时长幂等结算
并立即释放未使用预占；同一账号可以继续创建下一场会议。页面仍可能短暂显示
“正在生成纪要”，直到下一次查询取得完成状态。

## 4. uTools 调试

日常开发使用热更新模式：

```bash
npm run dev:utools
```

首次在 uTools 开发者工具中选择：

```text
<project>/plugin/plugin.json
```

保持命令运行后，在 uTools 搜索框执行“会议助手”打开插件。不要在 Chrome 地址栏打开
`http://127.0.0.1:5173/`：普通浏览器没有 preload，无法启动 ScreenCaptureKit/WASAPI。
页面右上角应显示 `API · main`；若显示 `API · browser`，说明打开方式不正确。

页面修改由 Vite 热更新。`preload.js` 或原生辅助程序变更后，需要退出并重新打开
插件；普通 Vue/TypeScript/CSS 修改无需重新接入。会议开始后页面会显示“电脑音频”
和最终“发送音频”两条实时电平，播放电脑声音即可判断系统音频是否进入。

uTools 插件登录还要求 core 持有当前开发者插件应用的服务端凭据。先在 uTools
开发者工具的插件应用中取得插件 ID，并通过重置取得服务端 secret；再从启动 core
的同一个终端注入环境变量：

```bash
export UTOOLS_PLUGIN_ID='开发者插件应用中的插件 ID'
export UTOOLS_PLUGIN_SECRET='开发者插件应用中重置取得的 secret'
make run-core
```

不要使用 `plugin.json` 的 `name` 代替平台分配的插件 ID，也不要把 secret 写入
`configs/core.yaml`、前端 `.env` 或提交到 Git。修改凭据后需重启 core，并重新打开
插件以获取新的 uTools 临时令牌。若未注入这两个变量，core 会返回
`UTOOLS_AUTH_NOT_CONFIGURED`。

该命令使用 `.env.real`：页面请求同源 `/api`，由 Vite 转发至
`http://127.0.0.1:8000`，同时保持真实系统音频和麦克风。不要把热更新模式改为
`.env.utools-local` 的直连地址，否则 `127.0.0.1:5173 -> 127.0.0.1:8000` 会触发
CORS `OPTIONS` 预检。

本地 uTools 直接连接已部署的线上服务时运行：

```bash
npm run dev:utools:online
```

该命令读取 `.env.production` 并连接 `https://dsh.nutrilens.cloud`，仍从源码目录的
`plugin/plugin.json` 加载 Vite 热更新页面。它不会切换到 Mock 或合成音频。

需要生成可打包目录时执行：

```bash
npm run build
```

该构建命令读取 `.env.utools-local`，生成的静态插件直接连接
`http://127.0.0.1:8000`、使用真实系统音频/麦克风且关闭 Mock。静态 uTools 插件不从
`127.0.0.1:5173` 加载，因此与上面的热更新代理模式不同。

`npm run build` 会先执行 `npm run native:build`：

- macOS 同时构建 `darwin-arm64` 和 `darwin-x64` ScreenCaptureKit 组件并进行本地
  ad-hoc 签名；正式发布应替换为开发者签名并完成干净机器权限验证。
- Windows 必须在目标架构的 Visual Studio Developer PowerShell 中执行，构建
  `win32-x64` 或 `win32-arm64` WASAPI Loopback 组件。
- 生成物位于 `plugin/native/<platform>-<arch>`，Vite 会复制到 `dist/native`。

uTools 安装后可能把发布目录封装为 ASAR，操作系统不能直接执行插件包内的原生文件。
preload 在 uTools 环境中始终读取随包发布的对应平台文件，按内容哈希释放到
`utools.getPath("userData")/meeting-assistant/native/<sha256>`，校验一致后再启动。不要
改为从网络动态下载并执行辅助程序；这会扩大供应链攻击面，并使离线安装包与实际运行
代码不一致。

正式跨平台 `.upx` 必须合并各平台构建产物后再执行最终 Vite 构建。不能在 macOS
上把未编译的 Windows C++ 源码当成 Windows 已验收。

无需创建会议即可检查真实系统音频。先播放电脑声音，再运行：

```bash
npm run native:smoke
```

命令采集 5 秒并只打印 PCM 分片数、峰值和 RMS，不保存音频、不访问 API。分片为零
表示原生链路未建立；有分片但峰值接近零表示默认输出设备或权限仍不正确。

然后在 uTools 开发者工具中选择：

```text
<project>/dist/plugin.json
```

`npm run native:smoke` 只用于排查权限、默认输出设备或原生组件问题，不是每次会议
测试的前置步骤。

## 5. 真实后端联调

生产环境已经由 `.env.production` 固定为：

```dotenv
VITE_API_BASE_URL=https://dsh.nutrilens.cloud
VITE_USE_MOCK_API=false
VITE_USE_SYNTHETIC_AUDIO=false
```

运行 `npm run build:production`。生产 API 地址属于公开客户端配置，不包含服务端凭据；
uTools plugin secret、百炼和 DeepSeek API Key 仍然只能保留在服务端。生产构建会删除
`dist/plugin.json` 的 `development` 字段，uTools 开发者工具测试构建目录时不会再访问
本地 Vite 端口。

检查：

1. uTools 已登录且能获取服务端临时令牌。
2. Vision 真实模式启动日志包含 `Bailian ASR startup probe passed` 和固定语音识别结果。
3. core-service 已实现 `/v1/auth/utools/login`。
4. 创建会议响应包含短期 WebSocket ticket 和音频约束。
5. HTTPS 证书、WSS、CORS/Origin 和网关升级配置正确。
6. 后端错误使用稳定 reason。

## 6. 质量检查

```bash
npm run typecheck
npm run test
npm run build
```

真实录音或桌面能力变更还要手工验证：

- uTools 主窗口和分离窗口。
- 麦克风允许、拒绝和撤销。
- macOS“屏幕与系统音频录制”允许、拒绝和重启生效。
- Windows 默认输出设备、蓝牙耳机、设备切换和 WASAPI 服务重启。
- 隐藏、退出、进程被结束和系统休眠。
- Wi-Fi 断开、恢复和服务端重启。
- 深色模式。
- 30 分钟以上内存与 CPU。
- Markdown 保存取消、覆盖和无权限目录。

## 7. 打包

正式打包前：

1. 使用生产 API 地址并关闭 Mock。
2. 运行全部质量检查。
3. 检查 `dist/plugin.json`、`dist/preload.js`、logo 和相对资源路径。
4. 确认 `dist` 不包含 `.env`、source secret、会议样本或用户数据。
5. 在 uTools 开发者工具中使用 `dist/plugin.json` 打包 `.upx`。
6. 分别在干净 macOS 与 Windows 用户环境安装并完成一次端到端会议。

## 8. 开发规则

- 不在组件中直接调用外部 API。
- 不在 Renderer 暴露任意 Node 能力。
- 当前压缩录音在 Renderer 中有明确的 256 MB 上限，并在正常停止时落到 uTools 本地文件；后续改为录制过程流式 spool 后，不再累计整场压缩音频。
- 不记录 token、音频、转写或纪要正文。
- 不通过强制类型断言信任服务端响应。
- 新增闭集状态时同步更新 domain、映射、UI 和测试。
- 每个手工 timer、MediaStream、WebSocket 都有 owner 和 cleanup。
- 修改 WebSocket 消息必须更新版本和 `api-contract.md`。

## 9. 官方参考

- [第一个 uTools 插件](https://www.u-tools.cn/docs/developer/basic/first-plugin.html)
- [插件目录结构](https://www.u-tools.cn/docs/developer/information/file-structure.html)
- [plugin.json](https://www.u-tools.cn/docs/developer/information/plugin-json.html)
- [preload](https://www.u-tools.cn/docs/developer/information/preload-js/preload-js.html)
- [事件生命周期](https://www.u-tools.cn/docs/developer/api-reference/utools/events.html)
- [用户临时令牌](https://www.u-tools.cn/docs/developer/api-reference/utools/user.html)
- [屏幕捕获](https://www.u-tools.cn/docs/developer/api-reference/utools/screen.html)
- [FFmpeg](https://www.u-tools.cn/docs/developer/api-reference/utools/ffmpeg.html)
