# 铁虎健身教练后端

基于 Go 1.25 和 Kratos v3 的微信小程序后端，当前保持两个部署服务：

- `core-service`：微信登录自动注册、会话、用户档案、器材动作内容、健身计划、训练记录和打卡。
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
- [搭建和启动](docs/setup.md)
- [新增 API 与 gRPC 开发流程](docs/api-development.md)
