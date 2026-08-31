# AI 会议助手后端需求说明

版本：V1.0  
状态：需求基线，分阶段实施中（B-300 已完成）  
适用范围：`tiehu-fitness` 仓库提供的后端 API 及 AI 处理能力  
客户端：uTools 插件（由独立客户端项目实现）

## 1. 文档目的

本文将《AI 会议助手 uTools 插件需求文档》转换为本仓库可执行的后端需求。本文描述服务边界、业务流程、API 能力、领域状态、数据归属、隐私要求和 MVP 验收标准，不包含 uTools 插件的界面与桌面采集实现。

本需求是在现有项目中增加“会议”业务域，不改变仓库的双服务部署结论：

- 不新增 `meeting-service`、`asr-service` 或独立 worker 程序。
- 不采用 Gin；继续使用 Go、Kratos v3、Protobuf、gRPC/HTTP、PostgreSQL 和 GORM。
- 对外会议 API 由 `core-service` 提供。
- 音频媒体、ASR、LLM 和后台 AI 任务由 `vision-service` 承担。
- 两个服务拥有独立数据，不直接读写对方数据库。

## 2. 产品目标与成功标准

后端应支持 uTools 插件完成以下最短路径：

1. 用户登录并开始会议。
2. 插件持续发送音频，后端返回实时转写片段。
3. 用户停止会议，后端完成最终转写并异步生成结构化纪要。
4. 用户查看会议、逐段文本和纪要。
5. 用户导出 Markdown，或删除会议及其云端数据。

第一阶段需要验证：

- 用户能否稳定完成一次从开始到纪要生成的会议。
- 中英文转写和会议纪要质量是否可用。
- 用户是否持续使用，并愿意为更高分钟数或高级模型付费。

商业化只作为容量和数据模型的后续约束。MVP 不实现订阅、支付、团队空间或知识库。

## 3. 范围

### 3.1 V1.0 后端必须完成

- 复用现有用户认证和访问令牌。
- 创建、停止、查询、分页列出和删除会议。
- 为每次会议建立实时转写会话。
- 接收麦克风或混合后的系统声音音频流。
- 支持最终录音文件直传对象存储，并确认上传完成。
- 调用可替换的 ASR Provider，支持中文、英文和自动语言检测。
- 向插件返回临时及最终转写片段，只持久化最终片段。
- 停止会议后异步生成结构化会议纪要。
- 查询 AI 处理状态和稳定的失败原因。
- 导出 Markdown。
- 支持“不保留原始录音”和删除云端会议数据。
- 对时长、帧大小、连接数、上传大小和文本规模设置边界。

### 3.2 V1.0 不包含

- uTools 插件 UI、插件打包、快捷入口和本地缓存清理实现。
- macOS/Windows 的麦克风、系统声音或屏幕采集实现。
- 屏幕录制、视频上传、OCR、截图或 VLM 分析。
- 多说话人声纹识别、真实姓名自动识别。
- TXT、PDF 导出。
- 自动同步第三方任务系统。
- 订阅计费、企业团队、权限组和知识库。
- 完全离线的本地 ASR/LLM。

原始需求中的“Markdown、TXT、PDF 导出”与“MVP 只要求 Markdown”存在范围差异。本需求以 MVP 范围为准：V1.0 只交付 Markdown，TXT/PDF 进入后续版本。

### 3.3 客户端与后端责任边界

uTools 插件负责：

- 获取 macOS/Windows 麦克风、系统音频和屏幕录制权限。
- 采集、混音、编码、断线缓存和最终文件生成。
- 展示计时、实时文本、处理进度和导出结果。
- 停止会议后自动完成音频上传。
- 清理本地录音与缓存。

后端负责：

- 鉴权、资源归属校验、会议生命周期和幂等控制。
- 接收音频流、持久化媒体、调度 ASR/LLM、保存结果。
- 提供历史记录、结构化纪要、Markdown 导出和云端删除能力。

后端不承诺直接“录制系统声音”。插件应向后端发送已经采集好的单轨混合音频；分轨录音可以作为后续增强。

## 4. 双服务架构与数据归属

