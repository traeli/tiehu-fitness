# 铁虎健身工程规范

本文件适用于整个仓库，对应英文规范为 `AGENTS.md`。修改任意一份时，必须同步
维护另一份，保证章节和约束语义一致。如果翻译意外产生歧义，以英文
`AGENTS.md` 作为工具读取的基准规则。

## 1. 项目基线

- 本项目使用 Go 1.25+、Kratos v3、Protobuf、gRPC/HTTP、PostgreSQL 和
  GORM。
- 保持根目录单一 `go.mod`。未经明确架构决策，不得引入 `go.work`、嵌套
  Go Module 或第三个可部署服务。
- 当前只有两个可部署服务：
  - `core-service`：身份、会话、用户档案、健身内容、训练计划、训练记录和
    用户参与功能。
  - `vision-service`：媒体处理、器材识别、姿势分析、AI 任务、模型和进程内
    有界 Worker Pool。
- core 内部的 user、content、training 是业务模块，不是独立微服务。同一进程
  内的模块之间不得为了形式而增加网络调用。
- core 与 vision 各自拥有独立数据，任何服务不得查询或修改另一个服务的数据
  库。
- 工作区存在用户未提交修改时，必须保留无关改动。未经用户明确要求，不得执行
  破坏性的 Git 或文件系统操作。

已有代码不能成为违反新规范的理由。如果本次修改触及遗留违规区域，并且迁移
安全、属于本次范围，应当一起修正；否则记录技术债务，但禁止把违规写法复制到
新代码。

## 2. 强制架构与依赖方向

遵循当前 Kratos 分层：

```text
api/*.proto
    |
server -> service -> biz <- data -> data/model
                     ^
                  worker
```

各目录职责：

- `api/<domain>/v1`：带版本的传输契约和 HTTP 注解。
- `app/<service>/cmd/<service>`：只作为依赖组装入口；加载配置、构造依赖、
  启动应用并返回启动错误。
- `internal/server`：HTTP/gRPC 配置、中间件和路由注册。
- `internal/service`：只负责协议适配和 Proto/领域对象转换。
- `internal/biz`：领域类型、不变量、Usecase、Repo 接口和状态流转。
- `internal/data`：Repo 适配器、事务、持久化映射、缓存和第三方客户端。
- `internal/data/model`：只放 GORM 持久化模型。
- `internal/worker`：vision 后台执行入口，必须调用 biz Usecase，不得绕过
  biz 直接修改 Repo。
- `internal/platform`：两个服务真正共享且与业务无关的基础设施。不得放入
  core 或 vision 专属业务规则。

禁止的依赖示例：

- `service` 导入 GORM、SQL Driver 或 `data/model`。
- `biz` 导入 `service`、`data`、GORM、Kratos Transport 或生成的 HTTP
  Handler。
- service 或 Proto 响应直接返回 `data/model`。
- Worker 绕过 Usecase 直接更新数据库。
- 一个服务导入另一个服务的 `internal` 包。

所有依赖必须通过构造函数显式注入。禁止包级可变状态和隐藏的单例客户端。

## 3. API First 与生成代码

- API 必须先定义或修改 Proto，再生成代码，然后实现 service、biz 和 data。
- 禁止手工修改 `*.pb.go`、`*_grpc.pb.go`、`*_http.pb.go`、
  `internal/conf/*.pb.go` 和 `openapi.yaml`。
- OpenAPI 接口说明写在 Proto 注释中，生成器全局信息写在 `buf.gen.yaml`。
- 包名使用 `<domain>.v1`，HTTP 路径保持稳定并以资源为中心。
- RPC 名称以动词开头，Proto 字段使用 `snake_case`。
- 必填输入使用 `google.api.field_behavior = REQUIRED`。
- 每个 Service、RPC、请求/响应 Message 和不直观字段都要有简短有效的注释。
- 已发布的 Proto 字段编号不得修改或复用；删除字段时必须 `reserved` 原编号和
  名称。
- 路由、字段、枚举或响应的破坏性变更，必须提供明确兼容和发布方案。
- 时间使用 `google.protobuf.Timestamp`，时长使用 `Duration`，不得自创
  字符串时间格式。
- 列表接口必须有有界分页；创建/提交接口必须定义重试和幂等行为。
- 不得因为序列化方便就暴露数据库模型、密钥、第三方原始响应或无边界 JSON。

Proto 修改后执行：

```bash
make api
buf lint
git diff -- api openapi.yaml
```

## 4. 有限取值必须使用领域类型或枚举

