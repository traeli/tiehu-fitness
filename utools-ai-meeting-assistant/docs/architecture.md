# 技术架构

## 1. 架构结论

插件采用：

- Vue 3：界面与组合式组件。
- TypeScript strict mode：领域状态、API 和桌面桥接类型。
- Vite：开发、构建和静态资源复制。
- Pinia：单一会议会话状态容器。
- uTools `plugin.json`：插件入口与搜索指令。
- CommonJS `preload.js`：uTools、Node 和文件系统白名单桥接。
- ScreenCaptureKit/WASAPI + Web Audio + MediaRecorder：跨平台系统音频与麦克风单轨混音、实时 PCM 和本地压缩录音。
- WebSocket：实时音频与转写片段。
- HTTP：鉴权、会议生命周期、历史、纪要、上传和导出。

不选择 Electron 独立应用，因为 uTools 已提供宿主和 Electron 能力；不再打包第二套桌面壳。不在插件内运行 Whisper 或 LLM，以控制启动时间、包体和资源使用。

## 2. uTools 运行模型

官方规范要求插件以 `plugin.json` 为核心入口，并以编译后的 HTML/CSS/JavaScript 运行。`preload` 是独立的 CommonJS 文件，可以使用 Node/Electron 能力。

本项目源码中的 `plugin/` 被 Vite 作为 `publicDir` 原样复制到 `dist/`：

```text
plugin/plugin.json       -> dist/plugin.json
plugin/preload.js        -> dist/preload.js
plugin/assets/logo.svg   -> dist/assets/logo.svg
index.html + src/*       -> dist/index.html + dist/assets/*
```

uTools 开发者工具和离线打包始终选择 `dist/plugin.json`，不选择项目根目录。

官方资料：