```text
uTools 插件
    |
    | HTTPS：登录、会议控制、额度和查询
    v
core-service
    |- 登录令牌验证
    |- 会议、最终转写、纪要、导出
    |- 用户归属、额度预占和用量账本
    |
    | internal gRPC：会话控制、最终片段和累计用量
    v
vision-service
    |- 一次性 WebSocket ticket
    |- 对 uTools 提供实时音频 WSS 数据面
    |- ASR / LLM Provider Adapter
    |- 有界 Worker Pool
    |- AI 任务、模型版本、临时媒体生命周期
```

### 4.1 `core-service` 所有权

新增 `meeting` 模块，拥有：

- 会议元数据和业务状态。
- 用户与会议的所有权关系。
- 最终转写片段。
- 结构化会议纪要。
- Markdown 导出记录或生成结果。
- 分钟数额度、预占、用量账本和单用户硬限制（MVP 不收费，但必须控制 Provider 成本）。
- 删除请求和跨服务清理 outbox。

对外新增 `meeting.v1.MeetingService`。它与现有用户、内容模块运行在同一个 `core-service` 进程中，不通过进程内网络调用其他 core 模块。

### 4.2 `vision-service` 所有权

新增会议 AI 处理能力，拥有：

- 对象存储中的音频资产及其保留策略。
- 实时 ASR 会话和 AI 任务状态。
- ASR/LLM Provider 调用、重试和限流。
- 模型、Provider、提示词版本和可追溯元数据。
- 临时音频分片和后台处理错误。

`vision-service` 的 worker 必须调用 vision biz 用例，不得直接更新数据表。会议处理可以扩展现有 AI job 领域，但任务类型与状态必须使用新的明确枚举或领域类型，不能写裸字符串。

### 4.3 服务间约束

- `core-service` 只能通过内部 gRPC 调用 `vision-service`，不能访问 vision 数据库。
- `vision-service` 不查询 core 用户、会议或纪要表。
- 外部请求中的 `user_id` 不可信。用户身份必须来自已验证的访问令牌。
- 跨数据库操作不假设分布式事务。删除、任务提交等需要最终一致性的操作使用 outbox、幂等命令和可重试消费。
- 对象存储引用使用内部 asset ID/object key；不在数据库持久化长期可用的公开 URL。

## 5. 核心业务流程

### 5.0 uTools 登录与访问鉴权

1. 插件调用 `utools.fetchUserServerTemporaryToken()` 获取一次性临时令牌，并连同可选设备 ID 提交到 `POST /v1/auth/utools/login`。
2. `core-service` 使用部署配置的 `plugin_id` 和 `plugin_secret` 调用 uTools 官方 `https://open.u-tools.cn/baseinfo`，对请求签名，并校验响应签名及十分钟时间窗口。
3. core 使用 `(plugin_id, open_id)` 作为稳定且唯一的外部身份；昵称、头像和会员状态只作为资料，不能作为身份凭证。
4. 首次验证成功时创建内部用户与 uTools identity，重复或并发登录复用同一个用户；数据库唯一约束是并发正确性的最终保障。
5. 登录复用已有 access/refresh session。访问令牌只以 SHA-256 hash 持久化；一次性 uTools 临时令牌不保存、不写日志。
6. 受保护接口通过 Bearer 中间件验证 access session 的存在、撤销状态、过期时间和用户状态，再把内部用户 ID 注入 context。service 不信任请求携带的 `user_id`。

uTools 登录失败需要返回稳定、脱敏的错误原因；Provider 响应正文、签名、插件密钥和临时令牌不得返回客户端。

### 5.1 开始会议

1. 插件调用创建会议接口，并提供语言偏好、客户端信息、是否保留录音和幂等键。
2. `core-service` 从访问令牌取得用户身份，原子检查并预占月度额度，同时检查并发会议数和创建频率。
3. `core-service` 创建 `RECORDING` 状态的会议和唯一额度 reservation。
4. `core-service` 将本次获准的最大音频秒数传给 `vision-service`，创建有时效的 ASR 会话。
5. 返回会议 ID、流式连接参数、音频约束、获准时长和会话过期时间。