任何取值集合封闭的字段，禁止使用无类型字符串表示或判断，包括
`status`、`state`、`type`、`kind`、`role`、`level`、
`source` 及类似分类字段。

强制要求：

- 在 `biz` 定义唯一可信的领域类型和常量。
- 对外字段在 Proto 中定义枚举。
- 所有不可信输入边界必须校验。
- 使用带错误返回的 Parse/Mapping 函数转换。
- 数据库约束必须与领域取值保持一致。
- 生命周期变化使用允许的状态流转，不得任意赋值。
- Proto 枚举必须包含 `UNSPECIFIED = 0`，已发布编号不得复用。

错误示范：

```go
if plan.Status == "active" {
    plan.Status = "completed"
}
```

推荐写法：

```go
type TrainingPlanStatus string

const (
    TrainingPlanStatusDraft     TrainingPlanStatus = "draft"
    TrainingPlanStatusActive    TrainingPlanStatus = "active"
    TrainingPlanStatusCompleted TrainingPlanStatus = "completed"
    TrainingPlanStatusCancelled TrainingPlanStatus = "cancelled"
)

func ParseTrainingPlanStatus(raw string) (TrainingPlanStatus, error) {
    switch raw {
    case string(TrainingPlanStatusDraft):
        return TrainingPlanStatusDraft, nil
    case string(TrainingPlanStatusActive):
        return TrainingPlanStatusActive, nil
    case string(TrainingPlanStatusCompleted):
        return TrainingPlanStatusCompleted, nil
    case string(TrainingPlanStatusCancelled):
        return TrainingPlanStatusCancelled, nil
    default:
        return "", fmt.Errorf("unknown training plan status %q", raw)
    }
}

func (s TrainingPlanStatus) CanTransitionTo(next TrainingPlanStatus) bool {
    switch s {
    case TrainingPlanStatusDraft:
        return next == TrainingPlanStatusActive ||
            next == TrainingPlanStatusCancelled
    case TrainingPlanStatusActive:
        return next == TrainingPlanStatusCompleted ||
            next == TrainingPlanStatusCancelled
    default:
        return false
    }
}
```

禁止在 service 和 data 到处复制枚举转换 switch。每个领域类型只保留一处传输
映射和一处持久化解析。

## 5. 类型安全：禁止未检查的断言和缩窄转换

禁止可能产生 panic、静默溢出、截断或接受非法领域值的转换。

禁止：

```go
name := value.(string)       // 动态类型不同时 panic
days := int16(req.Days)      // 可能静默溢出
status := Status(pb.Status)  // 假设两个无关枚举的编号相同
item := items[0]             // 空切片时 panic
result := *maybeResult       // nil 指针时 panic
```

正确写法：

```go
name, ok := value.(string)
if !ok {
    return errors.BadRequest("NAME_TYPE_INVALID", "name must be a string")
}

const (
    minInt16 = -1 << 15
    maxInt16 = 1<<15 - 1
)
if req.Days < minInt16 || req.Days > maxInt16 {
    return errors.BadRequest("DAYS_OUT_OF_RANGE", "days is out of range")
}
days := int16(req.Days)

if len(items) == 0 {
    return errors.NotFound("ITEM_NOT_FOUND", "item not found")
}
item := items[0]
```

其他约束：

- 类型断言和可能缺失的 map 查询使用 comma-ok。
- 访问指针、切片或可选嵌套 Proto Message 前必须检查。
- 外部字符串使用 `strconv`、`time.Parse`、UUID Parser 或领域 Parser，
  并处理错误。
- Proto 枚举到领域枚举使用完整 `switch`，不得依赖数字刚好相等。
- 数值缩窄前必须检查上下界。
- 不得用 `unsafe` 或反射逃避正常类型建模。
- 泛型 Helper 必须保留类型信息，不得用 `any` 替代明确的领域 API。

## 6. Panic 与恢复边界

- 请求链路、biz、Repo、Mapper 和 Worker 对可预期失败返回 error，不得 panic。
- 组合入口优先使用 `run() error`，而不是在 `main` 中 `panic(err)`。
- 只有经过证明不可能发生的不变量破坏或不可恢复的编程错误才允许 panic，并要
  注释原因。
- 参数校验、NotFound、第三方失败、数据库失败和流程控制不得使用 panic。
- 不得对运行时输入或用户输入使用 `Must*` Helper。
- Kratos HTTP/gRPC Server 必须保留 recovery middleware 作为最终防线。
- 每个手工启动的 goroutine 和 Worker 执行边界都必须有自己的 panic recovery，
  因为 `recover` 只能捕获同一 goroutine 的 panic。
