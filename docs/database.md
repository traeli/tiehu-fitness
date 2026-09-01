# PostgreSQL 与 GORM 数据库设计

## 技术选择

项目使用：

- PostgreSQL：持久化数据库。
- GORM：连接池、CRUD、事务和数据库对象映射。
- GORM AutoMigrate：服务启动时同步本服务拥有的完整 Model 列表。
- SQL migration：复杂约束、部分索引、触发器、重命名和数据回填的版本化来源。

Core 和 Vision 在打开 PostgreSQL 后、启动 Repository、Worker 和 Transport 前分别执行
data 层 `AutoMigrateSchema`。执行受 Context 超时和事务级 Advisory Lock 保护；失败会
阻止服务启动。两个服务使用显式且互不交叉的 Model 列表。

## 数据分层

```text
Proto DTO
    ↕
service
    ↕
biz 领域对象和 Repo 接口
    ↕
data Repo 实现
    ↕
data/model GORM 持久化对象
    ↕
PostgreSQL
```

禁止在 `biz` 中导入 GORM。GORM Model 不能直接作为 Proto 响应返回。

## 数据库边界

两个服务使用两个独立数据库：

```text
tiehu_core    -> core-service
tiehu_vision  -> vision-service
```

可以部署在同一个 PostgreSQL 实例，但两个服务不能直接访问对方数据库。

- vision 数据中的 `user_id`、`exercise_code` 只保存外部业务标识，不创建跨数据库外键。
- 两个服务同步通信使用 gRPC。
- 视频等耗时处理使用异步任务。

## 目录

```text
app/core/
├── internal/data/model/model.go
└── migrations/
    ├── 000001_init.up.sql
    ├── 000001_init.down.sql
    ├── 000002_add_password_credentials.up.sql
    ├── 000002_add_password_credentials.down.sql
    ├── 000003_use_email_for_password_credentials.up.sql
    └── 000003_use_email_for_password_credentials.down.sql

app/vision/
├── internal/data/model/model.go
└── migrations/
    ├── 000001_init.up.sql
    └── 000001_init.down.sql

internal/platform/database/postgres.go
```

## core-service 表

| 表 | 作用 |
|---|---|
| `users` | 业务用户主表 |
| `wechat_identities` | 微信 AppID、OpenID 与内部用户映射 |
| `utools_identities` | uTools 插件用户与内部用户映射 |
| `password_credentials` | Web 登录邮箱与 bcrypt 密码哈希 |
| `user_sessions` | 登录设备和 Token 哈希 |
| `fitness_profiles` | 目标、经验、频率、器材和伤病信息 |
| `equipment` | 健身器材内容 |
| `exercises` | 动作、教学视频、要点和常见错误 |
| `training_plans` | 用户训练计划 |
| `training_plan_items` | 计划中的训练日和动作 |
| `workout_sessions` | 一次实际训练 |
| `workout_sets` | 实际完成的组数、次数、重量和 RPE |
| `check_ins` | 每日训练打卡 |
| `meeting_quota_policies` | 实时生效的默认会议额度策略单例行 |
| `user_meeting_monthly_quotas` | 用户自然月基础、购买、已用和预占额度 |
| `orders` | 订单；`type=meeting_quota` 时关联对应月度额度行 |
| `meetings` | 会议生命周期、额度预占/结算、最终转写 JSONB 及当前结构化总结 |

会议域当前只使用以上 4 张表。`meetings` 一行保存一场会议的额度生命周期；
`transcript_segments` JSONB 保存有界的最终片段数组，片段 ID 与序号共同承担幂等检查。
月度余额更新使用 `user_meeting_monthly_quotas` 行锁，避免并发超扣。

微信身份使用唯一约束：

```text
(app_id, open_id)
```

会话表只保存 Token 哈希，不保存可直接使用的明文 Token。微信 `session_key` 字段只允许存放加密密文。

Web 登录邮箱经过语法校验、域名规范化并转为小写后保存。`password_credentials` 只保存 bcrypt 哈希，不保存、记录或返回明文密码。

## vision-service 表

| 表 | 作用 |
|---|---|
| `media_assets` | 图片、视频及对象存储 URI |
| `model_versions` | 器材识别和姿势分析模型版本 |
| `analysis_jobs` | AI 异步任务、状态、重试和结果摘要 |
| `equipment_recognition_results` | 器材候选、置信度和最终编码 |
| `meeting_summary_jobs` | 会议总结异步任务、解析结果及最近一次 LLM 请求/响应 |
| `posture_analysis_results` | 姿势评分、次数、摘要和问题列表 |
| `transcription_sessions` | 实时转写会话及其 ASR 配置版本快照 |
| `transcription_audio_chunks` | 已接收音频分片的序号和大小账本 |
| `asr_jobs` | ASR 会话任务状态与重试计数 |
| `ai_job_attempts` | ASR Provider 调用尝试记录 |
| `transcription_final_segments` | Vision 侧最终转写片段 |
| `transcription_outbox` | 向 Core 可靠投递转写与用量事件 |
| `meeting_summary_jobs` | 会议总结异步任务及其模型配置版本快照 |
| `asr_provider_configs` | 版本化 ASR Provider、Workspace、实时/文件模型和词汇表配置 |
| `meeting_summary_provider_configs` | 版本化总结 Provider、模型、Prompt 和输入输出边界 |
| `provider_credentials` | 百炼、DeepSeek API Key 明文和实时凭证版本 |

`analysis_jobs.status` 状态流转：

```text
pending -> processing -> succeeded
                      -> failed
```