重复的创建请求使用相同幂等键时必须返回同一会议，不能重复扣减额度或创建多个 ASR 会话。

### 5.2 实时转写

1. 插件使用 core 创建会议响应中的一次性 ticket，通过 WebSocket 直连 `vision-service`。
2. 插件按递增 `sequence_no` 发送音频帧和采集时间。
3. `vision-service` 使用受限缓冲和背压把 PCM 音频发送给 ASR Provider。
4. vision 将临时片段和最终片段直接推送给插件。
5. vision 通过内部 gRPC/outbox 将最终片段和累计用量可靠同步给 core；只有最终片段写入 core 数据库。
6. core 对最终片段使用 `(meeting_id, segment_sequence)` 和稳定 segment ID 去重。

WebSocket 需要心跳、ACK、最大未确认帧数和会话过期机制。客户端短暂断线后可以携带最后确认的 `sequence_no` 恢复；重复帧不能产生重复文本。

MVP 不提供可靠的姓名识别。`speaker` 为空或使用 `speaker_1` 之类的临时标签。原始示例中的“张三/李四”需要后续多人分离与用户校正能力才能可靠实现。

### 5.3 停止会议与生成纪要

1. 插件停止采集，向 vision WebSocket 发送 `finish`。
2. `vision-service` 刷新 ASR 尾部缓冲、可靠提交最后的最终片段并返回 `session_finished`。
3. 插件关闭 WebSocket 并调用 core 停止会议接口；客户端异常退出时由 session 超时和 Stop 补偿流程兜底。
4. `core-service` 幂等结算额度预占，并将会议从 `RECORDING` 转为 `PROCESSING`。
5. 如用户选择保留录音，插件完成对象存储直传；如不保留，服务端清理临时音频。
6. core 记录稳定的转写快照版本，提交会议总结任务。
7. LLM 对完整转写执行结构化总结；长会议采用分段摘要再合并，避免无界输入。
8. core 校验结构化结果并保存纪要，将会议转为 `COMPLETED`；部分产物成功时转为 `PARTIALLY_COMPLETED`。

停止请求可重试。重复停止不能重复创建总结任务。

### 5.4 查询、导出和删除

- 插件可以分页查询当前用户的会议列表。
- 会议详情返回各处理阶段状态，不要求长请求等待 AI 完成。
- 转写片段按时间和稳定序号分页返回。
- 纪要对外使用明确的 Proto 字段，不返回无界任意 JSON。
- Markdown 导出基于已保存的纪要版本生成，内容编码为 UTF-8。
- 删除会议先将 core 资源标记为删除中，再通过 outbox 清理 vision 任务和对象存储；清理成功后完成软删或物理删除。
- 删除接口必须幂等，并且用户不能读取或删除其他用户的会议。

## 6. 音频与上传约定

### 6.1 实时音频

实时流不直接使用完整 WAV/MP3 文件。Paraformer V1 固定使用 PCM signed 16-bit little-endian、16 kHz、mono。浏览器 MediaRecorder 的 WebM/Opus 不能伪装成 Paraformer 要求的 Ogg/Opus 透传。

服务端在创建会议响应中返回本次会话实际接受的 codec、采样率、声道数、建议帧时长和最大帧字节数。所有输入必须经过格式、长度和序号校验。

### 6.2 最终录音文件

MVP 最终录音文件接受 WAV 或 MP3。流程为：

1. 插件申请短期上传凭证。
2. 插件直传对象存储，不让大文件穿过 core 业务进程。
3. 插件提交上传完成命令。
4. `vision-service` 校验 object key、大小、MIME、音频头、时长和所有权后标记资产可用。

不能相信客户端提交的 MIME、文件扩展名、时长或对象地址。下载 Provider 输入时应使用受控对象存储，不允许任意 URL，避免 SSRF。

### 6.3 “是否上传”的准确含义

云端 ASR 必然需要把音频数据传给后端或第三方 Provider。因此 V1.0 将隐私选项拆为：

