# 客户端接口约定

状态：B-300 鉴权基线。用户与会议 HTTP 以 `api/user/v1/user.proto`、`api/meeting/v1/*.proto` 和生成的 `openapi.yaml` 为准；WebSocket v1 以本文和 Paraformer 接入方案为准。

Proto JSON 使用 lowerCamelCase 字段和完整 enum 名称；客户端 HTTP adapter 已按该契约严格解析。

## 1. 环境

```dotenv
VITE_API_BASE_URL=https://api.example.com
VITE_USE_MOCK_API=false
```

生产必须使用 HTTPS/WSS。浏览器 Mock 模式不依赖后端。

## 2. uTools 登录换取

```http
POST /v1/auth/utools/login
Content-Type: application/json

{
  "temporaryToken": "utools one-time token",
  "deviceId": "stable-client-device-id"
}
```

响应：

```json
{
  "accessToken": "business access token",
  "refreshToken": "business refresh token",
  "expiresInSeconds": 3600,
  "user": {
    "id": "internal-user-uuid",
    "nickname": "uTools nickname",
    "avatarUrl": "https://example.com/avatar.png",
    "status": "active",
    "createdAt": "2026-08-27T10:00:00Z",
    "updatedAt": "2026-08-27T10:00:00Z"
  },
  "isNewUser": true,
  "onboardingRequired": true
}
```

插件使用 `utools.fetchUserServerTemporaryToken()` 获取一次性临时令牌；令牌为空或不足 32 字节时不要发起登录。后端调用 uTools 服务端接口验证令牌，使用返回的 `open_id` 与后端配置的 `plugin_id` 作为稳定身份键，并绑定内部用户 ID。插件和后端都不能把昵称当身份凭证，后端不会保存或记录临时令牌。

除登录、刷新和退出外，受保护接口统一发送 `Authorization: Bearer <access-token>`。当前会议接口及用户资料、训练计划、打卡接口都从访问令牌取得用户，不信任路径或请求体中的 `user_id`。客户端应按稳定错误 reason 处理 `UNAUTHENTICATED`、`ACCESS_TOKEN_INVALID`、`ACCESS_TOKEN_EXPIRED` 和 `USER_DISABLED`。

## 3. 创建会议

```http
POST /v1/meetings
Authorization: Bearer <access-token>
Idempotency-Key: <uuid>
Content-Type: application/json

{
  "language": "MEETING_LANGUAGE_AUTO",
  "retainAudio": false,
  "transcriptionConsent": true
}
```

允许的 `language` 为 `MEETING_LANGUAGE_AUTO`、`MEETING_LANGUAGE_ZH_CN`、`MEETING_LANGUAGE_EN_US`。响应示例：

```json
{
  "meeting": {
    "meetingId": "uuid",
    "status": "MEETING_STATUS_RECORDING",
    "transcriptionStatus": "TRANSCRIPTION_STATUS_PENDING",
    "language": "MEETING_LANGUAGE_AUTO",
    "retainAudio": false,
    "grantedAudioDuration": "7200s",
    "startedAt": "2026-08-27T10:00:00Z",
    "createdAt": "2026-08-27T10:00:00Z",
    "updatedAt": "2026-08-27T10:00:00Z"
  },
  "transcriptionSession": {
    "sessionId": "uuid",
    "websocketUrl": "wss://vision.example.com/v1/realtime/transcriptions",
    "sessionTicket": "short-lived-ticket",
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

`grantedAudioDuration` 是本次预占后允许发送的硬上限，客户端不能扩大。实际值是单场上限与用户本月剩余额度的较小值。

## 4. 查询用户额度

```http
GET /v1/meeting-quota
Authorization: Bearer <access-token>
```

```json
{
  "quota": {
    "periodStart": "2026-08-01T16:00:00Z",
    "periodEnd": "2026-09-01T16:00:00Z",
    "limit": "9000s",
    "baseLimit": "7200s",
    "purchasedLimit": "1800s",
    "totalLimit": "9000s",
    "consumed": "600s",
    "reserved": "1200s",
    "remaining": "7200s",
    "maxMeetingDuration": "14400s",
    "maxConcurrentMeetings": 1,
    "activeMeetings": 1
  }
}
```

V1 周期按 `Asia/Shanghai` 自然月计算，时间字段以 UTC RFC 3339 返回。

用户首次查询额度或创建会议时，core 将当时生效的基础策略快照到
`meeting_usage_periods.base_quota_seconds`。同月后续策略变更不追溯修改该快照；
已支付的 `orders.type = meeting_quota` 订单累计到 `purchasedLimit`，并随本周期结束清零。
`limit` 为兼容旧客户端保留，值与 `totalLimit` 相同。

后端 B-350 已实现额度 policy、用户 override、reservation、累计上报、幂等结算和 Redis 创建限流。该 HTTP 路由会随 B-400 meeting service 一起注册；在此之前，客户端不能把“Proto/OpenAPI 中已有路径”当成运行时已经开放。

## 5. WebSocket v1

插件必须使用创建会议响应中的完整 `websocketUrl`；该地址指向 vision-service，可以和 core-service 不同域。连接建立后第一条消息发送：

```json
{
  "version": 1,
  "type": "start",
  "sessionTicket": "short-lived-ticket",
  "audio": {
    "mimeType": "audio/pcm;rate=16000",
    "sampleRate": 16000,
    "channels": 1,
    "chunkDurationMs": 200
  }
}
```

WebSocket `open` 不表示 ASR 已准备好。vision 消费 ticket 并收到百炼 `task-started` 后返回：

```json
{
  "version": 1,
  "type": "session_ready",
  "sessionId": "uuid",
  "acceptedAudio": {
    "format": "pcm",
    "sampleRate": 16000,
    "channels": 1
  },
  "grantedAudioSeconds": 7200
}
```

客户端必须收到 `session_ready` 后才发送音频。

音频采用二进制消息：

```text
{"version":1,"type":"audio_chunk","sequenceNo":1,"capturedAt":1787800000000,"mimeType":"audio/pcm;rate=16000"}\n
<PCM16LE bytes>
```

服务端接受帧后返回累计 ACK：

```json
{
  "version": 1,
  "type": "ack",
  "ackSequenceNo": 1,
  "acceptedAudioMs": 200
}
```

ACK 表示 vision 已接受帧，不表示音频已持久化或已经产生最终文本。重复 sequence 不重复识别和计量。

服务端转写片段：

```json
{
  "version": 1,
  "type": "transcript_segment",
  "segmentId": "segment-uuid",
  "sequenceNo": 1,
  "revision": 3,
  "startOffsetMs": 0,
  "endOffsetMs": 1800,
  "speakerLabel": null,
  "language": "zh",
  "content": "我们先确认今天的议题。",
  "isFinal": true
}
```

同一临时句复用 `segmentId` 并递增 `revision`；客户端替换旧临时文本。最终片段不可被后续临时片段覆盖。

心跳：

```json
{ "version": 1, "type": "ping", "sentAt": 1787800000000 }
```

```json
{ "version": 1, "type": "pong", "sentAt": 1787800000000 }
```

停止控制消息携带最后已发送序号：

```json
{ "version": 1, "type": "finish", "lastSequenceNo": 1200 }
```

vision 继续接收百炼尾部结果并把最终片段放入可靠同步路径，然后返回：

```json
{
  "version": 1,
  "type": "session_finished",
  "lastAckSequenceNo": 1200,
  "finalSegmentSequenceNo": 86,
  "acceptedAudioMs": 238000,
  "finishReason": "client_finished"
}
```

客户端收到 `session_finished` 后才正常关闭 WebSocket。`finishReason` 的 V1 闭集为 `client_finished`、`quota_exhausted`、`cancelled`、`expired`。

额度耗尽时 vision 不再接受新音频，但必须先 flush 已接收音频并返回最后的最终片段。随后发送 `MEETING_QUOTA_EXCEEDED` error 和 `session_finished`。

## 6. 停止会议

正常顺序固定为：

1. uTools 停止采集并发送 WebSocket `finish`。
2. 等待 `session_finished`，再关闭 WebSocket。
3. 调用 core-service `StopMeeting`。

如果客户端在第 2 步之前崩溃，vision 按 session 超时进行 flush/取消和用量上报；`StopMeeting` 也可以幂等触发服务端补偿结束，不要求客户端永远在线。

```http
POST /v1/meetings/{meeting_id}:stop
Authorization: Bearer <access-token>
Idempotency-Key: <uuid>
Content-Type: application/json