- Recover 必须记录结构化上下文和堆栈，把失败转换为稳定内部错误或失败任务状态，
  严禁静默吞掉 panic。
- 加入 `recover` 不能成为危险索引、断言或 nil 解引用的借口，应先消除风险。

错误示范：

```go
func loadProfile(raw any) Profile {
    return raw.(Profile)
}
```

可接受的 Worker 最终防线：

```go
defer func() {
    if recovered := recover(); recovered != nil {
        logger.Error("worker panic",
            "job_id", jobID,
            "panic", recovered,
            "stack", string(debug.Stack()),
        )
        if err := markJobFailed(ctx, jobID); err != nil {
            logger.Error("mark job failed", "job_id", jobID, "error", err)
        }
    }
}()
```

## 7. 错误处理

- 应用边界返回稳定的 Kratos Error，Reason 使用
  `EXERCISE_NOT_FOUND` 这样的英文大写标识。
- 禁止向客户端返回 SQL、DSN、第三方密钥、堆栈或原始内部错误。
- 使用 `%w` 或 `WithCause` 保留内部 Cause，供日志和
  `errors.Is/As` 使用。
- 禁止通过错误文本比较错误。
- 在拥有语义的边界转换错误：
  - data：Driver/Provider 错误转换成 Repo 或领域可识别错误；
  - biz：不变量和冲突错误；
  - service：只负责协议映射。
- 每个错误都必须处理。确实允许忽略的 Best Effort 错误，需要注释；对运维有
  影响时还要记录日志或指标。
- 保留 `context.Canceled` 和 `context.DeadlineExceeded`，不得改写成普通
  Internal Error。
- 多写入不变量必须使用事务，并返回事务回调错误以触发回滚。

错误示范：

```go
profile, _ := repo.GetProfile(ctx, userID)
if err.Error() == "record not found" { /* ... */ }
return errors.InternalServer("DB_ERROR", err.Error())
```

## 8. 数据库与 GORM

- 每个服务连接 PostgreSQL 后、启动 Repository、Worker 或传输服务器前，必须在
  data 层使用完整且显式的本服务 GORM Model 列表执行 `AutoMigrate`。自动迁移必须
  有 Context 超时、能在多实例并发启动时串行执行，并将失败作为启动错误返回。
- 对破坏性修改、表或列重命名、数据回填、数据库函数、触发器、部分索引以及 GORM
  无法安全表达的约束，仍需保留成对的 SQL Migration。不得以 `AutoMigrate` 为由
  削弱数据库不变量或静默丢弃数据。
- 已在共享环境执行的 Migration 不得修改，应新增成对的 `up` 和 `down`。
- 每个查询都使用 `WithContext(ctx)`。
- 明确处理 `gorm.ErrRecordNotFound`、已转换的约束错误和
  `RowsAffected`。
- 必须参数化查询，禁止拼接不可信 SQL。
- 事务应保持短小，事务中不得执行缓慢网络调用或 AI 推理。
- 按真实查询、Join、排序和唯一性场景建立索引。
- 关键不变量既要在 biz 校验，也要有数据库约束。
- 有限取值列必须有领域类型和数据库 Check Constraint；未知存量值必须解析失败，
  不得继续流入业务。
- 避免 N+1 查询和无边界 `Find`，应批量、明确 Preload 或分页。
- 只存 Token Hash，不存可直接使用的明文 Token。微信 `session_key` 等敏感
  信息必须加密，否则不持久化。
- core 和 vision 的数据库及 Migration 保持独立，禁止跨服务外键。
- GORM Model 是持久化细节，必须在 data 转换为 biz Entity。

## 9. 并发、Worker 与状态机

- 每个 goroutine 必须有 Owner、取消路径和有界生命周期。
- Context 必须传递，网络、存储和模型调用必须设置明确超时。
- 使用有界 Worker Pool 和背压，禁止对无边界输入逐个创建 goroutine。
- 共享内存必须有明确同步，或使用单 Owner Channel 语义；并发代码使用
  `go test -race` 验证。
- 可能重试的 Job 和 Command 必须幂等。
- 领取任务并执行 `pending -> processing` 必须依赖队列保证或数据库事务/锁
  原子完成。
- 允许的任务状态流转必须在领域状态机中编码，禁止 service、data、worker 各自
  写状态字符串。
- Worker 失败必须记录脱敏错误和重试信息，不能丢失原任务。