Worker 领取任务时必须使用数据库事务或队列的原子消费能力，避免多个 Worker 重复处理同一个任务。

### Vision AI Provider 配置

模型选择和 Provider API Key 都属于 Vision 数据：

- PostgreSQL 保存 Provider、Workspace、模型名、Prompt 版本、模型输入输出限制，以及 API Key 明文。
- `configs/vision.yaml` 保存可选 endpoint、超时、连接数、WebSocket、gRPC 和 Worker 租约/退避参数。
- `asr_provider_configs` 与 `meeting_summary_provider_configs` 各自最多只能有一条 `active` 记录。
- 已被会话或任务引用的配置不可原地修改；切换时创建新版本并将旧版本置为 `retired`。

Vision 在每次创建转写会话或会议总结任务时读取当前 `active` 行，因此提交切换事务后对
新任务实时生效，无需重启。已有会话、失败重试和待投递任务继续使用其
`provider_config_id` 指向的历史版本。

执行 `make configure-vision-credentials`，在终端隐藏输入后由管理命令原子更新
`provider_credentials.api_key`，同时递增 `version`。Vision
在解析 ASR Provider 或总结 Provider 时读取当前凭证版本；版本变化后会为后续请求重建
Provider Client，无需重启，已经建立的 WebSocket 会话不受影响。

`provider_credentials.api_key` 是明文敏感信息，数据库账号必须使用最小权限，备份和
SQL 日志也必须按密钥材料保护；服务日志和 API 响应仍禁止输出该字段。

例如切换百炼实时模型：

```sql
BEGIN;
SELECT pg_advisory_xact_lock(hashtext('vision:asr-provider-config'));

UPDATE asr_provider_configs
SET status = 'retired', updated_at = now()
WHERE status = 'active';

INSERT INTO asr_provider_configs (
    id, version, status, provider, workspace_id,
    realtime_model, file_model, vocabulary_id,
    activated_at, created_at, updated_at
)
SELECT gen_random_uuid(), COALESCE(MAX(version), 0) + 1, 'active',
       'bailian_paraformer', '你的-workspace-id',
       '新的实时模型名', '新的文件模型名', '',
       now(), now(), now()
FROM asr_provider_configs;

COMMIT;
```

例如切换 DeepSeek 总结模型：

```sql
BEGIN;
SELECT pg_advisory_xact_lock(hashtext('vision:summary-provider-config'));

UPDATE meeting_summary_provider_configs
SET status = 'retired', updated_at = now()
WHERE status = 'active';

INSERT INTO meeting_summary_provider_configs (
    id, version, status, provider, model_name, prompt_version,
    max_input_chars_per_chunk, max_chunks, max_output_tokens,
    activated_at, created_at, updated_at
)
SELECT gen_random_uuid(), COALESCE(MAX(version), 0) + 1, 'active',
       'deepseek', '新的模型名', 'meeting-summary-v2',
       60000, 64, 8192, now(), now(), now()
FROM meeting_summary_provider_configs;

COMMIT;
```

切换前应先确认对应 API Key 对新模型有权限。若新配置不可用，只会影响引用该新版本的
任务；回滚方式是再创建一个版本，而不是重新激活或修改已退休的历史行。

## 配置

core-service：

```bash
export CORE_DATABASE_DSN='postgres://tiehu:password@127.0.0.1:5432/tiehu_core?sslmode=disable'
```

vision-service：

```bash
export VISION_DATABASE_DSN='postgres://tiehu:password@127.0.0.1:5432/tiehu_vision?sslmode=disable'
```

连接池参数位于：

```text
configs/core.yaml
configs/vision.yaml
```

公共 GORM PostgreSQL 初始化代码位于：

```text
internal/platform/database/postgres.go
```

默认会议额度不是进程 YAML 配置。`meeting_quota_policies` 只允许 `id = 1`，启动迁移会
幂等写入初始策略；Core 启动时必须成功加载该行，并在每次会议授权或额度查询时读取当前
值。运维修改策略时应在同一事务中更新业务字段、递增 `version` 并刷新 `updated_at`。

## 创建数据库并启动

本机安装 PostgreSQL 和 `psql` 后：

```bash
createdb tiehu_core
createdb tiehu_vision

export CORE_DATABASE_DSN='postgres://tiehu:password@127.0.0.1:5432/tiehu_core?sslmode=disable'
export VISION_DATABASE_DSN='postgres://tiehu:password@127.0.0.1:5432/tiehu_vision?sslmode=disable'

make run-core
make run-vision
```

数据库用户必须拥有自己数据库的建表和改表权限。空库由 `AutoMigrateSchema` 自动创建
Core 22 张表或 Vision 15 张表，并补齐非敏感启动配置。API Key 不会被自动生成。

## 后续修改表结构

不要直接修改已经在共享环境执行过的 `000001_init.up.sql`，而是新增 migration：

```text
000002_add_user_phone.up.sql
000002_add_user_phone.down.sql
000003_add_posture_issue_frames.up.sql
000003_add_posture_issue_frames.down.sql
```

一次 migration 应该：

- 只处理一个清晰的结构变更。
- 同时提供 up 和 down。
- 明确索引、唯一约束、外键和删除行为。
- 在测试数据库先验证。
- 避免在大表上直接执行长时间锁表操作。

普通新增表和兼容字段由显式 GORM Model 列表在启动时同步。破坏性变更、重命名、
数据回填、Check Constraint、触发器和部分索引必须继续新增成对 SQL migration；不得
修改已在共享环境执行的历史 migration。