{}
```

响应的 `meeting.status` 允许为 `MEETING_STATUS_PROCESSING`，`transcriptionStatus` 可以已经为 `TRANSCRIPTION_STATUS_SUCCEEDED`。停止响应不包含纪要，客户端通过以下独立接口查询或重新生成：

```text
GET  /v1/meetings/{meeting_id}/summary
POST /v1/meetings/{meeting_id}/summary:regenerate
```

重新生成请求必须携带 `Idempotency-Key`。纪要状态为 `NOT_STARTED`、`PENDING`、`PROCESSING`、`SUCCEEDED` 或 `FAILED`；成功结果包含主题、摘要、关键讨论、决定、待办、风险、模型、Prompt 版本和转写快照版本。

重复 `StopMeeting` 使用相同或新的幂等键都不能重复结束 Provider、重复结算额度或重复提交最终片段；已经终止的会议返回当前状态。

## 7. 当前已冻结和后续接口

B-100 已冻结：

- `POST /v1/meetings`
- `POST /v1/meetings/{meeting_id}:stop`
- `GET /v1/meetings/{meeting_id}`
- `GET /v1/meetings/{meeting_id}/transcript-segments`
- `GET /v1/meeting-quota`

完整需求还包括：

- `GET /v1/meetings`
- `DELETE /v1/meetings/{meeting_id}`
- `POST /v1/meetings/{meeting_id}/audio-uploads`
- `POST /v1/meetings/{meeting_id}/audio-uploads/{upload_id}:complete`
- `GET /v1/meetings/{meeting_id}/summary`
- `POST /v1/meetings/{meeting_id}/summary:regenerate`
- `POST /v1/meetings/{meeting_id}/exports`
- `GET /v1/meetings/{meeting_id}/exports/{export_id}`

所有列表接口使用有上限的 `page_size` 和不透明 `page_token`。

## 8. 稳定错误

客户端按 Kratos `reason` 决定提示和重试，不比较错误消息文本。至少处理：

- `MEETING_QUOTA_EXCEEDED`
- `MEETING_CONCURRENCY_LIMIT_EXCEEDED`
- `MEETING_DURATION_EXCEEDED`
- `MEETING_RATE_LIMITED`
- `MEETING_STATE_CONFLICT`
- `TRANSCRIPTION_TICKET_INVALID`
- `TRANSCRIPTION_SESSION_EXPIRED`
- `AUDIO_SEQUENCE_INVALID`
- `TRANSCRIPTION_BACKPRESSURE`
- `AUDIO_FORMAT_UNSUPPORTED`
- `ASR_PROVIDER_UNAVAILABLE`
- `SUMMARY_GENERATION_FAILED`
- `UNAUTHENTICATED`
- `PERMISSION_DENIED`

Provider 原始错误、SQL、对象存储信息和密钥不得到达插件。