- `retain_audio = false`：音频只为实时/最终识别临时传输，任务完成或超时后删除原始音频，不形成可回放录音。
- `retain_audio = true`：除识别外，保存最终录音，直到用户删除或达到保留期限。

“完全不把音频发送到云端”意味着本地 ASR，超出 V1.0 范围。客户端必须在开始前向用户说明这一点并取得同意。

## 7. API 能力清单

实际 API 必须先在 `api/meeting/v1/meeting.proto` 定义，再生成 HTTP/gRPC/OpenAPI 代码。下列路径是需求基线，字段号和最终消息拆分在 Proto 设计阶段确认。

| RPC | HTTP | 用途 |
|---|---|---|
| `CreateMeeting` | `POST /v1/meetings` | 创建并开始会议，要求幂等键 |
| `StopMeeting` | `POST /v1/meetings/{meeting_id}:stop` | 停止采集并触发后处理 |
| `GetMeeting` | `GET /v1/meetings/{meeting_id}` | 查询会议与各阶段状态 |
| `ListMeetings` | `GET /v1/meetings` | 按时间倒序分页查询当前用户会议 |
| `DeleteMeeting` | `DELETE /v1/meetings/{meeting_id}` | 删除会议及云端关联数据 |
| `CreateAudioUpload` | `POST /v1/meetings/{meeting_id}/audio-uploads` | 创建短期直传凭证 |
| `CompleteAudioUpload` | `POST /v1/meetings/{meeting_id}/audio-uploads/{upload_id}:complete` | 确认并校验上传 |
| `ListTranscriptSegments` | `GET /v1/meetings/{meeting_id}/transcript-segments` | 分页查询最终转写片段 |
| `GetMeetingSummary` | `GET /v1/meetings/{meeting_id}/summary` | 查询结构化纪要 |
| `RegenerateMeetingSummary` | `POST /v1/meetings/{meeting_id}/summary:regenerate` | 以指定转写版本重新生成，要求幂等键 |
| `ExportMeeting` | `POST /v1/meetings/{meeting_id}/exports` | 创建 Markdown 导出 |
| `GetMeetingExport` | `GET /v1/meetings/{meeting_id}/exports/{export_id}` | 查询导出结果或短期下载地址 |

实时转写使用：

```text
WebSocket wss://<vision-host>/v1/realtime/transcriptions
```

vision 的 Kratos HTTP Server 通过自定义路由适配 WebSocket，并消费 vision 自己签发的一次性 ticket。core-to-vision 使用内部控制 RPC；vision-to-core 使用最终片段和累计用量 RPC/可靠 outbox，不通过 core 转发实时音频。WebSocket 消息协议包含版本字段，并在 `docs` 中单独固化。

所有列表接口必须使用有上限的 `page_size` 和不透明 `page_token`。所有创建、停止、完成上传、重新生成和导出命令必须定义重试语义。

## 8. 对外领域模型

### 8.1 会议状态

Proto 枚举必须包含 `UNSPECIFIED = 0`，biz 中使用独立领域类型并显式转换。

```text
RECORDING
    -> PROCESSING
        -> COMPLETED
        -> PARTIALLY_COMPLETED
        -> FAILED
    -> CANCELLED
```

- `RECORDING`：接受音频。
- `PROCESSING`：已停止，正在刷新转写、校验上传或生成纪要。
- `COMPLETED`：最终转写和纪要均可用。
- `PARTIALLY_COMPLETED`：转写可用，但录音或纪要失败；允许重试失败阶段。
- `FAILED`：没有可用的最终转写，会议处理失败。
- `CANCELLED`：录制阶段被用户取消。

状态只能通过 biz 中的允许转换表改变，不能由 service、data 或 worker 任意赋值。

### 8.2 AI 任务状态

```text
PENDING -> PROCESSING -> SUCCEEDED
                      -> FAILED
        -> CANCELLED
```

任务类型至少包括 `REALTIME_TRANSCRIPTION`、`FINAL_TRANSCRIPTION` 和 `MEETING_SUMMARY`。类型、状态、语言、导出格式等闭集全部使用 Proto 枚举和 biz 领域类型。

### 8.3 转写片段

最终转写片段至少包含：

