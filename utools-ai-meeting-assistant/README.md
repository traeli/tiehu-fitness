# 会议助手 uTools 插件

面向 Windows 与 macOS 的本地会议录音和转写工具。系统音频、麦克风混音、Whisper 推理、录音和转写历史均在用户电脑完成，不依赖业务后端。

## 当前功能

- macOS 13+：ScreenCaptureKit 采集系统音频。
- Windows 10+：WASAPI Loopback 采集系统音频。
- 可选麦克风，与系统音频在本机混音。
- whisper.cpp 本地中文/多语言识别，可由用户选择模型：Tiny Q5（约 31 MB）、Base Q5（约 57 MB）、Small Q5（约 181 MB，推荐）或 Medium Q5（约 514 MB）。
- 模型由用户主动按需下载，下载后保存在本机，可随时切换并离线转写。
- 录音与转写保存在 `文稿/会议助手/录音`，可回放、跳转、导出 Markdown 或删除。
- 不创建云端会议，不上传 PCM，不使用云端 ASR、WebSocket 或会议额度。
- 会议总结可选择本机 `Ollama`，也可由用户主动连接 OpenAI 或 DeepSeek。
- Ollama 地址仅允许 `127.0.0.1`、`localhost` 或 `::1`；选择 OpenAI 或 DeepSeek 时，只有转写文字会发送至所选服务，录音文件不会上传。

## 开发

```bash
npm install
npm run dev:utools
```

系统音频和本地 Whisper 只能在 uTools 桌面环境中验证，普通浏览器仅用于页面开发。

### 本地会议总结

先安装并启动 Ollama，再安装一个本地模型，例如：

```bash
ollama pull deepseek-r1:7b
ollama serve
```

进入“本地会议记录”，选择一场已有转写的会议，在“AI 总结”页选择提供方：

- `本地 Ollama`：检测本机模型并生成总结。默认服务地址为 `http://127.0.0.1:11434`。
- `OpenAI`：填写模型和用户自己的 API Key，生成总结时转写文字会发送至 OpenAI。
- `DeepSeek`：填写模型和用户自己的 API Key，生成总结时转写文字会发送至 DeepSeek。

OpenAI 和 DeepSeek 的 API Key 只保留在当前运行会话，不写入本地存储、会议文件或日志。

### 原生组件

常规 `npm run native:build` 构建系统音频组件：macOS 需要 Xcode Command Line Tools；Windows 需要 Visual Studio C++ 桌面开发环境。

macOS 本地 Whisper 组件使用官方 XCFramework：

```bash
mkdir -p .native-build/whisper-sdk
unzip whisper-b5130-xcframework.zip -d .native-build/whisper-sdk
sh native/transcription/build-macos.sh \
  .native-build/whisper-sdk/build-apple/whisper.xcframework
```

Windows 本地 Whisper 组件从固定版本 whisper.cpp 源码编译：

```powershell
.\native\transcription\build-windows.ps1 `
  -WhisperSource C:\src\whisper.cpp `
  -Architecture x64
```

发布包必须包含对应平台的 `tiehu-system-audio`、`tiehu-local-whisper`；macOS 还需包含相邻的 `whisper.framework`。模型不放入安装包，由用户按需下载并校验文件大小和 SHA-256。

## 验证

```bash
npm run typecheck
npm test
npm run build
```

跨平台正式发布前执行 `npm run release:check`，并按 [发布手册](docs/release.md) 完成 Windows/macOS 原生组件和干净机器验收。

## 隐私

隐私说明见 [docs/privacy.md](docs/privacy.md)。会议音频始终在本机处理；本地 Ollama 模式下转写和总结不离开设备。只有用户主动选择 OpenAI 或 DeepSeek 时，转写文字才会发送给所选服务生成总结。模型下载请求不包含会议内容。

## 目录

```text
plugin/                    uTools preload、本地文件与进程桥接
native/macos/              macOS 系统音频组件
native/windows/            Windows 系统音频组件
native/transcription/      whisper.cpp 本地转写组件
src/infrastructure/audio/  采集与混音
src/infrastructure/transcription/ 本地 PCM 分段与转写
src/stores/meeting.ts      本地会议生命周期
```