## 10. 复用、重复与包设计

- 禁止在多个 Handler 复制业务规则、枚举映射、校验、SQL 片段、DTO 映射和错误
  转换。
- 只能在正确边界复用：
  - 领域行为留在 biz；
  - 协议映射留在 service；
  - 持久化映射和查询 Helper 留在 data；
  - 真正与业务无关的共享基础设施才可进入 `internal/platform`。
- 不同领域中看起来相似的逻辑，不能仅为减少行数强行合并，必须保留领域归属和
  术语。
- 优先使用小而内聚的函数和包。函数存在多个变化原因时才拆分，不使用机械行数
  作为唯一判断。
- 禁止 God Struct、万能 `utils` 包、循环依赖、隐藏副作用、死代码、投机抽象，
  以及返回成功零值的占位实现。
- TODO 必须说明缺失内容并关联 Issue 或具体后续任务，完成路径中禁止留下
  `TODO: implement`。
- 注释解释意图、不变量和非显然权衡，不复述语法。

错误复用：

```go
// utils/helpers.go
func Process(data any, kind string) any { /* 所有领域都塞进来 */ }
```

合理复用：

- vision biz 中只有一个 `PostureAnalyzer` 策略接口；
- 分页语义确实一致时复用一个分页值对象；
- 用户状态和分析状态即便在 SQL 中都存字符串，也保持独立类型。

## 11. 设计模式决策规则

任何结构性功能都要先识别变化点、生命周期、所有权和失败边界，再选择模式。只有
模式能让这些因素更清晰时才使用。

本项目适用方向：

- Repository + 依赖倒置：持久化和第三方 Provider Port。
- Adapter：微信、对象存储、模型运行时和外部 API。
- Strategy：可替换训练计划算法或 AI 模型 Provider。
- State Pattern 或显式流转表：训练和分析任务生命周期。
- Factory/Constructor：经过校验的客户端构造和依赖组装。
- Command/Job：vision 异步任务。
- Middleware/Decorator：鉴权、Tracing、日志、指标、Recovery 和幂等。
- Outbox：数据库修改与事件发布必须一致时使用。

禁止“为了模式而模式”：

- 不创建只有无意义 `Manager/Processor/Handler` 方法的接口；
- 不创建只包装 Struct Literal、没有校验或选择逻辑的 Factory；
- 不使用全局 Service Locator；
- 不因为设计模式名称高级就增加抽象。

新增跨包抽象或设计模式时，必须用简短代码或设计注释说明解决的问题，以及为什么
简单局部函数不足以解决。

## 12. 安全与隐私

- 禁止提交密钥、真实 Token、带密码 DSN、私有媒体地址或生产用户数据。
- 受保护接口必须鉴权并校验资源所有权，不能只相信路径中的 `user_id`。
- vision 下载用户媒体前必须校验 URI Scheme、Host 策略、MIME、大小、时长和
  所有权，防止 SSRF 和解压炸弹。
- 限制请求体大小和集合长度。
- 禁止记录 Access Token、Refresh Token、微信 Code、Session Key、原始视频或
  敏感档案说明。
- 数据库和对象存储凭据遵循最小权限。
- 第三方和模型错误返回客户端前必须脱敏。

## 13. 可观测性

- 使用结构化日志，包含 Service、Operation、Request/Job ID 和安全领域标识。
- 只在拥有足够上下文的边界记录一次错误，避免每层重复打印同一个错误。
- 长时间 vision 任务需要时长、排队延迟、结果、重试次数和模型版本指标。
- HTTP、gRPC 和 Job 之间传递 Trace 与关联信息。
- 不得为了调试便利牺牲密钥和隐私安全。

## 14. 测试要求

- biz 测试在测试边界定义 Fake，不依赖生产 data 实现。
- SQL、约束、事务和锁相关 data 行为必须使用 PostgreSQL 集成测试，不得假设
  SQLite 语义相同。
- service 测试覆盖 Proto/Domain 映射，以及 Path 参数优先于 Body 的行为。
- 校验、枚举解析、状态流转和错误映射使用表驱动测试。
- 每个 Bug 修复都要有回归测试。
- 按需覆盖重复请求、重试、NotFound、非法枚举、数值边界、nil/空输入、事务回滚
  和 Context 取消。
- 并发代码需要 Race 测试和确定性的取消测试。
- 测试断言行为和稳定 Error Reason，不依赖私有实现细节。

普通修改的最低验证：

