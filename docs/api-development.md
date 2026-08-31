# 新增 API 与 gRPC 开发流程

本文约定铁虎健身教练后端新增接口时的标准开发顺序。目标是让 Web/微信小程序使用 HTTP/JSON，让微服务之间使用 gRPC，同时复用同一套业务逻辑。

## 1. 核心原则

项目采用 API First：先定义 Proto，再实现业务。

```text
Proto 契约
   ├── HTTP/JSON（Web、微信小程序、运营后台）
   └── gRPC（服务间调用、内部工具）
             ↓
        service 协议适配
             ↓
        biz 业务逻辑
             ↓
        data 数据访问
```

必须遵守以下规则：

- 一个 RPC 只实现一次业务逻辑，HTTP 和 gRPC 共用同一个 service 方法。
- `service` 负责 DTO 转换和调用 Usecase，不直接访问数据库。
- `biz` 负责业务规则、领域对象、Repo 接口，不依赖 `service` 和 `data`。
- `data` 实现 `biz` 定义的 Repo 接口，封装数据库、缓存和第三方接口。
- `server` 只负责 HTTP/gRPC Server 配置、中间件和接口注册。
- 不手工修改 `*.pb.go`、`*_grpc.pb.go`、`*_http.pb.go` 等生成文件。

当前项目使用单个 `go.mod`，服务代码位于 `app/<service>`。如果以后将目录调整为 `service/<service>`，下面的分层和开发顺序保持不变。

## 2. 第一步：判断接口属于哪个服务

新增接口前先确认业务归属，不能为了复用代码而跨服务堆放业务。

| 服务 | 业务职责 | 示例 |
|---|---|---|
| `core-service` | 登录会话、用户档案、器材动作内容、训练计划、训练记录和打卡 | Web/微信登录、查询器材、生成计划、完成训练 |
| `vision-service` | 图片/视频处理、器材识别、姿势分析、AI 任务和模型版本 | 提交识别任务、查询姿势分析结果 |

判断方法：

1. 普通业务和内容接口放入 core-service，计算密集型接口放入 vision-service。
2. core 内部的 user、content、training 是代码模块，不是独立微服务，不需要通过 gRPC 互相调用。
3. core 与 vision 需要同步通信时使用 gRPC，不直接访问对方数据库。
4. vision 的 Worker Pool 在 vision-service 进程内运行，不创建第三个程序。

## 3. 第二步：定义 Proto

Proto 文件放在：

```text
api/user/v1/user.proto
api/content/v1/content.proto
api/vision/v1/vision.proto
```

以 core-service 内容模块的“查询动作详情”为例：

```proto
syntax = "proto3";

package content.v1;

option go_package = "github.com/tiehu-ai/tiehu-fitness/api/content/v1;contentv1";

import "google/api/annotations.proto";

service ContentService {
  rpc GetExercise(GetExerciseRequest) returns (GetExerciseResponse) {
    option (google.api.http) = {
      get: "/v1/exercises/{exercise_code}"
    };
  }
}

message GetExerciseRequest {
  string exercise_code = 1;
}

message GetExerciseResponse {
  Exercise exercise = 1;
}
```

### 3.1 同时提供 HTTP 和 gRPC

带有 `google.api.http` 注解的 RPC 会同时生成：

- gRPC Server/Client。
- HTTP Server/Client。
- OpenAPI 描述。

小程序请求：

```http
GET /v1/exercises/cable-row
```

服务间调用：

```text
content.v1.ContentService/GetExercise
```

两种协议最终进入同一个 `GetExercise` Go 方法。

### 3.2 只提供 gRPC

仅供内部使用的 RPC 可以不写 HTTP 注解：

```proto
rpc RebuildExerciseIndex(RebuildExerciseIndexRequest)
    returns (RebuildExerciseIndexResponse);
```

### 3.3 Proto 设计约定