- `segment_id`
- `sequence_no`
- `start_offset` 和 `end_offset`（`google.protobuf.Duration`）
- 可空 `speaker_label`
- `content`
- `language`
- `confidence`（Provider 不提供时为空）
- `created_at`（`google.protobuf.Timestamp`）

临时结果需要 `revision` 或替换范围，便于客户端覆盖旧文本；临时结果不进入历史查询。

### 8.4 结构化会议纪要

LLM Provider 必须返回符合版本化 JSON Schema 的结果。vision service 完成语法和大小校验，core service 转换为类型化领域对象。对外结构至少包括：

- `topic`
- `abstract`
- `key_discussions[]`
- `decisions[]`
- `action_items[]`
  - `assignee`
  - `task`
  - 可空 `due_at` 或原文期限
  - `status`（初始为未指定或待处理）
- `risks[]`
- `source_transcript_revision`
- `model_name`
- `prompt_version`
- `generated_at`

对数组数量、单项长度和总输出大小设置上限。Provider 输出中的未知字段不直接透传给客户端。

## 9. 持久化模型

Core 和 Vision 启动时分别对本服务显式 Model 列表运行 `AutoMigrate`；复杂约束、
部分索引、触发器、重命名和数据回填继续使用成对 SQL migration。

### 9.1 core 数据库

建议表：

- `meetings`
  - `id`, `user_id`, `title`, `language`, `status`
  - `retain_audio`, `audio_asset_id`
  - `started_at`, `stopped_at`, `created_at`, `updated_at`, `deleted_at`
  - `transcript_revision`, `summary_version`, `summary_status`
  - 当前总结的 `summary_content`（JSONB）、Provider、模型、Prompt 版本、Token 与失败原因
- `meeting_transcript_segments`
  - `id`, `meeting_id`, `sequence_no`, offsets, `speaker_label`, `content`, `language`, `confidence`
  - 唯一约束 `(meeting_id, sequence_no)`
- Core 当前阶段只在 `meetings` 保存最新结构化总结，不单独维护总结历史表。
- Vision 的 `meeting_summary_jobs` 保留异步执行记录，并保存最近一次不含认证头的 LLM 请求体、原始响应体、HTTP 状态和耗时，便于开发排查。
- `meeting_exports`
  - `id`, `meeting_id`, `format`, `status`, `asset_id`, timestamps
- `user_meeting_quota_overrides`
  - `user_id` 唯一，可选覆盖月度秒数、单场秒数和并发数
  - `status` 是 `active`/`disabled` 闭集，不存在或 disabled 时使用服务端默认策略
- `meeting_usage_periods`
  - `(user_id, period_start)` 唯一，保存自然月 `consumed_seconds` 和 `reserved_seconds`
- `meeting_usage_reservations`
  - `meeting_id` 唯一，保存 grant、单调累计 reported、终态、过期和结算时间
- `meeting_usage_records`
  - `(meeting_id, usage_kind)` 唯一，保存最终计费时长、Provider 审计用量和结算原因
- `outbox_events`
  - 跨服务任务提交与删除清理事件

`audio_asset_id` 只是 vision 资源标识，不建立跨数据库外键。

### 9.2 vision 数据库

建议扩展或新增：

- `media_assets`：所有者引用、object key、MIME、大小、时长、校验状态、保留截止时间。
- `ai_jobs`：任务类型、状态、幂等键、重试次数、下次执行时间、安全错误码。
- `ai_job_attempts`：Provider、模型、耗时、token/音频秒数、结果、脱敏错误。
- `transcription_sessions`：短期 session、codec、最后确认序号、过期时间。

数据库对闭集列增加 check constraint；读取未知值必须报错，不能流入业务逻辑。

## 10. 错误、重试和一致性

对客户端返回稳定的 Kratos error reason，例如：

- `MEETING_NOT_FOUND`
- `MEETING_ACCESS_DENIED`
- `MEETING_STATE_CONFLICT`
- `MEETING_ALREADY_RECORDING`
- `TRANSCRIPTION_SESSION_EXPIRED`
- `AUDIO_FORMAT_UNSUPPORTED`
- `AUDIO_FRAME_TOO_LARGE`
- `AUDIO_UPLOAD_INVALID`
- `ASR_PROVIDER_UNAVAILABLE`
- `SUMMARY_GENERATION_FAILED`
- `MEETING_QUOTA_EXCEEDED`