```bash
gofmt -w <changed-go-files>
buf lint
go test ./...
go vet ./...
go build ./app/core/cmd/core
go build ./app/vision/cmd/vision
git diff --check
```

并发修改执行 `go test -race ./...`；持久化修改应在一次性 PostgreSQL 数据库中
实际执行 Migration。

## 15. 依赖与仓库卫生

- 优先标准库和现有依赖。新增依赖必须有明确收益，并检查维护情况、许可证和最小
  使用范围。
- Buf 模板中的代码生成器固定版本，生成结果必须可复现。
- 禁止引入 Vendor 生成代码、第二套框架或重复基础设施封装。
- 只格式化触及的文件，禁止无关的全仓库重写。
- 仓库中不得遗留构建二进制、临时媒体、本地数据库、Coverage 产物或密钥。
- 不得为了让检查通过而削弱 Lint、测试、约束或错误处理。

## 16. 强制变更流程

编码前：

1. 判断功能属于 core 还是 vision。
2. 识别领域所有权、不变量、状态、信任边界和失败模式。
3. 检查是否改变 API 或数据库契约，并评估兼容性。
4. 选择最简单合适的模式，并记录不明显的决策原因。

实现顺序：

1. Proto 契约和注释。
2. 生成代码并审查 OpenAPI。
3. biz 领域类型、枚举/状态流转、Port 和 Usecase。
4. data Adapter、Model 映射、事务和 Migration。
5. service 映射以及 server/worker 组装。
6. 测试、文档、格式化和完整验证。

完成定义：

- 实现遵循当前服务和分层边界。
- 有限取值已类型化并校验，没有新增裸字符串 status/type 判断。
- 不存在未检查断言、缩窄转换、nil 解引用、越界访问或可避免的 panic 风险。
- 客户端错误稳定，同时保留安全的内部 Cause。
- 重复逻辑已消除，但没有跨越业务边界强行复用。
- 按需考虑了安全、并发、幂等、可观测性和回滚。
- 生成文件可复现且未手工修改。
- 测试和验证通过，剩余限制已明确说明。

## 17. 垃圾代码拒绝清单

以下写法即使能够编译，也必须在 Code Review 中拒绝：

| 禁止模式 | 拒绝原因 | 必须替换为 |
|---|---|---|
| Service 方法直接查询 GORM | 破坏依赖方向，让业务行为依赖传输层 | 新增 biz Usecase 和 Repo Port |
| 多个 Handler 复制相同校验或映射 | 修复会漂移，最终一定漏掉某条链路 | 提取归属正确、带类型的边界 Helper |
| 占位代码返回 `nil, nil`、空对象或虚假成功 | 隐藏未完成功能并误导调用方 | 返回稳定的未实现/内部错误，或暂不注册路由 |
| 无 Owner、取消、边界和 Recovery 的 `go func()` | 泄漏资源，并可能击穿或拖垮进程 | 有 Owner 的 errgroup 或有界 Worker Pool |
| 用 `context.Background()` 替换请求 Context | 丢失取消、Deadline 和 Trace | 传递调用方 Context，只派生有界子 Context |
| 忽略错误或只打印错误 | 丢失回滚和运维失败信号 | 返回、转换或有意识地记录错误 |
| 一个通用 Manager 按字符串 Kind 处理无关领域 | 形成 God Abstraction，破坏业务边界 | 使用带类型输入的独立领域 Strategy/Adapter |
| 全局可变 Map、Client 或 Config | 引入竞态、隐藏耦合并污染测试 | 构造函数注入和明确 Owner |
| Recover 不记录信息并继续返回成功 | 状态可能损坏，却伪装成功 | 记录堆栈和上下文，并返回失败或标记任务失败 |
| 对每一行单独查询数据库 | 产生 N+1 延迟和负载 | 批量查询、Join 或明确 Preload |

错误的分层穿透和虚假成功：

```go
func (s *UserService) GetProfile(ctx context.Context, req *v1.Request) (*v1.Reply, error) {
    var row model.FitnessProfile
    _ = s.db.Where("user_id = ?", req.UserId).First(&row).Error
    return &v1.Reply{}, nil
}
```

错误的无界任务和吞 Panic：

```go
for _, video := range videos {
    go func() {
        defer func() { _ = recover() }()
        analyze(context.Background(), video)
    }()
}
```

如果一个变更为了跑通 Happy Path 而削弱类型安全、分层边界、失败处理、取消或可观测
性，Code Review 必须拒绝。代码行数更少不代表质量更好，清晰所有权和正确行为才是
目标。
