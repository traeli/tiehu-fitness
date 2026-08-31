# AI 会议助手 uTools 插件

一个面向 uTools 的轻量 AI 会议记录客户端，支持会议状态管理、电脑系统音频与麦克风混音、实时转写通道、本地录音回放、最终转写查询、结构化纪要展示和 Markdown 导出。

当前客户端已经实现后端 WS v1 所需的 PCM16LE/16kHz/mono 采集、ACK、心跳和 finish 握手，并可在最近会议中回放本地录音、查看最终转写和异步 AI 总结；默认开发模式仍使用 Mock API。

## 技术栈

- Vue 3 + TypeScript
- Vite
- Pinia
- uTools `plugin.json` + CommonJS `preload`
- AudioWorklet PCM + WebSocket
- macOS ScreenCaptureKit + Windows WASAPI Loopback 原生辅助程序

## 快速开始

```bash
npm install
cp .env.example .env.local
npm run dev
```

浏览器开发默认启用 Mock API。点击“开始会议”会请求麦克风权限，并模拟实时文本与最终纪要。

联调模式分为两种，避免把 Fake ASR 文本误认为真实识别结果：

```bash
# 合成静音 PCM，仅验证真实 API、WebSocket、ACK 和结束握手
npm run dev:api

# 浏览器真实麦克风；系统音频仍需在 uTools 中测试
npm run dev:real
```

使用本仓库本地 core/vision 完成真实 WebSocket 联调：

```bash
# 仓库根目录分别启动基础设施和两个 Go 服务
make infra-up
make run-vision
make run-core

# 当前目录启动浏览器真实 API 模式
npm run dev:api
```

打开 `http://127.0.0.1:5173/`，使用页面内的邮箱注册或登录。`dev:api` 通过
Vite 同源代理访问 core，并显式启用合成静音 PCM；它不申请麦克风权限，但会经过
真实的会议创建、vision ticket、WebSocket、音频 ACK 和 finish 路径。静音通常不会
产生转写文本；该模式不会进入默认 Mock 或生产 uTools 构建。

构建连接本地 core、使用真实系统音频和麦克风的 uTools 插件目录：

```bash
npm run build
```

日常开发不需要反复构建和重新选择 `dist/plugin.json`。首次在 uTools 开发者工具中
选择源码目录的 `plugin/plugin.json`，以后运行：

```bash
npm run dev:utools
```

页面使用 Vite 热更新；只有修改 preload 或原生音频源码时才需要退出并重新打开插件。
会议录制中会直接显示“电脑音频”和“发送音频”电平，`native:smoke` 仅用于权限或
设备故障诊断，不是正常测试步骤。热更新模式通过同源 `/api` 代理访问本地 core，
不会向 `127.0.0.1:8000` 发送浏览器 CORS 预检请求。

不要直接在 Chrome 中打开 `http://127.0.0.1:5173/` 测试系统音频。Vite 只负责提供
页面，ScreenCaptureKit/WASAPI 由 uTools preload 启动；必须在 uTools 中执行“会议助手”
指令打开页面。标题栏环境标识应显示 `API · main`，显示 `API · browser` 就说明仍在
普通浏览器中。

构建后在 uTools 开发者工具中选择：

```text
dist/plugin.json
```

### uTools 本地录音

真实系统音频/麦克风会议正常停止后，插件会把混音后的压缩音频和本地索引写入：

```text
文稿/铁虎AI会议助手/录音
```

“最近会议录音”列表由该本地索引驱动。点击一条记录时，音频从本地文件读取，会议状态和最终转写通过 core API 按 `meeting_id` 查询。`保留云端录音` 只控制云端是否保留原始音频，不影响 uTools 本地录音。

当前单条录音限制为 256 MB，本地索引最多保存 1000 条并展示最近 100 条。当前版本在正常停止时落盘；进程崩溃后的录音恢复和录制过程流式 spool 仍属于后续可靠性工作。

系统音频不再依赖 uTools 内置 Electron 的桌面音轨：macOS 13+ 使用
ScreenCaptureKit，Windows 10+ 使用 WASAPI Loopback，两端都通过受控辅助程序输出
48kHz 单声道 PCM，再与可选麦克风在 Web Audio 中混音。macOS 首次使用需要在
“系统设置 → 隐私与安全性 → 屏幕与系统音频录制”中允许 `Tiehu System Audio`；
拒绝、辅助程序缺失或异常退出都会停止会议，不能静默回退为麦克风录音。

`npm run build` 会先构建当前系统的原生组件。macOS 需要 Xcode Command Line Tools，
并同时生成 Apple Silicon 与 Intel 版本；Windows 需要在 Visual Studio Developer
PowerShell 中安装“使用 C++ 的桌面开发”，并为当前架构生成 WASAPI 组件。正式跨平台
发布包必须同时包含 `darwin-arm64`、`darwin-x64`、`win32-x64`，以及计划支持
Windows on ARM 时的 `win32-arm64` 二进制。

`npm run build` 使用 `.env.utools-local`，直接连接 `http://127.0.0.1:8000`，不经过 Vite 代理。生产构建使用 `npm run build:production`，并在构建环境设置：

```dotenv
VITE_API_BASE_URL=https://api.example.com
VITE_USE_MOCK_API=false
```

## 文档

- [产品需求](docs/requirements.md)
- [实施计划与当前进度](docs/implementation-plan.md)
- [技术架构](docs/architecture.md)
- [接口约定](docs/api-contract.md)
- [阿里云百炼 Paraformer 实时转写接入方案](docs/阿里百炼Paraformer接入方案.md)
- [后端实施计划方案](docs/实施计划方案.md)
- [开发与发布](docs/development.md)

## 当前目录

```text
plugin/                 uTools 元数据、preload 与静态资源
native/                 ScreenCaptureKit/WASAPI 源码、构建脚本与帧协议
scripts/                跨平台原生构建入口
src/application/        应用编排和 Markdown 生成
src/components/         Vue 展示组件
src/domain/             会议领域类型与客户端状态规则
src/infrastructure/     HTTP、WebSocket、音频和桌面适配器
src/stores/             Pinia 状态容器
docs/                   项目、需求、接口和开发文档
dist/                   构建产物，不提交 Git
```