不得向客户端返回 SQL、对象存储凭证、Provider 原始载荷、提示词、堆栈或密钥。Provider 错误在 data adapter 转换为领域可理解的错误；`context.Canceled` 和 `context.DeadlineExceeded` 必须保留。

重试要求：

- ASR/LLM 调用仅在确认可安全重试时重试，使用指数退避、抖动和最大次数。
- 任务幂等键必须避免 Provider 成功但响应丢失后重复生成多份业务结果。
- worker 认领 `PENDING -> PROCESSING` 必须原子化。
- worker panic 边界记录 job ID、结构化上下文和堆栈，并把任务置为可重试或失败，不能返回假成功。
- core 保存纪要和推进会议状态需要事务。

## 11. 隐私与安全

- 所有会议接口都需要认证，并按 token 中的用户身份授权。
- uTools 临时令牌只在登录请求生命周期内使用，不持久化、不缓存、不记录；稳定身份使用 `(plugin_id, open_id)`，不使用昵称。
- uTools Provider 请求和响应均使用 HMAC-SHA256 签名，响应还需校验时间窗口；生产环境只允许官方 HTTPS endpoint，测试 loopback endpoint 必须显式启用。
- access/refresh token 只持久化 hash；鉴权必须检查 session 撤销、访问过期和用户禁用状态。
- 开始会议前记录转写同意和原始音频保留选项。
- 不记录音频内容、完整转写、纪要、令牌或 Provider 密钥到日志。
- 音频对象默认私有；上传和下载 URL 短期有效并限制 object key、方法和大小。
- 数据传输使用 TLS；敏感 Provider 配置通过部署环境的 secret 管理。
- 原始录音、临时分片、转写、纪要和备份分别定义可配置保留期。
- 删除操作覆盖 core 记录、vision 任务、对象存储对象和可识别缓存，并留下不含会议正文的审计结果。
- 如第三方 ASR/LLM 会保留输入，必须在上线前完成供应商数据保留、训练使用、地域和删除能力评估，并在客户端隐私说明中披露。
- 导出下载地址不得永久公开，不得允许通过猜测 ID 访问他人会议。

## 12. 非功能要求

原始需求中的“启动时间小于 2 秒、内存小于 200 MB”是 uTools 客户端指标，不直接作为后端进程指标。后端需要建立以下可观测目标，具体阈值在压测后固化：

- 普通控制 API 不等待 AI Provider，返回可查询的异步状态。
- 实时链路采用有界缓冲和背压，慢客户端不能导致无界内存增长。
- 单用户同时只能有有限个录制中会议，默认建议为 1，可配置。
- 单次会议时长、单帧大小、最终文件大小和转写文本总量必须有配置上限；初始产品上限建议为 4 小时。
- ASR 记录首字延迟、最终片段延迟、实时率、重连次数和丢帧数。
- 总结任务记录排队时长、执行时长、输入规模、输出规模、模型、提示词版本、重试和结果。
- API、gRPC 和 AI job 传播 request ID、meeting ID、job ID 和 trace context。
- 并发代码通过 `go test -race`，断线、取消和服务关闭有确定性测试。

## 13. Provider 设计

### 13.1 B-200 运行配置基线

- core 和 vision 可以连接同一个 Redis 地址，但分别读取 `CORE_REDIS_*` 与 `VISION_REDIS_*` 凭证，并固定使用 `core:`、`vision:` key 前缀；部署环境应为两个服务配置独立 Redis ACL 用户和 key pattern。
- Redis 启动连接会校验 `host:port`、正数读写/拨号超时、连接池上限、TLS 1.2 最低版本和 key 前缀，Ping 失败时服务不得启动。
- vision 从数据库 Provider 配置读取百炼 Workspace、模型和 Vocabulary，并从
  `provider_credentials.api_key` 读取百炼、DeepSeek 明文 API Key；真实凭证不得写入
  YAML、服务日志或 API 响应，数据库及备份必须按密钥材料保护。
