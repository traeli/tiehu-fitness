# 阿里云百炼 Paraformer 实时转写接入方案

> 版本：V1.0<br>
> 更新时间：2026-08-27<br>
> 状态：方案已确定，待后端与客户端实施<br>
> 适用项目：`utools-ai-meeting-assistant` 客户端及 `tiehu-fitness` 的 core-service、vision-service<br>
> 参考方案：[`../../docs/对话翻译接入方案.md`](../../docs/对话翻译接入方案.md)

官方文档：

- [Paraformer 实时语音识别 WebSocket API](https://help.aliyun.com/zh/model-studio/websocket-for-paraformer-real-time-service)
- [Paraformer 客户端事件](https://help.aliyun.com/zh/model-studio/paraformer-client-events)
- [Paraformer 服务端事件](https://help.aliyun.com/zh/model-studio/paraformer-server-events)
- [百炼语音识别模型与格式](https://help.aliyun.com/zh/model-studio/asr-model)
- [百炼模型计费](https://help.aliyun.com/zh/model-studio/model-pricing)

## 1. 文档目的

本文将已有项目的“对话翻译接入方案”中可复用的供应商隔离、密钥保护、流式错误处理和可替换 Provider 设计，调整为当前 AI 会议助手的实时语音转写方案。

本文固化以下决定：

- V1 实时 ASR 供应商使用阿里云百炼。
- 实时模型使用 `paraformer-realtime-v2`。
- 可选会后二次识别使用 `paraformer-v2`。
- uTools 不直接连接百炼，也不持有百炼 API Key。
- uTools 通过 vision-service 的业务 WebSocket 上传音频并接收实时文本。
- core-service 负责用户、会议、额度和资源归属；vision-service 负责媒体、ASR 会话和 Provider 调用。
- core-service 与 vision-service 可以连接同一个 Redis 实例，但必须使用不同 ACL、连接配置和 key 前缀。

本文是接入设计，不表示相应后端代码已经完成。

实时传输路径以本文为准：此前文档中“uTools 的实时音频先进入 core-service，再由 core 通过 gRPC 转发给 vision-service”的草案被本方案替代。core-service 仍是登录和会议控制面的唯一入口；只有实时音频 WebSocket 通过 vision-service 返回的一次性 ticket 直连 vision-service。

## 2. 与参考方案的区别

参考文档实现的是“一轮一句”的对话翻译，本项目实现的是持续数十分钟到数小时的会议转写，不能复用原来的 HTTP + SSE 调用形态。

| 项目 | 对话翻译参考方案 | 当前会议助手 |
|---|---|---|
| 输入 | 一段完整 Base64 音频 | 持续发送二进制音频帧 |
| 客户端通道 | HTTP POST | WebSocket |
| 厂商通道 | OpenAI 兼容 HTTP 流 | 百炼 Paraformer WebSocket |
| 输出 | SSE 译文/音频 | WebSocket 临时和最终转写片段 |
| 会话时长 | 一句话 | 长会议 |
| 音频保存 | 不保存 | 可选本地落盘、可选云端保留 |
| 业务所有者 | 单一翻译接口 | core 会议域 + vision AI 域 |
| 断线处理 | 请求失败后重试 | ACK、有限缓冲、重连和会后校正 |

继续复用的原则：

- 客户端协议不暴露厂商名称、模型名或厂商错误结构。
- Provider 密钥只存在于后端部署环境。
- 业务层只依赖能力接口，百炼协议封装在 data adapter。
- 开始流之前和开始流之后使用不同的错误通道。
- 厂商原始错误只进入脱敏日志，不直接返回客户端。

## 3. 模型与成本决策

### 3.1 实时模型

```text
paraformer-realtime-v2
```

官方当前按输入音频时长计费，单价为 `0.00024 元/秒`，折合约 `0.864 元/小时`；输出文本不另行计费。官方价格页当前列出每月 10 小时免费额度，实际结算以百炼控制台账单为准。

### 3.2 会后模型

```text
paraformer-v2
```

录音文件识别当前为 `0.00008 元/秒`，折合约 `0.288 元/小时`。该模型用于重要会议、实时链路出现缺口或用户主动要求高质量重整时的二次识别，不默认对所有会议调用。

如果整场会议同时执行实时和文件识别，ASR 理论成本约为：

```text
0.864 + 0.288 = 1.152 元/小时
```

### 3.3 成本控制规则

- 默认只执行实时识别。
- 只有实时连接出现缺口、质量低于阈值或用户使用高级功能时才执行文件二次识别。
- 记录 Provider 返回的计费时长，不能仅根据会议墙钟时长估算账单。
- 设置用户月度分钟数、单场最长时长和并发会议上限。
- 停止会议或客户端失联超过宽限期后及时结束百炼任务。
- 每日汇总实际音频秒数、免费额度使用量、失败请求和估算成本。

## 4. 当前项目现状与兼容性结论

### 4.1 已有客户端能力

当前 uTools 项目已经具备：

- `HttpMeetingGateway` 创建和停止会议。
- `TranscriptionClient` 建立业务 WebSocket。
- `start`、`audio_chunk`、`finish` 消息骨架。
- `transcript_segment` 解析和实时界面展示。
- `BrowserMeetingAudioRecorder` 基于 Web Audio 混合原生系统音频（macOS ScreenCaptureKit / Windows WASAPI Loopback）和可选麦克风，并由 `MediaRecorder` 保存本地压缩录音。

当前 WebSocket 协议仍缺少 ACK、心跳、重连、resume、临时片段 revision 和稳定错误码，实施 Paraformer 前需要补齐。

### 4.2 音频格式阻塞项

当前 `BrowserMeetingAudioRecorder` 的本地录音默认产生：

```text
audio/webm;codecs=opus
```

Paraformer 虽支持 Opus，但官方要求 Opus 使用 Ogg 封装。浏览器 `MediaRecorder` 生成的 WebM/Opus 不能声明成 `opus` 后直接透传，否则会出现格式与实际容器不一致。

V1 决定将音频拆成两条管线：

```text
麦克风 MediaStream + 原生系统音频 PCM
    |
    |-- 实时管线：AudioWorklet -> 16kHz mono PCM16 -> vision WebSocket
    |
    `-- 录音管线：MediaRecorder -> WebM/Opus 本地 spool -> 可选上传
```

实时音频固定为：

- 编码：signed PCM 16-bit little-endian。
- 采样率：16,000 Hz。
- 声道：mono。
- 推荐分片：100～200ms。
- 对外 MIME：`audio/pcm;rate=16000`。
- 单个 200ms PCM 分片约 6,400 字节，不含业务消息头。

不在 vision-service 中为每个实时会话启动 FFmpeg 转码进程。服务端只验证和转发已经符合约定的 PCM，避免高并发下的进程、CPU 和延迟成本。

## 5. 总体架构

实时通道使用“core 控制面、vision 数据面”：

```text
uTools Renderer
    |
    | 1. uTools 临时令牌登录、创建会议（HTTPS）
    v
core-service
    |- 验证用户身份
    |- 创建 Meeting
    |- 检查额度和并发限制
    |
    | 2. PrepareTranscription（内部 gRPC）
    v
vision-service
    |- 创建一次性 WebSocket ticket
    |- 管理 ASR session
    |- 连接百炼 Paraformer
    |
    | 3. 返回 WSS URL + ticket 给 core，再返回 uTools
    v
uTools Renderer
    |
    | 4. PCM 音频与转写事件（业务 WebSocket）
    v
vision-service
    |
    | 5. PCM 二进制流 / result-generated
    v
阿里云百炼 Paraformer
```

core-service 不转发实时音频，避免音频流经过两个 Go 服务带来的额外带宽、复制和背压链路。vision-service 暴露的 WebSocket 只接受短期一次性 ticket，不接受客户端提供的 `user_id` 作为身份依据。

### 5.1 数据所有权

core-service 拥有：

- 用户身份和 uTools identity。
- Meeting 元数据和业务状态。
- 用户额度与使用记录。
- 最终转写片段和结构化纪要。
- 用户对会议的查看、导出和删除权限。

vision-service 拥有：

- 短期转写 session。
- 百炼连接和 Provider task ID。
- 音频资产、临时媒体和保留策略。
- ASR job、attempt、Provider 用量与失败信息。
- WebSocket 帧的有限重放缓冲。

vision-service 不查询 core 数据库；core-service 不查询 vision 数据库。最终转写片段通过内部 gRPC 命令或 outbox 驱动的可靠事件传给 core。

## 6. 用户鉴权、Ticket 与共享 Redis

### 6.1 登录链路

```text
uTools
  -> preload.getUserServerTemporaryToken()
  -> POST /v1/auth/utools/login
  -> core-service 验证 uTools 临时令牌
  -> 返回本项目 access token / refresh token
```

百炼 API Key 与 uTools 用户信息没有直接关系。uTools 用户只向 core-service 证明身份；vision-service 使用 core 已授权会议对应的短期 ticket 接受 WebSocket。

### 6.2 Ticket 创建和消费

1. core-service 创建会议后，通过内部 gRPC 调用 vision-service 的 `PrepareTranscription`。
2. vision-service 生成至少 256 bit 随机 ticket，只把原值返回一次。
3. Redis 只保存 ticket 的哈希值和有边界的会话声明。
4. uTools 建立 WSS 后，在第一条 `start` 消息中提交 ticket，不放 URL query。
5. vision-service 使用原子读取并删除消费 ticket；同一个 ticket 不能建立第二条连接。
6. ticket 默认 60 秒过期；过期后由 core 重新申请，不延长旧 ticket。

Redis value 建议包含：

```json
{
  "version": 1,
  "session_id": "uuid",
  "meeting_id": "uuid",
  "user_id": "uuid",
  "expires_at": "2026-08-27T10:01:00Z",
  "audio_format": "pcm",
  "sample_rate": 16000,
  "channels": 1
}
```

Redis key 不包含 ticket 原文：

```text
vision:meeting_asr_ticket:v1:<sha256(ticket)>
```

### 6.3 共享 Redis 的边界

两个服务可以使用同一个 Redis 集群或实例，但共享的是基础设施，不是领域所有权：

- core 前缀：`core:*`。
- vision 前缀：`vision:*`。
- core 和 vision 使用不同 Redis ACL 用户。
- vision 创建和消费自己的 ticket；core 不直接写 `vision:*` key。
- core 不读取 vision session，vision 不读取 core access session。
- 两个服务分别配置连接池、超时、熔断指标和容量预算。
- Redis 不保存百炼 API Key、原始音频、转写正文或长期用户资料。

Redis 不可用时，创建新的实时转写会话返回稳定的服务不可用错误；已经建立的会话不依赖 Redis 维持百炼连接，但重连需要重新签发 ticket。

## 7. uTools 与 vision-service WebSocket 协议

### 7.1 创建会议响应

core-service 返回由 vision-service 生成的连接信息：

```json
{
  "meeting": {
    "meetingId": "018f...",
    "status": "MEETING_STATUS_RECORDING",
    "transcriptionStatus": "TRANSCRIPTION_STATUS_PENDING",
    "grantedAudioDuration": "7200s",
    "startedAt": "2026-08-27T10:00:00Z"
  },
  "transcriptionSession": {
    "sessionId": "uuid",
    "websocketUrl": "wss://vision.example.com/v1/realtime/transcriptions",
    "sessionTicket": "one-time-secret",
    "expiresAt": "2026-08-27T10:01:00Z",
    "audio": {
      "format": "AUDIO_FORMAT_PCM_S16LE",
      "mimeType": "audio/pcm;rate=16000",
      "sampleRate": 16000,
      "channels": 1,
      "chunkDuration": "0.200s",
      "maxChunkBytes": 8192
    },
    "grantedAudioDuration": "7200s"
  }
}
```

插件不能假设 WebSocket 与 core-service 同域，必须使用响应中的完整 `websocketUrl`。HTTP JSON 字段遵循 Proto JSON lowerCamelCase；WebSocket 业务消息继续遵循本文单独定义的 camelCase 协议。

### 7.2 开始消息

WebSocket 建立后，第一条消息必须为 JSON：

```json
{
  "version": 1,
  "type": "start",
  "sessionTicket": "one-time-secret",
  "audio": {
    "mimeType": "audio/pcm;rate=16000",
    "sampleRate": 16000,
    "channels": 1,
    "chunkDurationMs": 200
  }
}
```

vision-service 消费 ticket、校验音频参数并收到百炼 `task-started` 后返回：

```json
{
  "version": 1,
  "type": "session_ready",
  "sessionId": "uuid",
  "acceptedAudio": {
    "format": "pcm",
    "sampleRate": 16000,
    "channels": 1
  }
}
```

客户端必须收到 `session_ready` 后才开始发送音频。当前客户端在 WebSocket `open` 后立即认为已经连接成功，需要在实施时调整。

### 7.3 音频消息

继续使用当前项目的“JSON header + 换行 + 二进制音频”单消息格式：

```text
{"version":1,"type":"audio_chunk","sequenceNo":1,"capturedAt":1787800000000,"mimeType":"audio/pcm;rate=16000"}\n
<PCM16LE bytes>
```

服务端校验：

- header 长度上限。
- `version`、`type` 和 MIME。
- `sequenceNo` 严格递增或为允许的重放序号。
- 音频字节数非零且不超过会话约束。
- PCM 字节数必须为 2 的倍数。
- 每个 session 的总时长、速率和未确认队列不超过上限。

服务端接受并成功写入当前 Provider 发送队列后返回：

```json
{
  "version": 1,
  "type": "ack",
  "ackSequenceNo": 1
}
```

ACK 表示 vision-service 已接受该帧，不表示该帧已经形成最终文本，也不表示音频已持久化。需要无缺口最终稿时，以本地完整录音和可选会后二次识别兜底。

### 7.4 转写片段

```json
{
  "version": 1,
  "type": "transcript_segment",
  "segmentId": "provider-task-id:170",
  "sequenceNo": 8,
  "revision": 3,
  "startOffsetMs": 170,
  "endOffsetMs": 2450,
  "speakerLabel": null,
  "language": "zh",
  "content": "我们先确认今天的会议议题。",
  "isFinal": true
}
```

映射规则：

- `begin_time` -> `startOffsetMs`。
- `end_time` -> `endOffsetMs`；中间结果为空时使用当前已知偏移或省略。
- `text` -> `content`。
- `sentence_end=false` -> `isFinal=false`。
- `sentence_end=true` -> `isFinal=true`。
- `heartbeat=true` 的 Provider 结果不生成转写片段。
- 同一句中间结果复用 `segmentId` 并递增 `revision`，客户端替换旧文本。
- 最终片段不可被后续临时片段覆盖，只持久化最终片段。

Paraformer 实时接口不在本方案中承诺说话人分离，`speakerLabel` 在 V1 实时结果中为空。会后文件识别可作为后续说话人分离与文本校正入口。

### 7.5 心跳、停止与完成

业务心跳：

```json
{ "version": 1, "type": "ping", "sentAt": 1787800000000 }
```

```json
{ "version": 1, "type": "pong", "sentAt": 1787800000000 }
```

停止消息：

```json
{ "version": 1, "type": "finish", "lastSequenceNo": 1200 }
```

vision-service 必须继续读取百炼结果，直到收到 `task-finished` 或结束超时，再向客户端返回：

```json
{
  "version": 1,
  "type": "session_finished",
  "lastAckSequenceNo": 1200,
  "finalSegmentSequenceNo": 86,
  "providerDurationSeconds": 238
}
```

客户端收到 `session_finished` 后再正常关闭 WebSocket。当前客户端发送 `finish` 后立即 `close()`，实施时必须改为等待服务端完成事件或结束超时，避免丢失尾部文本。

### 7.6 错误事件

WebSocket 升级前的鉴权、Origin、连接数和请求格式错误使用 HTTP 状态返回。升级后的错误统一使用：

```json
{
  "version": 1,
  "type": "error",
  "code": "ASR_PROVIDER_UNAVAILABLE",
  "message": "实时转写服务暂时不可用，请稍后重试",
  "retryable": true,
  "lastAckSequenceNo": 118
}
```

稳定错误至少包括：

| code | 是否可重试 | 说明 |
|---|:---:|---|
| `TRANSCRIPTION_TICKET_INVALID` | 否 | ticket 不存在、已消费或签名不合法 |
| `TRANSCRIPTION_SESSION_EXPIRED` | 否 | ticket 或 session 已过期，需要重新申请 |
| `AUDIO_FORMAT_UNSUPPORTED` | 否 | MIME、采样率、声道或编码不符合约定 |
| `AUDIO_SEQUENCE_INVALID` | 视情况 | 序号跳跃、倒退或超过重放窗口 |
| `TRANSCRIPTION_BACKPRESSURE` | 是 | 服务端队列达到上限，客户端应暂停发送 |
| `ASR_PROVIDER_UNAVAILABLE` | 是 | 百炼建连、超时或服务暂时不可用 |
| `ASR_PROVIDER_REJECTED` | 否 | 参数、账号、权限或配额被 Provider 拒绝 |
| `MEETING_DURATION_EXCEEDED` | 否 | 超过单场最长时长 |
| `INTERNAL_ERROR` | 视情况 | 已脱敏的未知服务端错误 |

不得把百炼响应体、API Key、Workspace ID 或内部堆栈放进 `message`。

## 8. vision-service 到百炼的调用

### 8.1 接口与鉴权

vision-service 使用 Go 直接连接百炼 WebSocket。DashScope SDK 当前主要覆盖 Java/Python，因此 Go 侧使用 WebSocket 协议适配器，不通过 Python 子进程。

```text
wss://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference
```

请求头：

```http
Authorization: Bearer <BAILIAN_API_KEY>
X-DashScope-WorkSpace: <BAILIAN_WORKSPACE_ID>
```

API Key 只从部署环境或密钥管理服务读取。默认不发送 `X-DashScope-DataInspection: enable`。

### 8.2 启动任务

连接成功后发送 `run-task`：

```json
{
  "header": {
    "action": "run-task",
    "task_id": "uuid",
    "streaming": "duplex"
  },
  "payload": {
    "task_group": "audio",
    "task": "asr",
    "function": "recognition",
    "model": "paraformer-realtime-v2",
    "parameters": {
      "format": "pcm",
      "sample_rate": 16000,
      "language_hints": ["zh", "en"],
      "semantic_punctuation_enabled": true,
      "punctuation_prediction_enabled": true,
      "inverse_text_normalization_enabled": true,
      "disfluency_removal_enabled": false,
      "heartbeat": true
    },
    "input": {}
  }
}
```

参数决策：

- 默认模型固定为 `paraformer-realtime-v2`，但由后端配置提供，不由客户端传厂商模型名。
- `auto` 语言不传 `language_hints`，或根据正式质量测试决定是否默认限制为 `zh/en`。
- 中文或英文明确选择时映射为 `zh` 或 `en`。
- 会议优先使用语义断句；若实际延迟过高，可切换 VAD 并配置 `max_sentence_silence`。
- 不默认过滤“嗯、啊”等语气词，保留原始语义；纪要生成阶段再处理冗余表达。
- 开启 Provider heartbeat，避免长时间静音导致连接被关闭。
- 技术术语通过配置的 `vocabulary_id` 使用热词表，不允许客户端提交任意 vocabulary ID。

必须先收到百炼 `task-started`，才能写入音频二进制帧。

### 8.3 结束任务

客户端音频全部写入后发送：

```json
{
  "header": {
    "action": "finish-task",
    "task_id": "与 run-task 相同的 UUID",
    "streaming": "duplex"
  },
  "payload": {
    "input": {}
  }
}
```

收到 `task-finished` 才算 Provider 正常结束。`task-failed`、连接异常和结束超时都要映射为失败，不能返回假成功。

## 9. Provider 抽象与代码边界

Provider port 位于 vision biz，百炼实现位于 vision data：

```text
app/vision/internal/server
    WebSocket 升级、帧限制、Origin、读写循环
            |
app/vision/internal/service
    客户端消息与 biz 命令/事件映射
            |
app/vision/internal/biz
    TranscriptionUsecase、状态、配额边界、ASRProvider port
            ^
            |
app/vision/internal/data
    session repo、Redis adapter、Provider adapter
            |
app/vision/internal/data/asr/paraformer
    百炼 WebSocket、事件解析、错误翻译
```

建议能力接口：

```go
type ASRProvider interface {
	Name() ASRProviderName
	Start(ctx context.Context, input StartTranscriptionInput) (ASRSession, error)
}

type ASRSession interface {
	WriteAudio(ctx context.Context, chunk AudioChunk) error
	Events() <-chan ASREvent
	Finish(ctx context.Context) error
	Close() error
}
```

约束：

- `ASRProviderName`、session 状态和事件类型使用 biz 中的领域类型，不使用散落的裸字符串。
- `Events()` 通道必须有明确 owner、关闭规则和有界缓冲。
- 每个读写 goroutine 有 context、超时和 panic recovery。
- `Finish` 幂等；重复调用不重复发送 Provider 结束命令。
- adapter 负责百炼 JSON、header、Provider task ID 和错误结构，service 不解析厂商 payload。
- 不把 ASR 业务接口放进 `internal/platform`，它属于 vision 领域。

## 10. 配置方案

`B-200` 已在 `internal/conf/conf.proto` 定义 Redis、ASR 和默认会议配额配置，并通过 `make config` 生成 `internal/conf/conf.pb.go`。后续调整仍必须先修改 Proto 再重新生成，不得手工修改生成文件。

建议配置结构：

```yaml
redis:
  addr: ${VISION_REDIS_ADDR:127.0.0.1:6379}
  username: ${VISION_REDIS_USERNAME:}
  password: ${VISION_REDIS_PASSWORD:}
  db: 0
  dial_timeout: 3s
  read_timeout: 2s
  write_timeout: 2s
  pool_size: 50
  min_idle_conns: 5
  tls_enabled: false
  tls_server_name: ${VISION_REDIS_TLS_SERVER_NAME:}
  key_prefix: "vision:"

asr:
  provider: ASR_PROVIDER_BAILIAN_PARAFORMER
  session_timeout: 14400s
  connect_timeout: 5s
  read_timeout: 30s
  write_timeout: 5s
  finish_timeout: 10s
  max_concurrent_sessions: 100
  max_audio_frame_bytes: 6400
  allow_test_endpoint: false
  bailian:
    api_key: ${BAILIAN_API_KEY:}
    workspace_id: ${BAILIAN_WORKSPACE_ID:}
    endpoint: ${BAILIAN_ASR_ENDPOINT:wss://dashscope.aliyuncs.com/api-ws/v1/inference}
    realtime_model: paraformer-realtime-v2
    file_model: paraformer-v2
    vocabulary_id: ${BAILIAN_ASR_VOCABULARY_ID:}
```

启动时必须校验：

- Provider 名称属于支持的闭集。
- endpoint 使用 `wss`，host 属于允许的阿里云域名或明确配置的测试地址。
- 生产环境 API Key 和 Workspace ID 非空。
- session、连接、读写和 finish 超时均为正数并设有上限。
- 并发数、帧大小和会议时长均有界。
- Redis 地址、连接超时、池大小、TLS 参数和服务 key 前缀在创建连接前校验；启动 Ping 失败时服务返回启动错误。

开发配置只放环境变量占位符，不把真实密钥写入 `configs/vision.yaml`、`.env`、测试快照或日志。

## 11. 会话状态与并发

建议实时 ASR session 状态：

```text
CREATED
  -> CONNECTING
  -> STREAMING
  -> FINISHING
  -> SUCCEEDED

CONNECTING / STREAMING / FINISHING
  -> FAILED
  -> CANCELLED
  -> EXPIRED
```

必须在 biz 中定义允许的转换，不从 server、data 和 worker 分别写状态字符串。

并发要求：

- 全局和每用户实时 session 数有上限。
- 每条连接一个有界音频输入队列和一个有界事件输出队列。
- WebSocket 写操作由单一 goroutine 拥有，避免并发写。
- Provider 写操作由单一 goroutine 拥有，保持音频顺序。
- 慢客户端触发背压或断开，不能无限缓存转写事件。
- 客户端断开时取消 Provider context，并在宽限期后结束 session。
- 服务关闭时停止接受新 session，在截止时间内完成或取消现有连接。

### 11.1 重连边界

百炼 Provider WebSocket 断开后不能假设可以恢复原任务上下文。V1 重连策略为：

1. vision-service 记录最后最终句边界和有限音频重放窗口。
2. 短暂故障时创建新的百炼任务，重放最近未最终确认的音频。
3. 根据时间范围和文本内容去除重放导致的重复片段。
4. 超出重放窗口时向 uTools 返回可重试错误并标记实时稿可能存在缺口。
5. 本地完整录音可用时，通过 `paraformer-v2` 生成最终校正版。

业务 ACK 不能包装成“音频已永久保存”。最终无缺口保证来自本地录音和会后校正，不来自内存中的实时队列。

## 12. 最终片段同步与持久化

临时片段：

- 只推送给当前 uTools 会话。
- 不写 core 数据库。
- 可以随 Provider revision 被替换。

最终片段：

- vision 生成稳定 `segment_id`、序号和时间范围。
- 通过内部 gRPC 批量提交或可靠事件同步到 core。
- core 使用 `(meeting_id, sequence_no)` 和 `segment_id` 幂等去重。
- core 成功确认前，vision 保留有限待同步记录；不能只依赖 WebSocket 已推给客户端。
- 不在跨服务数据库之间建立外键或直接查询。

会后文件转写产生新 transcript revision，不原地静默覆盖旧稿。纪要记录必须引用所使用的 transcript revision。

## 13. 错误处理

Provider adapter 将百炼失败转换为领域错误，不比较错误消息文本：

| Provider 场景 | 领域结果 |
|---|---|
| 401/403、API Key 或 Workspace 错误 | 配置错误，非客户端重试 |
| 参数、音频格式、采样率错误 | `ErrAudioFormatUnsupported` 或启动配置错误 |
| 限流、配额不足 | `ErrProviderRateLimited` / `ErrProviderQuotaExceeded` |
| 网络、握手、5xx、临时超时 | `ErrProviderUnavailable`，允许有界重试 |
| `task-failed` | 根据安全错误码分类，保存脱敏 attempt |
| context cancelled | 保留 `context.Canceled` |
| deadline exceeded | 保留 `context.DeadlineExceeded` |

重试规则：

- 建连失败只做少量指数退避重试，并受会议 context 控制。
- 已经发送音频后的重试必须进入重放与去重流程，不能盲目整段重复。
- 参数错误、鉴权失败和配额不足不自动重试。
- 所有重试记录 attempt、耗时和最终结果。

## 14. 安全与隐私

- uTools 只连接本项目 HTTPS/WSS，不直连百炼。
- 百炼 API Key 只在 vision-service 运行环境中存在。
- WebSocket ticket 短期、一次性、随机生成并在 Redis 中哈希存储。
- WSS upgrade 校验 ticket、Origin 策略、消息大小、连接数和速率。
- 不信任客户端提交的 `meeting_id`、`user_id`、MIME、采样率或时长。
- 不记录原始音频、ticket、access token、转写正文或会议纪要正文。
- 结构化日志只记录安全 ID、阶段、耗时、字节数、Provider 和错误分类。
- `retain_audio=false` 时，完成识别或达到清理期限后删除临时音频。
- 对象存储访问使用内部 object key 和短期签名，不接受任意公网 URL，避免 SSRF。
- 默认不启用百炼数据检查 header；若未来因业务需要启用，必须先完成隐私告知和审批。

## 15. 可观测性和成本记录

每个 ASR attempt 至少记录：

- `meeting_id`、`session_id`、内部 job ID。
- Provider：`bailian`。
- 模型：`paraformer-realtime-v2` 或 `paraformer-v2`。
- Provider task ID。
- 建连耗时、首个临时结果耗时、首个最终结果耗时、总时长。
- 输入音频字节数、发送音频时长、Provider usage duration。
- 最终片段数、临时 revision 数、重连次数和重放秒数。
- 成功、取消、超时、限流、配额或安全错误分类。

建议指标：

- `asr_active_sessions`。
- `asr_session_duration_seconds`。
- `asr_first_partial_latency_seconds`。
- `asr_finalization_latency_seconds`。
- `asr_audio_duration_seconds_total`。
- `asr_provider_errors_total{reason}`。
- `asr_reconnect_total`。
- `asr_estimated_cost_cny_total`。

告警至少覆盖 Provider 鉴权失败、配额不足、错误率突增、首字延迟恶化、Redis 不可用和并发接近上限。

## 16. 测试方案

### 16.1 单元测试

- Provider 事件到领域事件的完整映射。
- `sentence_end` 临时/最终结果。
- heartbeat 结果过滤。
- 缺字段、未知事件、未知错误码和无效 JSON。
- 状态转换、幂等 Finish 和重复片段去重。
- PCM 帧大小、奇数字节、序号和时长边界。
- ticket 哈希、过期、一次性消费和并发消费。

### 16.2 Fake Provider 集成测试

建立本地 Fake WebSocket Provider，覆盖：

- 正常 `task-started -> result-generated -> task-finished`。
- 临时文本多次修订后变成最终文本。
- Provider 慢读造成发送背压。
- 建连失败、中途断开、`task-failed` 和 finish 超时。
- 重放音频后出现重复文本。
- context 取消和服务优雅关闭。

自动测试不得调用真实百炼或消耗真实额度。

### 16.3 真实环境冒烟

使用部署环境注入的真实 Key，至少验证：

- 10 秒普通话 PCM。
- 中文夹英文技术词。
- 30 秒静音后继续说话。
- 语义断句与 VAD 两组参数的延迟和质量。
- 30 分钟会议的连接稳定性和实际 usage duration。
- 停止后最后一句不会丢失。
- 控制台账单与 `ai_job_attempts` 记录基本一致。

真实测试音频必须取得授权，不提交仓库，测试后按保留策略清理。

## 17. 实施顺序

### P0：协议与音频基线

- [ ] 冻结 WebSocket v1：`session_ready`、ACK、heartbeat、revision、finish 和 error。
- [ ] 客户端增加 AudioWorklet PCM16 采集器。
- [ ] 把实时 PCM 和留存录音拆成两个 adapter，避免职责混合。
- [ ] 更新 `api-contract.md` 的 PCM、vision WSS 和停止握手约定。

### P1：配置和基础设施

- [ ] 在 `internal/conf/conf.proto` 增加 Redis 和 ASR 配置并运行 `make config`。
- [ ] 为 core、vision 配置独立 Redis ACL 和 key 前缀。
- [ ] 增加有超时和指标的 Redis adapter。
- [ ] 加入百炼环境变量占位符和启动校验。

### P2：API 与领域

- [ ] Proto-first 定义会议控制 API 和 core-to-vision 内部 gRPC 合同。
- [ ] vision biz 定义 session 状态、Provider 类型、端口和用例。
- [ ] core meeting biz 定义会议状态、额度和资源所有权。
- [ ] 生成 API/OpenAPI 后检查兼容性，不手改生成文件。

### P3：Paraformer adapter

- [ ] 实现百炼 WebSocket 握手和 `run-task`。
- [ ] 实现二进制音频顺序发送。
- [ ] 实现 Provider 事件解析、心跳过滤和领域映射。
- [ ] 实现 `finish-task`、超时、取消和错误翻译。
- [ ] 增加 Fake Provider 测试。

### P4：业务 WebSocket

- [ ] vision 注册自定义 WSS route。
- [ ] 实现一次性 ticket、Origin、帧大小、速率和并发限制。
- [ ] 实现单 owner 读写循环、有界队列和背压。
- [ ] 实现 ACK、临时 revision、最终片段和完成事件。
- [ ] 实现最终片段向 core 的幂等同步。

### P5：客户端联调

- [ ] `TranscriptionClient.connect()` 等待 `session_ready`。
- [ ] `finish` 后等待 `session_finished` 再关闭。
- [ ] 实现 ACK 队列、暂停发送、重连上限和取消。
- [ ] 临时片段按 `segmentId + revision` 替换，最终片段不可回退。
- [ ] 在真实 uTools 中验证权限、长会议、断网、休眠和退出。

### P6：会后二次识别

- [ ] 接入 `paraformer-v2` 文件识别。
- [ ] 只在质量、缺口或付费规则满足时触发。
- [ ] 保存新的 transcript revision 并基于该版本重新生成纪要。
- [ ] 对比实时稿、最终稿、实际费用和处理时延。

## 18. 验收标准

- uTools 不含百炼 Key、Workspace ID 或可复用厂商凭证。
- uTools 使用 core 登录和创建会议，并通过 vision 短期 ticket 建立 WSS。
- 16kHz/16bit/mono PCM 可以连续发送并获得临时和最终文本。
- `audio/webm;codecs=opus` 不会被错误声明为 Paraformer `opus` 透传。
- 相同 ticket 并发使用时只有一个连接成功。
- 客户端收到 `session_ready` 后才发送音频，收到 `session_finished` 后才关闭。
- 慢网络、百炼断开和 Redis 故障不会造成无界内存、进程 panic 或假成功。
- 只有最终片段持久化，重复提交不会形成重复文本。
- 日志和错误响应不包含密钥、token、音频或会议正文。
- Fake Provider 自动测试通过。
- 真实普通话与中英混合冒烟通过，尾部文本不丢失。
- 30 分钟以上会议的内存、CPU、延迟和账单记录达到产品阈值。
- `gofmt`、`buf lint`、`go test ./...`、`go vet ./...`、core/vision build、客户端 typecheck/test/build 和 `git diff --check` 全部通过。

## 19. 本期不做

- 不让 uTools 直连百炼或接收百炼临时凭证。
- 不使用 SSE 承载实时音频或实时转写；SSE 可在后续用于纪要生成进度。
- 不在 Renderer 或 vision 内存中累积整场原始音频。
- 不为每个实时连接启动 FFmpeg。
- 不承诺实时说话人身份识别。
- 不把 Provider 原始事件直接透传给客户端。
- 不把 Redis 当作两个服务共享领域数据的捷径。
- 不默认对所有会议重复执行实时和文件识别。
- 不在 V1 同时实现第二家 ASR，但保留 Provider 接口和错误契约。
