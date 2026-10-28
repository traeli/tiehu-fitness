# AI 会议助手本地转写快速上线实施方案

状态：MVP 实施基线

适用范围：Windows uTools 客户端、iOS 客户端

目标：优先尽快上线可用版本，不处理复杂恢复、极端环境和大规模扩展能力

最后更新：2026-09-28

## 1. 最终需求范围

本版本只完成以下闭环：

1. 用户登录。
2. 用户下载一个默认本地语音模型。
3. Windows 采集系统音频和麦克风；iOS 采集麦克风或导入音频文件。
4. 音频在设备本地实时转写，不上传后端。
5. 录音、转写和会议记录保存在本地。
6. 用户本机安装 Ollama 时，可以选择用 Ollama 整理转写并生成会议总结。
7. 用户可以查看历史会议并导出 Markdown/TXT。

除登录外，客户端不再与业务后端进行会议相关交互：

- 不创建云端会议。
- 不申请云端转写 session。
- 不连接远端转写 WebSocket。
- 不查询或扣减会议额度。
- 不上传音频。
- 不上传转写。
- 不从服务端查询会议历史、转写或总结。
- 不调用云端总结服务。

登录成功后，后端只负责返回用户身份和访问令牌。登录状态不影响本地转写引擎的实现边界。

## 2. 快速上线原则

- 只选择一个默认实时 ASR 引擎：`sherpa-onnx`。
- 只选择一个经过验证的中文/中英混合模型。
- Windows 复用现有 uTools、WASAPI、麦克风混音和页面。
- iOS 使用原生 SwiftUI 和 AVFoundation，不复用 Electron/uTools 壳。
- 第一版不接入 whisper.cpp；接口保留扩展点，后续再加。
- Ollama 只处理转写文本，不处理 PCM 音频。
- 第一版只保证正常开始、录制、停止、保存和回看。
- 不做崩溃恢复、断点续传、自动补转写和复杂迁移。
- 不做多模型市场、GPU 多后端选择和自动性能调优。
- 不做说话人分离、声纹识别和团队同步。
- 不为了抽象完整性迁移桌面框架或重写现有 UI。

## 3. Meetily 可借鉴部分

Meetily 当前将音频采集、VAD、本地模型和转写 worker 放在桌面原生核心中，前端只负责发命令和接收转写事件。其旧的 Python/FastAPI 和独立 whisper-server 已不是当前支持路径。

本项目只借鉴以下四点：

1. 本地原生进程负责模型推理，前端不直接运行大型模型。
2. 音频统一转成 16kHz 单声道后交给 ASR。
3. 转写引擎通过统一 provider 接口隔离。
4. 转写结果通过事件增量发送给 UI，并保存在本地。

参考：

