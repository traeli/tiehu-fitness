# 会议助手发布手册

更新日期：2026-10-28

## 当前发布状态

当前候选版本为 `0.1.0`，尚未达到跨平台正式发布条件。

已通过：

- TypeScript 类型检查。
- 43 项自动化测试。
- macOS Apple Silicon 与 Intel 系统音频组件检查。
- macOS Apple Silicon 与 Intel 本地 Whisper 组件及 Framework 检查。
- Windows x64 WASAPI 系统音频组件检查。
- Vue 生产构建。

当前阻塞项：

- 缺少 `plugin/native/win32-x64/tiehu-local-whisper.exe`。没有该文件时，Windows 用户无法使用本地语音识别，禁止发布标注为 Windows/macOS 均可用的正式版本。
- Windows 原生组件完成后，仍需在干净的 Windows 10/11 x64 环境完成真实会议验收。
- 当前功能改造尚在未提交工作区中；发布前应完成代码审查，建立可追溯的发布提交并记录提交哈希。

## 生成 Windows 本地转写组件

在安装了 Visual Studio C++ 桌面开发环境、CMake 和 Node.js 的 Windows x64 机器上，使用 Developer PowerShell 进入项目目录并执行：

```powershell
npm ci
npm run native:build
npm run native:verify-release
```

`native:build` 会生成：

```text
plugin/native/win32-x64/tiehu-system-audio.exe
plugin/native/win32-x64/tiehu-local-whisper.exe
```

将生成的 Windows 文件与 macOS 双架构文件合并到同一个工作副本后，再生成最终发布目录。不得使用占位文件绕过校验。

## 最终发布检查

合并全部平台文件后执行：

```bash
npm ci
npm run release:check
```

该命令依次检查类型、测试、原生发布文件并生成不含开发入口和 source map 的生产 `dist`。

随后人工检查：

1. `dist/plugin.json` 不包含 `development` 字段。
2. `dist/native` 同时包含 macOS arm64、macOS x64 和 Windows x64 运行文件。
3. `dist` 不包含 `.env`、API Key、会议录音、转写正文或用户数据。
4. macOS 13+ Apple Silicon 和 Intel 各完成一次系统音频、麦克风、模型下载、录音、转写和保存。
5. Windows 10/11 x64 完成相同流程，并覆盖扬声器、耳机及默认输出设备切换。
6. Tiny、Base、Small、Medium 四种模型至少分别验证一次下载校验；Small 完成一次中文真实会议转写。
7. Ollama、OpenAI、DeepSeek 三种总结入口的隐私提示与实际网络行为一致。
8. 退出插件后，音频采集和本地转写进程均已结束。

## uTools 打包与发布

在 uTools 开发者工具中选择：

```text
<project>/dist/plugin.json
```

不要选择项目根目录中的 `plugin/plugin.json`。

离线测试：点击“打包”，填写版本号并生成 `.upxs`，在干净机器安装验证。离线包版本号与市场发布版本号彼此独立。

应用市场发布：点击“发布”，确认版本号和 `dist` 目录内容，填写版本说明、应用介绍、用户手册和截图，然后提交审核。

建议首发版本号：`0.1.0`。

## 市场文案草案

### 一句话介绍

本地录音、本地 Whisper 转写、私有安全的会议助手。

### 应用介绍

会议助手面向 Windows 与 macOS，支持采集系统声音和麦克风，并在当前电脑上完成 Whisper 语音识别。录音、转写和会议记录默认只保存在本机，不上传会议音频。

用户可按设备性能选择 Tiny、Base、Small 或 Medium 模型。会议总结默认支持本机 Ollama；如用户主动选择 OpenAI 或 DeepSeek，仅发送转写文字，不发送录音文件。

使用前请确认你有权记录会议，并按所在地法律和组织制度履行必要的告知或取得同意。

### `0.1.0` 版本说明

- 支持 Windows 与 macOS 系统音频及麦克风录制。
- 支持本地 Whisper 实时转写，提供四种模型按需下载。
- 录音、转写和会议记录保存在本机。
- 支持本机 Ollama、OpenAI 和 DeepSeek 生成结构化会议总结。
- 增加静音检测和常见 Whisper 幻觉过滤。

### 截图清单

至少准备 4 张无真实隐私数据的截图：

1. 主工作台和“本地模式”状态。
2. Whisper 模型选择、大小和已下载状态。
3. 录音中的音频电平和实时转写。
4. 本地会议记录与结构化总结。

## 发布后检查

1. 在“发布历史”确认审核状态。
2. 审核通过后，从应用市场重新安装公开版本。
3. 再完成一次模型下载、录音、转写、总结和删除流程。
4. 保存最终版本号、提交哈希、`.upxs` 测试包和人工验收记录。