- 百炼 endpoint 默认只接受使用 `wss` 的 `*.aliyuncs.com` 主机；自动化测试只有显式启用后才能连接 loopback `ws/wss` 地址。
- 默认会议额度由 core 数据库的 `meeting_quota_policies` 单例行提供；YAML 不保存额度业务数据。core 启动时必须读取并校验该行，创建会议和查询额度时重新读取，因此已提交的数据库修改从下一次请求生效。

### 13.2 B-300 uTools 身份配置基线

- core 从 `UTOOLS_PLUGIN_ID` 和 `UTOOLS_PLUGIN_SECRET` 读取插件身份；真实密钥不得写入 YAML、源码、测试输出或日志。
- `UTOOLS_BASE_INFO_ENDPOINT` 默认固定为 `https://open.u-tools.cn/baseinfo`，`UTOOLS_REQUEST_TIMEOUT` 默认 5 秒，`UTOOLS_RESPONSE_MAX_AGE` 默认 600 秒。
- 空凭证不阻止 core 的其他登录方式启动，但 uTools 登录返回稳定的 `UTOOLS_AUTH_NOT_CONFIGURED`；生产部署在开放 uTools 登录前必须配置凭证。
- 自动化测试使用受控 HTTP transport，不访问真实 uTools 服务；真实插件凭证和官方服务连通性在部署环境做冒烟验证。

### 13.3 B-350 单用户额度实现基线

- 默认策略由 `meeting_quota_policies` 当前行转换为 typed policy，用户 active override 只替换明确存在的月度、单场和并发字段；数据库和 biz 同时校验范围。策略变更只影响后续授权，已创建会议保留原 reservation grant。
- 周期固定为 `Asia/Shanghai` 自然月，边界以 UTC `TIMESTAMPTZ` 保存；非零不足一秒的已接受音频向上取整。
- reservation 通过 `meeting_usage_periods` 行锁串行化同一用户的计算，grant 取单场上限与剩余额度的较小值；并发请求不能透支额度。
- progress 只接受单调增加的累计用量，回退值不覆盖，超过 grant 的值截断到 grant。
- 完成、额度耗尽、取消、失败、过期和 Provider 准备失败走同一个幂等 finalize；迟到 Provider 用量只更新成本审计字段，不再次增加用户 consumed。
- 提供最多 1,000 条一批的跨周期过期 reservation 扫描入口；并发扫描仍通过幂等 finalize 收敛，B-400 负责用有 owner、取消和恢复边界的后台任务调度。
- Redis 固定窗口 key 为 `core:meeting_create_rate:v1:<user_id>:<window_start>`，原子计数并设置 TTL。Redis 故障按配置拒绝，或仅退化到 PostgreSQL 配额与并发检查。
- B-350 提供可注入的 biz/data 能力。公开 `GetMeetingQuota` handler、外部幂等键到 meeting ID 的映射以及“创建会议 + 预占”同事务组合在 B-400 完成；vision 单连接硬限制在 B-700 完成。

在 vision biz 中定义能力明确的端口，例如实时转写、批量转写和结构化总结；具体 Whisper、阿里云、腾讯云、DeepSeek、Qwen 或 GPT 实现在 data adapter。

Provider 选择使用 Strategy/Adapter 的原因是语音与总结供应商在流式协议、语言能力、价格、失败语义和部署方式上存在真实变化点。业务层不能依赖某一家 SDK，也不能使用一个按字符串 `kind` 分发所有 AI 请求的通用 Manager。

Provider 上线前至少评估：

- 中英文及中英混说准确率。
- 流式与最终结果协议。
- 时间戳、标点、热词和说话人能力。
- 最大音频时长、并发限制和价格。
- 超时、限流、重试和幂等语义。
- 数据地域、保留、训练使用和删除政策。
- 结构化 JSON 的可靠性及 schema 约束能力。

## 14. 测试与验收

### 14.1 领域与服务测试