- [Meetily 仓库](https://github.com/Zackriya-Solutions/meetily)
- [Meetily frontend README](https://github.com/Zackriya-Solutions/meetily/blob/main/frontend/README.md)
- [Meetily TranscriptionProvider](https://github.com/Zackriya-Solutions/meetily/blob/main/frontend/src-tauri/src/audio/transcription/provider.rs)
- [Meetily 转写 worker](https://github.com/Zackriya-Solutions/meetily/blob/main/frontend/src-tauri/src/audio/transcription/worker.rs)
- [Meetily 音频 pipeline](https://github.com/Zackriya-Solutions/meetily/blob/main/frontend/src-tauri/src/audio/pipeline.rs)

不照搬：

- Tauri/Rust 桌面壳。
- Meetily 的完整模型管理系统。
- 复杂 checkpoint 和崩溃恢复。
- 多种云端 provider。
- 旧 FastAPI/whisper-server 架构。

## 4. 技术选型

### 4.1 实时转写

第一版统一使用 `sherpa-onnx streaming`：

- 支持 Windows 和 iOS。
- 支持完全离线。
- 支持真正的流式输入。
- 支持 VAD 和端点检测。
- CPU 可以运行，不要求用户安装 CUDA。

参考：[sherpa-onnx 文档](https://k2-fsa.github.io/sherpa/onnx/index.html)

第一版模型要求：

- 中文和中英混合可用。
- Windows 普通 CPU 能保持实时。
- 目标 iPhone 能保持实时。
- 模型许可证允许当前分发方式。
- 模型体积在用户可接受范围。

阶段开始时只用 1～2 天比较少量候选模型，不建立完整 benchmark 项目。

### 4.2 whisper.cpp

不进入第一版开发范围。

代码只保留 `TranscriptionProvider` 抽象，后续可以增加：

- Whisper 多语言模型。
- 会议结束后二次高质量转写。
- 更高性能设备上的可选实时模式。

### 4.3 Ollama

Ollama 只用于：

- 清理口语和重复内容。
- 生成会议标题和摘要。
- 提取主要讨论、决定、待办和风险。

默认访问：

```text
http://127.0.0.1:11434
```

第一版只支持用户选择本机 Ollama 模型，不支持远程公网地址。

## 5. 简化后的整体架构

```text
                 登录 API
                    ^
                    |
Windows / iOS 客户端
        |
        v
本地音频采集
        |
        v
16kHz mono PCM
        |
        v
sherpa-onnx 本地转写
        |
        +------> 实时转写 UI
        |
        +------> 本地会议文件
        |
        `------> 可选 Ollama 总结
```

除登录箭头外，没有会议数据流向业务后端。

## 6. 最小接口设计

第一版只定义当前需要的方法：

```ts
export interface TranscriptionProvider {
  prepare(modelPath: string): Promise<void>;
  start(listener: (event: TranscriptEvent) => void): Promise<void>;
  pushAudio(chunk: ArrayBuffer): void;
  stop(): Promise<void>;
  dispose(): Promise<void>;
}
```

转写事件：

```ts
export interface TranscriptEvent {
  segmentId: string;
  revision: number;
  startOffsetMs: number;
  endOffsetMs: number;
  content: string;
  isFinal: boolean;
}
```

基本规则：

- 临时文本使用相同 `segmentId` 更新。
- `revision` 较大的结果覆盖较小结果。
- final 结果进入本地会议记录。
- 停止时等待引擎输出最后一个 final 结果，然后释放模型 session。

第一版不设计复杂远程 provider、恢复协议和任务调度框架。

## 7. Windows 实施方案

### 7.1 复用现有能力

继续使用当前项目：

- `native/windows/main.cpp` 的 WASAPI Loopback。
- `plugin/preload.js` 的受控原生进程管理。
- `microphoneRecorder.ts` 的麦克风和系统音频混音。
- AudioWorklet 生成的 PCM16LE/16kHz/mono。
- `TranscriptPanel.vue` 实时展示。
- `RecordingList.vue` 历史会议和播放界面。

### 7.2 新增本地 ASR 程序

新增：

```text
native/windows/local-stt-runtime.exe
```

职责：

1. 加载固定的 sherpa-onnx 模型目录。
2. 从 stdin 接收 PCM 帧。
3. 本地执行流式识别。
4. 从 stdout 输出 JSON 转写事件。
5. 收到 stop 后 flush 并退出本次 session。

第一版不开放 HTTP 端口，不做 Windows Service，不要求用户另外安装 Python。

### 7.3 preload 桥接

新增最小白名单 API：

```ts
prepareLocalTranscription(modelId)
startLocalTranscription()
pushLocalAudio(arrayBuffer)
stopLocalTranscription()
subscribeLocalTranscript(listener)
```

不暴露任意进程执行和任意模型路径。

### 7.4 Store 改造

当前 `startMeeting()` 改为：

1. 确认用户已经登录。
2. 检查默认模型是否存在。
3. 创建本地 meeting id。
4. 启动本地 ASR。
5. 启动现有录音器。
6. 将 PCM 发送给本地 ASR，而不是远端 WebSocket。
7. 更新实时转写 UI。

删除或绕过：

- `createMeeting()`。
- 会议额度检查。
- session ticket。
- `TranscriptionClient` WebSocket。
- 云端 stop meeting。
- 云端 summary polling。

`stopMeeting()` 改为：

1. 停止采集。
2. 停止本地 ASR并获取最后结果。
3. 保存录音和转写。
4. 可选调用 Ollama。
5. 刷新本地会议列表。

### 7.5 Windows 第一版不做

- 进程崩溃后恢复。
- 未完成录音恢复。
- GPU/CUDA/Vulkan 选择。
- 同时安装多个 ASR 模型。
- 长时间积压后的自动补转写。
- Windows ARM64。
- 独立桌面安装程序；首版继续通过 uTools 发布。

## 8. iOS 实施方案

### 8.1 应用形态

新增一个简单的 SwiftUI 工程：

```text
clients/meeting-assistant-ios/
├── App/
├── Login/
├── Recording/
├── Transcription/
├── Meetings/
└── Storage/
```

### 8.2 首版功能

- 复用现有后端登录接口。
- 使用 Keychain 保存登录令牌。
- AVAudioEngine 采集麦克风。
- 导入本地音频文件进行转写。
- sherpa-onnx 本地实时识别。
- 展示实时转写。
- 保存本地录音和转写。
- 展示本地会议列表和详情。
- 导出 Markdown/TXT。

### 8.3 首版不做

- 采集其他 iOS App 的系统声音。
- 电话、微信通话或 VoIP 通话录音。
- ScreenCaptureKit 集成。
- 完整后台异常恢复。
- iCloud 同步。
- iPhone 上直接运行 Ollama。
- 多设备同步。

iOS 首版的明确场景是：线下会议麦克风录音和已有音频文件导入。

## 9. 模型下载

第一版不建设完整模型 catalog。

只在客户端内置一个固定模型描述：

```json
{
  "id": "sherpa-zh-en-default",
  "version": "1",
  "url": "https://download.example.com/sherpa-zh-en-default.zip",
  "sizeBytes": 0,
  "sha256": "..."
}
```

流程：

1. 首次进入检查模型文件。
2. 不存在时显示下载按钮。
3. 显示下载进度。
4. 下载结束校验 SHA-256。
5. 解压到固定应用目录。
6. 校验失败时提示重新下载。

第一版不做：

- 断点续传。
- 多镜像源。
- 模型自动升级。
- 模型市场。
- 多版本回滚。
- 用户输入任意下载地址。

## 10. 本地数据

第一版采用简单会议目录，不引入新的数据库：

```text
meetings/
└── <meeting-id>/
    ├── metadata.json
    ├── audio.webm 或 audio.m4a
    ├── transcript.json
    └── summary.json
```

`metadata.json`：

```json
{
  "meetingId": "uuid",
  "title": "未命名会议",
  "createdAt": "2026-09-28T10:00:00Z",
  "durationMs": 60000,
  "status": "completed"
}
```

`transcript.json`：

```json
{
  "version": 1,
  "segments": []
}
```

保存策略：

- 录制期间转写保存在内存中。
- 用户正常停止时一次写入 `transcript.json`。
- 录音使用现有保存方式。
- 历史列表读取 metadata。
- 详情页读取本地音频、转写和总结。

第一版接受应用异常退出后当前会议数据可能丢失，不做 checkpoint 和恢复。

## 11. Ollama 接入

Windows 设置页增加：

- 启用 Ollama。
- Ollama 地址，默认 `http://127.0.0.1:11434`。
- 检测连接。
- 读取本机模型列表。
- 选择总结模型。

会议正常停止后：

1. 组合 final transcript。
2. 调用 Ollama `/api/chat`。
3. 要求返回固定 JSON。
4. 解析并保存 `summary.json`。
5. 失败时显示“总结失败”，不影响录音和转写完成。

第一版不做：

- 实时调用 Ollama 修改字幕。
- 远程公网 Ollama。
- 多种总结模板。
- 自动重试队列。
- iOS Ollama 集成。

## 12. 页面调整

### 保留

- 登录页面。
- 开始/停止按钮。
- 系统音频和麦克风开关。
- 音频电平。
- 实时转写。
- 本地录音列表。
- 录音播放和转写跳转。
- 会议总结。
- Markdown 导出。

### 删除或隐藏

- 云端转写同意选项。
- 会议额度卡片。
- 云端连接状态。
- 保留云端录音选项。
- 云端处理和 summary polling 文案。

### 新增

- “音频和转写仅在本机处理”提示。
- 默认模型下载页面或弹窗。
- 模型下载进度。
- 本地模型加载状态。
- Ollama 简单配置页面。

## 13. 快速实施计划

### 阶段 A：Windows 本地转写，5～7 个工作日

第 1 天：

- 固定 sherpa-onnx 模型。
- 在 Windows CLI 中验证麦克风/PCM 文件转写。
- 确认模型许可证和最低机器性能。

第 2～3 天：

- 实现 `local-stt-runtime.exe`。
- 实现 stdin/stdout PCM 和转写事件协议。
- preload 增加本地转写桥接。

第 4～5 天：

- 改造 Meeting Store。
- PCM 从远端 WebSocket 切换到本地进程。
- 删除会议额度和云端会议依赖。
- 实时转写接入现有 UI。

第 6～7 天：

- 保存本地 transcript。
- 历史详情读取本地转写。
- 30 分钟真实会议测试。
- 构建可用 uTools 包。

交付：Windows 用户登录后可以下载模型，录制系统音频/麦克风并在本地实时转写。

### 阶段 B：Ollama，1～2 个工作日

- 检测本机 Ollama。
- 列出本地模型。
- 会议结束后生成 JSON 总结。
- 保存并展示总结。

Ollama 不阻塞 Windows 第一版发布；如果进度不足，可以放到第一个小版本更新。

### 阶段 C：iOS MVP，7～10 个工作日

第 1～2 天：

- 创建 SwiftUI 工程。
- 接入登录。
- AVAudioEngine 麦克风录音。

第 3～5 天：

- 接入 sherpa-onnx。
- 本地模型下载。
- 实时转写 UI。

第 6～7 天：

- 本地会议目录。
- 会议列表、详情和导出。
- 音频文件导入转写。

第 8～10 天：

- 真机测试。
- 权限文案和基础错误处理。
- TestFlight 构建。

### 阶段 D：联合测试与上线，2～3 个工作日

- Windows 一台主流设备测试。
- iPhone 至少两台真机测试。
- 断网测试。
- 30 分钟会议测试。
- 确认没有音频或转写请求发送到业务后端。
- 修复阻塞使用的问题。
- 发布 Windows/uTools 包和 iOS TestFlight。

## 14. 时间与 Token 预算

### 14.1 单人实施

- Windows 可用版：5～7 个工作日。
- Windows + Ollama：6～9 个工作日。
- iOS 可用版：在 Windows 之后增加 7～10 个工作日。
- Windows + iOS + Ollama 联合上线：约 3～4 周。

前提：

- 有可用的 Windows 开发机。
- 有 Mac、Xcode、Apple Developer 账号和至少一台 iPhone 真机。
- 默认 sherpa 模型在第一天验证通过。
- 不临时扩大 iOS 系统音频、独立 Windows 桌面应用等范围。

### 14.2 两人并行

- 一人负责 Windows，一人负责 iOS。
- 约 2 周可以得到 Windows 可用包和 iOS TestFlight MVP。
- Ollama 和下载页可以在第二周末或后续小版本补充。

### 14.3 Codex Token 预算

快速 MVP 预计：

| 工作 | Token 预算 |
|---|---:|
| Windows 本地 ASR 和现有前端改造 | 25万～40万 |
| 本地会议数据和模型下载 | 10万～18万 |
| Ollama | 5万～10万 |
| iOS MVP | 30万～50万 |
| 测试、构建和修复 | 10万～20万 |

完整快速版约需：

> 80万～138万 Codex Token

建议先为 Windows 阶段分配 40万～60万 Token，Windows 可用后再为 iOS 分配 40万～65万 Token。

运行时：

- sherpa-onnx 本地转写不消耗 LLM Token。
- Ollama 使用本机模型 token，不产生云端 API 费用。

## 15. 最小验收标准

Windows：

1. 用户能正常登录。
2. 用户能下载并加载默认模型。
3. 断网后仍能开始会议。
4. 系统音频和麦克风可以本地实时转写。
5. 停止后可以在历史列表看到录音和转写。
6. 音频和转写没有发送到业务后端。
7. Ollama 存在时可以生成总结；不存在时不影响转写。

iOS：

1. 用户能正常登录。
2. 用户能下载并加载默认模型。
3. 麦克风可以本地实时转写。
4. 可以导入音频文件进行本地转写。
5. 停止后可以查看本地会议和导出文本。
6. 音频和转写没有发送到业务后端。

不作为首版阻塞条件：

- 应用崩溃后恢复当前会议。
- 两小时以上压力测试。
- 所有蓝牙和声卡兼容。
- 低端设备性能优化。
- GPU 加速。
- 多模型切换。
- 说话人分离。
- iOS 第三方 App 系统音频。
- App Store 正式审核通过；首版以 TestFlight 可安装为完成标准。

## 16. 建议上线顺序

1. 先用 5～7 个工作日发布 Windows 本地转写版。
2. 同时或随后用 7～10 个工作日完成 iOS 麦克风/文件导入版。
3. Ollama 在 Windows 核心转写稳定后用 1～2 天接入。
4. whisper.cpp、崩溃恢复、模型市场、GPU 和 iOS 系统音频全部放入后续迭代。

本方案的完成标准是“用户能够登录、下载模型、在本地录音和转写、保存并查看结果”，而不是一次性建设完整的本地 AI 会议平台。
