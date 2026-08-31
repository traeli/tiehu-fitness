# 搭建和启动

## 环境

- Go 1.25+
- GNU Make
- Buf CLI（`make init` 安装）
- Docker Desktop（推荐的本地 PostgreSQL/Redis 启动方式）
- PostgreSQL 16+ 和 `psql`（仅手动管理数据库时需要）

## 本地 PostgreSQL 和 Redis

仓库根目录的 `docker-compose.yml` 提供 PostgreSQL 16 和 Redis 7。core 与 vision
使用独立的数据库账号、数据库、Redis ACL 账号和键前缀，不能跨服务访问数据。

本地账号、密码、端口和 DSN 统一放在 `configs/docker-compose.env`。该文件已被
Git 忽略；可提交的字段示例位于 `configs/docker-compose.env.example`。首次使用时：

```bash
cp configs/docker-compose.env.example configs/docker-compose.env
# 修改 configs/docker-compose.env 中全部 change_me 值
make infra-up
make infra-status
```

当前工作区已经有本地 `configs/docker-compose.env` 时，不要再次复制覆盖。core 的
启动命令会自动加载该文件；vision 的 PostgreSQL DSN、Redis 账号和本地 WebSocket
地址直接配置在 `configs/vision.yaml`。首次启动 Vision 前，用隐藏输入配置两个
Provider API Key：

```bash
make configure-vision-credentials
```

该命令会将百炼、DeepSeek API Key 明文写入 PostgreSQL 的
`provider_credentials.api_key`，但不会写入环境变量、YAML、命令参数或日志。
数据库及备份需要按敏感密钥材料保护。以后轮换 API Key 仍执行同一个命令，Vision
会在后续请求中读取新的凭证版本，无需重启。

服务统一使用下面两个启动命令：

```bash
make run-core
make run-vision
```

Core 和 Vision 每次启动都会在 data 层执行带 60 秒超时和 PostgreSQL Advisory Lock
的 `AutoMigrate`。空库会自动创建各服务拥有的全部 GORM 表；重复启动只同步缺失结构，
不会清空已有数据。Core 会幂等补齐默认会议额度，Vision 会幂等补齐默认 ASR/LLM
模型配置，但 Provider API Key 仍必须通过上面的隐藏输入命令配置。

Docker 数据卷首次创建时仍会执行版本化 SQL migration，用于 GORM 无法安全表达的
Check Constraint、部分索引、触发器和数据回填。常用管理命令：

```bash
make infra-down   # 停止容器，保留数据
make infra-up     # 再次启动
make infra-reset  # 删除本项目的本地数据卷并重新初始化（会丢失本地数据）
```

默认只监听 `127.0.0.1`。如果默认端口被占用，可修改 `POSTGRES_PORT` 或
`REDIS_PORT`，并同步修改同一文件中的 DSN 或 Redis 地址。真实密钥和生产密码不得
写入 YAML、示例文件或提交到 Git。

## 生成与验证

```bash
make init
make all
make lint
make test
make build
```

生成的程序：

```text
bin/core
bin/vision
```

实际只生成 `bin/core` 和 `bin/vision` 两个程序。

## 使用已有 PostgreSQL

```bash
createdb tiehu_core
export CORE_DATABASE_DSN='postgres://你的用户:你的密码@127.0.0.1:5432/tiehu_core?sslmode=disable'
make run-core
```

数据库账号必须拥有目标数据库的建表和改表权限。Core 启动时会自动创建缺失表并写入
默认会议额度策略。版本化 SQL migration 仍用于复杂约束、重命名、回填和生产发布审计。

## Web 注册和登录

首次测试先注册一个账号：

```bash
curl -X POST http://127.0.0.1:8000/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"web.user@example.com","password":"password-123","nickname":"Web 测试用户","device_id":"browser-1"}'
```

之后使用普通登录接口：

```bash
curl -X POST http://127.0.0.1:8000/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"web.user@example.com","password":"password-123","device_id":"browser-1"}'
```

邮箱会忽略首尾空格和大小写，国际化域名会规范化为 Punycode；不接受显示名称、连续点、单标签域名或不完整地址。密码长度为 8 到 72 字节。示例密码只用于本地联调，生产环境必须使用强密码并启用 HTTPS。

响应包含 `access_token`、`refresh_token`、`expires_in_seconds`、`user` 和 `onboarding_required`。密码错误和邮箱不存在统一返回 `LOGIN_INVALID`。

## 配置微信登录（保留）

```bash
export WECHAT_APP_ID='你的小程序 AppID'
export WECHAT_APP_SECRET='你的小程序 AppSecret'
```

小程序调用 `wx.login()` 后，将一次性 `code` 发送到：

```http
POST /v1/auth/wechat/login
Content-Type: application/json

{"code":"wx.login 返回的 code","device_id":"device-1"}
```

## 启动服务

默认会议额度现在只保存在 `meeting_quota_policies` 的 `id = 1` 行。Core 启动时会
强制加载并校验；创建会议和查询额度时会读取当前行，所以提交修改后从下一次请求
实时生效，无需重启。已经创建的会议继续使用创建时的 reservation grant。

例如将月度额度调整为 4 小时：

```sql
UPDATE meeting_quota_policies
SET monthly_audio_seconds = 14400,
    version = version + 1,
    updated_at = NOW()
WHERE id = 1;
```

```bash
make run-core
make run-vision
```

`make run-core` 会自动加载本地 `configs/docker-compose.env`。只有使用微信登录时才需要
额外配置 `WECHAT_APP_ID` 和 `WECHAT_APP_SECRET`。

默认端口：

- core：HTTP `8000`，gRPC `9000`
- vision：HTTP `8100`，gRPC `9100`

### 实时 WebSocket 与 Provider 启动检查

vision 还会通过 `core_grpc_client`（本地默认 `127.0.0.1:9000`）消费 PostgreSQL
outbox：先批量同步最终片段，再上报累计音频并发送完成通知。core 成功处理后会将
会议置为 `completed/succeeded`，结算实际秒数并释放其余预占。若 core 暂时不可用，
worker 会按 `transcription_outbox_worker` 的租约和指数退避配置重试。

Vision 启动时会读取并解密数据库中的百炼、DeepSeek 凭证，同时校验 active
Workspace/模型和安全 endpoint；凭证缺失、密钥文件错误或解密失败都会以退出码 1
结束。`asr.startup_probe.enabled` 开启后，Vision 会在监听业务端口前把内置的 3.63 秒
PCM 固定语音按实时速度发送给百炼；只有完成 `task-started`、音频发送、最终文本和
`task-finished` 全链路后才启动 HTTP/gRPC。

百炼 Workspace、实时/文件模型、词汇表，以及 DeepSeek 模型、Prompt 版本和分块/输出限制
不再写入 `vision.yaml`，由 Vision 数据库的版本化配置表管理。新会议和新总结任务每次
读取 active 版本，配置切换无需重启；历史任务继续使用创建时保存的配置版本。具体切换
SQL 见 `docs/database.md`。

本地验证 core-service 中的内容接口：

```bash
curl http://127.0.0.1:8000/v1/equipment/cable-machine
```

正式环境通过 Nginx、Kubernetes Ingress 或云 API 网关统一域名和 TLS，不额外创建业务 gateway-service。