- [插件目录结构](https://www.u-tools.cn/docs/developer/information/file-structure.html)
- [plugin.json](https://www.u-tools.cn/docs/developer/information/plugin-json.html)
- [preload](https://www.u-tools.cn/docs/developer/information/preload-js/preload-js.html)
- [uTools 生命周期事件](https://www.u-tools.cn/docs/developer/api-reference/utools/events.html)

## 3. 分层与依赖方向

```text
Vue Components
      |
Pinia Meeting Store
      |
Application Services
      |
Domain Types / Rules
      ^
      |
Infrastructure Adapters
  |- HTTP API
  |- WebSocket transcription
  |- Meeting audio mixer
  |- uTools preload bridge
```

依赖原则：

- 组件只展示状态并发出用户意图，不直接调用 `fetch`、WebSocket 或 MediaRecorder。
- Store 编排一次会议生命周期，不包含 Provider 或平台实现细节。
- Domain 只包含类型、状态和纯规则，不依赖 Vue/uTools。
- Infrastructure 实现外部能力，并在不改变上层的情况下替换。
- Preload 不导入 Vue 应用代码，保持可审查的 CommonJS 文件。

## 4. 目录设计

```text
utools-ai-meeting-assistant/
├── plugin/
│   ├── plugin.json
│   ├── preload.js
│   └── assets/
├── src/
│   ├── application/
│   ├── components/
│   ├── domain/
│   ├── infrastructure/
│   │   ├── api/
│   │   ├── audio/
│   │   ├── desktop/
│   │   └── realtime/
│   ├── stores/
│   ├── types/
│   ├── App.vue
│   └── main.ts
├── docs/
├── index.html
├── vite.config.ts
└── package.json
```

新增能力必须放到拥有它的边界，避免形成一个包含录音、API、UI 和本地文件的万能 service。

## 5. 桌面安全边界

Renderer 默认只使用浏览器能力。需要 uTools/Node 的操作通过 `window.meetingDesktop` 白名单完成：

- 读取非敏感运行环境信息。
- 获取 uTools 用户和一次性服务端临时令牌。
- 弹出保存对话框并写 Markdown。
- 发送安全通知。

禁止直接暴露：

- 任意 `fs` 读写。
- 任意 shell/child process 执行。
- 任意路径删除。
- 后端密钥或 uTools 服务端密钥。
- 可以绕过用户选择的静默文件写入。

系统音频或 FFmpeg 接入时，应新增用途明确的方法和参数校验，不能把 `exec(command)` 暴露给 Renderer。

## 6. 鉴权架构

V1 推荐使用 uTools 用户临时令牌实现免注册登录：

```text
Renderer
  -> preload.getUserServerTemporaryToken()
  -> POST /v1/auth/utools/login
  -> core-service 调用 uTools 服务端验证
  -> 返回本项目 access token / refresh token
```

uTools 服务端签名密钥只能保存在后端。插件只持有短期临时令牌和业务访问令牌。当前骨架把 access token 保存在内存中的 `ApiClient`，未实现 refresh token 持久化。

后端需要在现有 core 用户身份域增加 `utools_identity`，不能把昵称当用户唯一标识。

官方资料：

- [uTools 用户与临时令牌](https://www.u-tools.cn/docs/developer/api-reference/utools/user.html)
- [uTools 服务端 API](https://www.u-tools.cn/docs/developer/api-reference/server.html)

## 7. 音频架构

### 7.1 抽象

业务层依赖 `AudioRecorder`：

```ts
interface AudioRecorder {
  start(onChunk: AudioChunkHandler, onFailure?: AudioFailureHandler): Promise<void>
  stop(): Promise<void>
}
```

当前 `BrowserMeetingAudioRecorder` 通过 preload 窄接口启动平台原生辅助程序：macOS
使用 ScreenCaptureKit，Windows 使用 WASAPI Loopback。辅助程序只允许输出版本化帧协议
中的 48kHz/mono/PCM16LE，不接受 Renderer 提供的命令或文件路径。Renderer 通过
AudioWorklet 把系统 PCM 转为 Web Audio 源，再与可选麦克风混成单轨。同一混音同时
输出给实时 16kHz 单声道 PCM 和 MediaRecorder 本地压缩录音；不采集或保存屏幕画面。

### 7.2 内存与落盘

实时 PCM 分片生成后立即发送，不在 Renderer 中累计实时转写队列。当前留存录音由 MediaRecorder 压缩并设置 256 MB 上限，会议正常停止后通过 preload 文件适配器写入“文稿/铁虎AI会议助手/录音”；本地索引只保存受校验的会议与文件元数据。长会议的录制过程流式 spool 和崩溃恢复仍是下一阶段可靠性工作。

录音历史以本地索引为入口：列表阶段只读取元数据，用户点击后才读取对应音频文件并生成临时播放 URL；最终转写通过 core 的会议详情和分页转写接口读取。Renderer 不接收任意文件路径，preload 只允许访问由 UUID 和受控 MIME 类型推导出的录音文件名。

不得为了实现最终上传而使用 `chunks.push(blob)` 保存数小时音频。

### 7.3 编码协商

音频拆成两个 adapter：实时 ASR 使用 AudioWorklet 输出 PCM16LE/16kHz/mono；留存录音可以继续使用 MediaRecorder 和本地 spool。后端创建会议返回本次实际音频约束，实时 adapter 必须严格匹配。

当前骨架的 `audio/webm;codecs=opus` 不能用于 Paraformer 实时链路，因为百炼要求 Ogg 封装的 Opus。不得把 WebM/Opus 伪装成 `opus` 或 PCM 透传。最终持久化格式由后端和平台适配验证后确定。

### 7.4 跨平台系统音频

当前 uTools 7.8.0 使用的 Electron 22 无法可靠提供 macOS loopback 音轨，因此系统
音频不能继续依赖 `desktopCaptureSources`。平台变化点封装在同一个原生协议后：

- macOS 13+：ScreenCaptureKit `.audio` 输出，需要“屏幕与系统音频录制”权限；
- Windows 10+：默认渲染端点的 WASAPI shared-mode loopback，不依赖“立体声混音”设备；
- 两端均下混/重采样为 48kHz 单声道 PCM16LE，每 100ms 一个受长度校验的二进制帧；
- preload 单实例管理进程、启动超时、停止握手和异常退出，Renderer 不接触 `child_process`。

系统 PCM 与麦克风按固定增益混为单轨，匹配当前 ASR 的单声道协议。无数据、权限
拒绝或原生进程退出必须返回明确错误并停止会议，不能回退成“成功的静音录制”。
必须在目标 uTools 版本、腾讯会议、飞书、Zoom 和浏览器会议中分别覆盖耳机与扬声器。

官方资料：

- [Apple ScreenCaptureKit](https://developer.apple.com/documentation/screencapturekit)
- [Microsoft WASAPI Loopback](https://learn.microsoft.com/windows/win32/coreaudio/loopback-recording)
- [uTools preload/Node.js](https://www.u-tools.cn/docs/developer/information/preload.html)

## 8. 实时转写架构

WebSocket v1 基线：

1. WebSocket 建立后发送 `start` JSON，包含短期 session ticket 和音频参数。
2. vision 收到百炼 `task-started` 后返回 `session_ready`；客户端此前不得发送音频。
3. 每个音频消息为二进制：第一行 UTF-8 JSON header，换行后为 PCM16LE chunk。
4. header 包含 `version`、`type`、`sequenceNo`、`capturedAt` 和 `mimeType`。
5. 服务端返回累计 ACK，以及带 revision 的临时/最终 `transcript_segment`。
6. 客户端使用 ping/pong 保活，并维护有界未确认队列。
7. 停止时发送 `finish`，等待 `session_finished` 后再关闭连接。
8. 关闭 WebSocket 后调用 core `StopMeeting`；重复调用不得重复结算额度。

指数退避重连和 session resume 仍需在实现阶段完成。额度耗尽时 vision 停止接收新音频、flush 已接收内容并返回稳定错误和完成事件。

session ticket 放在首个受 TLS 保护的消息中，不放普通 URL query，减少代理日志泄漏。

## 9. 状态管理

唯一会议状态由 `useMeetingStore` 持有：

```text
idle -> starting -> recording -> stopping -> processing -> terminal
```

Recorder、WebSocket 和计时器是 store 拥有的运行时资源。任何异常、停止或销毁路径都必须通过统一 cleanup 释放资源。

组件不能自行推断“后端成功”。例如按钮点击后只有在会议创建、WebSocket（真实模式）和麦克风启动全部成功时才进入 `recording`。

## 10. 后端集成

插件只调用 `tiehu-fitness` 项目提供的 API：

- `core-service`：uTools 登录、会议生命周期、额度、最终转写查询、纪要、导出和用户数据。
- `vision-service`：插件仅使用 core 创建会议时返回的短期一次性 ticket 直连实时 WSS；不持有 core-to-vision 内部凭证或百炼凭证。
- 对象存储：插件仅使用后端签发的短期、限范围上传凭证直传。

服务端详细需求位于上级项目：

```text
../docs/ai-meeting-assistant-backend-requirements.md
```

## 11. Mock 与真实环境

`VITE_USE_MOCK_API=true`：

- 不调用后端。
- 仍真实请求麦克风，便于验证权限和资源释放。
- 模拟三段转写和一份纪要。
- 浏览器环境使用 Blob 下载模拟 Markdown 保存。

`VITE_USE_MOCK_API=false`：

- 通过 preload 获取 uTools 临时令牌。
- 调用真实认证和会议 API。
- 使用后端返回的 WebSocket 与音频约束。

Mock 不得进入生产构建配置，发布流水线需要显式检查环境变量。

## 12. 测试策略

- Domain：状态转换、闭集解析和边界值。
- Application：Markdown 转义、幂等意图和资源清理。
- Infrastructure：API 错误映射、WS 协议、乱序/重复片段、Recorder 失败。
- Components：按钮可用性、错误、空态、处理中和纪要展示。
- E2E：真实 uTools 中的麦克风权限、隐藏/退出、保存对话框和深色模式。
- 长稳：30 分钟、2 小时会议的内存、CPU、分片队列和重连。

平台音频和 uTools API 不能只在普通浏览器中验收。

## 13. 架构演进

当系统音频、屏幕录制和本地 spool 接入时，优先增加边界明确的 adapter。只有出现多个页面和真实导航需求时再引入 Vue Router；只有出现可靠本地数据库需求时再评估 SQLite，不提前增加复杂度。