- 包名保持 `<domain>.v1`，例如 `content.v1`。
- RPC 使用动词开头，例如 `GetExercise`、`CreateTrainingPlan`。
- 字段使用 `snake_case`。
- 已发布字段编号不可修改或复用；删除字段时使用 `reserved`。
- 时间使用 `google.protobuf.Timestamp`，时长使用 `google.protobuf.Duration`。
- 列表接口应考虑分页字段，避免一次返回无限数据。
- 创建类接口需要考虑重复提交和幂等性。
- 不在 Proto 中暴露数据库表结构、内部密钥或第三方原始响应。

## 4. 第三步：生成代码

第一次开发先安装 Buf：

```bash
cd /Users/Zhuanz1/develop/src/tiehu-fitness
make init
```

修改 Proto 后执行：

```bash
make api
```

`make api` 会执行依赖更新和代码生成：

```bash
buf dep update
buf generate --template buf.gen.yaml
```

生成结果包括：

```text
api/<domain>/v1/*.pb.go
api/<domain>/v1/*_grpc.pb.go
api/<domain>/v1/*_http.pb.go
openapi.yaml
```

生成后检查 Git diff，确认没有意外删除或修改已有字段：

```bash
git diff -- api openapi.yaml
```

如果修改的是配置 Proto，再执行：

```bash
make config
```

## 5. 第四步：实现 biz 业务层

相关文件位于：

```text
app/<service>/internal/biz/
```

`biz` 中需要完成三件事：

1. 定义领域对象。
2. 定义 Repo 接口。
3. 实现 Usecase 和业务校验。

```go
type Exercise struct {
	Code          string
	Name          string
	EquipmentCode string
}

type ContentRepo interface {
	GetExercise(context.Context, string) (*Exercise, error)
}

type ContentUsecase struct {
	repo ContentRepo
}

func (uc *ContentUsecase) GetExercise(
	ctx context.Context,
	code string,
) (*Exercise, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.BadRequest(
			"EXERCISE_CODE_REQUIRED",
			"exercise_code is required",
		)
	}
	return uc.repo.GetExercise(ctx, code)
}
```

错误码使用稳定的大写英文标识，例如：

- `EXERCISE_CODE_REQUIRED`
- `EXERCISE_NOT_FOUND`
- `TRAINING_PLAN_CONFLICT`
- `VISION_TASK_NOT_READY`

不要把数据库错误文本直接返回给客户端。

## 6. 第五步：实现 data 数据层

相关文件位于：

```text
app/<service>/internal/data/
```

`data` 实现 `biz` 中的 Repo 接口：

```go
type contentRepo struct {
	// db、cache 或第三方 client
}

func NewContentRepo() biz.ContentRepo {
	return &contentRepo{}
}

func (r *contentRepo) GetExercise(
	ctx context.Context,
	code string,
) (*biz.Exercise, error) {
	// 查询数据库并转换为 biz.Exercise。
	// 查询不到时返回 errors.NotFound。
	return nil, nil
}
```

这里负责：

- SQL、事务和数据映射。
- Redis 缓存。
- 对象存储。
- 微信、AI 模型等第三方客户端。
- 将底层错误转换为可识别的业务错误。

## 7. 第六步：实现 service 协议适配层

相关文件位于：

```text
app/<service>/internal/service/
```

`service` 实现 Proto 生成的 Server 接口，并在 Proto DTO 与 biz 领域对象之间转换：

```go
type ContentService struct {
	contentv1.UnimplementedContentServiceServer
	uc *biz.ContentUsecase
}

func (s *ContentService) GetExercise(
	ctx context.Context,
	req *contentv1.GetExerciseRequest,
) (*contentv1.GetExerciseResponse, error) {
	item, err := s.uc.GetExercise(ctx, req.GetExerciseCode())
	if err != nil {
		return nil, err
	}
	return &contentv1.GetExerciseResponse{
		Exercise: toExerciseProto(item),
	}, nil
}
```

`service` 不应该：

- 写 SQL。
- 直接使用 Redis。
- 实现复杂业务判断。
- 调用其他服务的数据库。

## 8. 第七步：注册 HTTP 和 gRPC

相关文件位于：

```text
app/<service>/internal/server/server.go
```

HTTP 注册：

```go
contentv1.RegisterContentServiceHTTPServer(srv, svc)
```