- 会议和 AI job 的全部合法/非法状态转换。
- Proto enum 与 biz 类型的双向显式映射，包括 `UNSPECIFIED` 和未知值。
- 创建、停止、上传完成、总结再生成的重复请求。
- 非所有者访问、无效 UUID、空输入、超长输入和边界数值。
- 临时片段替换、最终片段去重、乱序帧和重连恢复。
- 无转写内容、只有短文本、中英文混合和超长会议。
- Provider 超时、限流、返回非法 JSON、部分成功和重试耗尽。
- 删除期间查询、重复删除及跨服务清理重试。
- 请求取消和 deadline 传播。

### 14.2 数据与并发测试

- PostgreSQL 集成测试覆盖唯一约束、check constraint、事务回滚、outbox 和原子 job claim。
- 受限 worker pool、背压、关闭、panic recovery 和竞态测试。
- 对象存储凭证范围、伪造 MIME、错误文件头、超大文件和越权 object key。

### 14.3 MVP 端到端验收

1. 用户可以开始会议并在一次正常网络会话中持续看到最终转写片段。
2. 短暂断线重连不会重复持久化片段。
3. 停止会议后立即返回处理状态，不阻塞等待 LLM。
4. 处理完成后可查询主题、摘要、讨论、决策、待办和风险。
5. Markdown 内容与同一纪要版本一致，中文编码正确。
6. ASR 或 LLM 失败时显示稳定状态，并能安全重试失败阶段。
7. 用户不能读取、导出或删除其他用户的会议。
8. 选择不保留录音时，最终结果可用但云端没有可回放原始录音。
9. 删除会议后，core、vision 和对象存储的关联数据最终完成清理。

## 15. 30 天实施建议

### 第 1 周：合同与会议生命周期

- 完成 Proto、HTTP 路径、错误原因、状态机和兼容性评审。
- 完成 core/vision migration、Repository port 和基础用例。
- 完成创建、停止、查询、列表、删除和上传凭证骨架。

### 第 2 周：实时 ASR

- 完成 vision WebSocket 协议、core-to-vision 控制 RPC 和 vision-to-core 最终片段/用量同步。
- 接入一个 ASR Provider Adapter。
- 完成序号、ACK、背压、重连、最终片段持久化和观测指标。

### 第 3 周：总结与导出

- 接入一个 LLM Provider Adapter。
- 完成版本化 JSON Schema、长文本分段总结和结果校验。
- 完成纪要查询、重新生成和 Markdown 导出。

### 第 4 周：隐私、可靠性与联调

- 完成音频保留策略、跨服务删除 outbox、额度记录和安全审查。
- 完成异常路径、并发、race、PostgreSQL 集成和端到端测试。
- 与 uTools 插件联调真实会议、断网、超时和 Provider 故障场景。

每次 Proto 变更后按仓库规范运行 `make api`、`buf lint` 并审查生成代码和 OpenAPI；普通变更完成后运行仓库要求的格式化、测试、vet、双服务 build 和 `git diff --check`。

## 16. 后续版本

### V1.1

- 屏幕录制和视频直传。
- 关键帧截图、OCR 和图片理解。
- TXT/PDF 导出。
- 用户手工修正说话人和纪要。

### V2.0

- 实时 VLM、PPT/文档/代码识别。
- 多说话人分离、声纹授权和姓名映射。
- 第三方任务系统同步。
- 团队空间、知识库、细粒度权限和审计。

## 17. 实现前仍需产品确认

以下选择不阻塞需求建档，但进入 Proto 和 Provider 开发前必须确认：

- V1.0 首选 ASR 和 LLM Provider，以及故障时是否允许自动切换供应商。
- 免费用户每月分钟数、单次会议最大时长和超额行为。
- 原始录音、转写、纪要、删除回收站和备份的具体保留期限。
- 是否允许用户关闭“保存录音”但保留转写和纪要（本文默认允许）。
- 是否需要在 MVP 提供标题编辑、转写修订和手工修正纪要。
- Markdown 导出是直接返回内容、生成下载文件，还是两者都支持。
- macOS MVP 是否把麦克风与系统声音混为单轨，以及插件可用的实际编码格式。
