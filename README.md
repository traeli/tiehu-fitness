# 铁虎健身教练后端

基于 Go 1.25 和 Kratos v3 的健身辅助后端，当前以 Web 联调为主并保留微信小程序登录能力，只部署两个服务：

- `core-service`：Web 注册登录、微信登录自动注册、会话、用户档案、器材动作内容、健身计划、训练记录和打卡。
- `vision-service`：图片/视频处理、器材识别、姿势分析、AI 任务、模型版本和内部异步 Worker。

公网流量由 Nginx、Kubernetes Ingress 或云 API 网关按路径转发，不在业务层新增 gateway-service。

```bash
make init
make all
make test
make build
```

详细文档：

- [需求整理](docs/requirements.md)
- [双服务架构](docs/architecture.md)
- [PostgreSQL 与 GORM 数据库设计](docs/database.md)
- [自建邮箱验证码方案](docs/email-verification.md)
- [搭建和启动](docs/setup.md)
- [新增 API 与 gRPC 开发流程](docs/api-development.md)