gRPC 注册：

```go
contentv1.RegisterContentServiceServer(srv, svc)
```

HTTP 和 gRPC 注册同一个 `svc`，所以不需要写两套业务实现。

只有在下列情况才需要修改 server：

- 新增了一个 Proto service，而不是在已有 service 中新增 RPC。
- 增加认证、日志、限流、追踪等中间件。
- 增加开发环境的 gRPC Reflection。

## 9. 第八步：完成依赖装配

当前项目在 `app/<service>/cmd/<service>/main.go` 中使用构造函数手动装配：

```go
repo := data.NewContentRepo()
uc := biz.NewContentUsecase(repo)
svc := service.NewContentService(uc)
hs := server.NewHTTPServer(bc.Server, svc)
gs := server.NewGRPCServer(bc.Server, svc)
```

如果只是给已有 Usecase 和 Service 增加方法，通常不需要修改装配代码。

如果新增 Repo、Usecase、Service 或第三方客户端，需要在这里把依赖连接起来。以后项目引入 Wire 后，这一步改为更新各层 `ProviderSet` 并执行：

```bash
go generate ./...
```

## 10. 第九步：测试

推荐测试层次：

| 层 | 测试重点 |
|---|---|
| `biz` 单元测试 | 业务规则、参数边界、状态变化、错误码 |
| `data` 集成测试 | SQL、事务、缓存、第三方适配器 |
| `service` 测试 | DTO 转换、错误传递 |
| 接口测试 | HTTP 路由与 gRPC 调用是否可用 |

至少先执行目标服务测试：

```bash
go test ./app/core/...
```

再执行全量检查：

```bash
make lint
make test
make build
```

### 10.1 HTTP 联调

启动 core-service：

```bash
make run-core
```

调用接口：

```bash
curl http://127.0.0.1:8000/v1/exercises/cable-row
```

### 10.2 gRPC 联调

生成的 `NewContentServiceClient` 可以直接用于服务间调用。使用标准 gRPC 客户端的最小示例：

```go
conn, err := grpc.NewClient(
	"127.0.0.1:9000",
	grpc.WithTransportCredentials(insecure.NewCredentials()),
)
if err != nil {
	return err
}
defer conn.Close()

client := contentv1.NewContentServiceClient(conn)
reply, err := client.GetExercise(ctx, &contentv1.GetExerciseRequest{
	ExerciseCode: "cable-row",
})
```

如果开发环境启用了 gRPC Reflection，也可以使用：

```bash
grpcurl \
  -plaintext \
  -d '{"exercise_code":"cable-row"}' \
  127.0.0.1:9000 \
  content.v1.ContentService/GetExercise
```

生产环境是否开放 Reflection 需要单独评估，不应默认暴露。

## 11. 完成检查清单

提交一个新接口前逐项确认：

- [ ] 接口放在正确的业务服务中。
- [ ] Proto RPC、请求、响应和 HTTP 路由命名清晰。
- [ ] 已考虑鉴权、幂等、分页和错误码。
- [ ] 已执行 `make api`，没有手改生成文件。
- [ ] `service` 只做协议适配，业务规则位于 `biz`。
- [ ] Repo 接口定义在 `biz`，实现位于 `data`。
- [ ] HTTP 和 gRPC 注册使用同一个 Service 实例。
- [ ] 新依赖已完成构造函数装配。
- [ ] 已补充业务单元测试。
- [ ] `make lint`、`make test`、`make build` 全部通过。
- [ ] 涉及不兼容变更时，没有破坏已经发布的 Proto 字段编号。
- [ ] 新增配置、数据库迁移和环境变量时已同步更新文档。

## 12. 当前可参考的完整示例

core-service 的内容模块已包含一条完整链路：

```text
api/content/v1/content.proto
        ↓
app/core/internal/service/content.go
        ↓
app/core/internal/biz/content.go
        ↓
app/core/internal/data/content.go
        ↓
app/core/internal/server/server.go
        ↓
app/core/cmd/core/main.go
```

第一次开发新接口时，建议按这条链路逐层对照。
