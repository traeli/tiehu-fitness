# 双服务架构

## 架构结论

当前阶段只保留两个部署服务：

| 服务 | 部署资源 | 内部模块 | 拥有的数据 |
|---|---|---|---|
| core-service | 普通 CPU 服务器 | identity、session、profile、content、training、engagement | Web 密码凭证、微信身份、用户、会话、健身档案、器材动作、教学内容、计划、训练记录和打卡 |
| vision-service | 高性能 CPU/GPU 服务器 | media、equipment-recognition、posture-analysis、model、worker | 媒体资产、分析任务、模型版本、识别结果和姿态结果 |

core-service 是模块化业务服务。用户、内容和训练保持独立 API 与代码模块，但共享一个进程和一套 Kratos HTTP/gRPC Server。

vision-service 同一进程内运行 HTTP、gRPC 和异步 Worker Pool，不再创建独立的 vision-worker 程序。

## 部署关系

```text
Web / WeChat Mini Program
        |
   Ingress / API Gateway（基础设施）
        |
        +----> core-service
        |        ├── 用户与登录
        |        ├── 器材与动作内容
        |        └── 计划、记录与打卡
        |
        +----> vision-service（高性能 CPU/GPU）
                 ├── HTTP/gRPC
                 ├── 图片与视频处理
                 ├── AI 模型运行时
                 └── 内部 Worker Pool
```

构建只生成：

```text
bin/core
bin/vision
```

## core-service

core-service 注册多个业务 Proto Service：

```text
user.v1.UserService
content.v1.ContentService
```

多个 Proto Service 不等于多个微服务。它们可以保持清晰的接口边界，同时部署在同一个 core 进程。

代码结构：

```text
app/core/
├── cmd/core/
└── internal/
    ├── service/
    │   ├── user.go
    │   └── content.go
    ├── biz/
    │   ├── user.go
    │   └── content.go
    ├── data/
    │   ├── user.go
    │   └── content.go
    └── server/
        └── server.go
```

### Web 注册与登录

```text
Web 注册
    -> core-service /v1/auth/register
    -> 校验并规范化邮箱，生成 bcrypt 密码哈希
    -> 事务内创建 user + password_credential + 空健身档案
    -> 签发业务 access_token / refresh_token

Web 登录
    -> core-service /v1/auth/login
    -> 查询规范化邮箱并校验 bcrypt 密码
    -> 签发业务 access_token / refresh_token
```

### 微信首次登录

```text
小程序 wx.login
    -> core-service /v1/auth/wechat/login
    -> 微信 code2Session
    -> 按 (app_id, open_id) 查询身份
    -> 不存在则事务内创建 user + wechat_identity + 空健身档案
    -> 签发业务 access_token / refresh_token
    -> 返回 is_new_user 与 onboarding_required
```

微信渠道不提供独立注册接口。OpenID 只作为微信身份映射，业务数据统一使用内部 `user_id`。微信 `session_key` 不返回小程序；正式数据层应加密或短期保存。

## vision-service

vision-service 负责计算密集型任务：

- 图片预处理和器材识别。
- 视频抽帧、人体关键点检测和动作分段。
- 健身姿势评分、错误时间点和纠正建议。
- AI 模型加载、版本记录和结果追溯。
- 异步任务执行、并发限制、失败重试。

代码结构：

```text
app/vision/
├── cmd/vision/
└── internal/
    ├── service/
    ├── biz/
    ├── data/
    ├── server/
    └── worker/
```

典型异步流程：

```text
提交图片/视频
    -> vision-service 创建 pending 任务
    -> 返回 job_id
    -> 内部 Worker Pool 消费任务
    -> processing
    -> succeeded / failed
    -> 客户端按 job_id 查询结果
```

当前 Worker 只是生命周期骨架，后续接入持久化任务表或消息队列。耗时的视频分析不能直接占用 HTTP 请求直到处理完成。

## 服务之间的边界

- core-service 不执行图片识别、视频抽帧或模型推理。
- vision-service 不维护用户档案、训练计划或教学内容。
- 两个服务不直接读写对方拥有的数据库表。
- 同步调用使用 gRPC；耗时任务使用 `job_id` 异步查询。
- 登录 Token 由 core-service 签发，vision-service 通过共享公钥或统一鉴权中间件验证，不为每次请求同步查询 core 数据库。

初期可以共用一套 PostgreSQL 实例以降低运维成本，但应使用独立数据库或 Schema，并保持代码访问边界。

## Kratos 分层

两个服务内部都遵守相同分层：

- `service`：Proto DTO 转换和协议适配。
- `biz`：领域对象、业务用例和 Repo 接口。
- `data`：数据库、缓存、微信、对象存储、模型和消息队列实现。
- `server`：HTTP/gRPC 注册与中间件。
- `worker`：仅 vision-service 使用，负责后台任务生命周期。

依赖方向：

```text
server -> service -> biz <- data
                       ^
                    worker
```

worker 调用 biz 用例，不能绕过 biz 直接修改业务状态。

## 未来拆分触发条件

现在不提前增加微服务。只有出现明确收益时再拆：

- core-service 中某个模块出现独立团队和独立发布节奏。
- 内容读取流量远高于用户与训练业务，需要独立扩缩容。
- vision API 与 AI Worker 的资源需求明显不同，必须分别扩容。
- 视频转码影响姿势推理稳定性，需要独立媒体处理资源池。
- 数据合规要求某类数据独立存储和授权。

在这些条件出现前，保持两个服务。
