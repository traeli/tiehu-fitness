# 搭建和启动

## 环境

- Go 1.25+
- GNU Make
- Buf CLI（`make init` 安装）

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

## 配置微信登录

```bash
export WECHAT_APP_ID='你的小程序 AppID'
export WECHAT_APP_SECRET='你的小程序 AppSecret'
make run-core
```

小程序调用 `wx.login()` 后，将一次性 `code` 发送到：

```http
POST /v1/auth/wechat/login
Content-Type: application/json

{"code":"wx.login 返回的 code","device_id":"device-1"}
```

## 启动服务

```bash
make run-core
make run-vision
```

默认端口：

- core：HTTP `8000`，gRPC `9000`
- vision：HTTP `8100`，gRPC `9100`

本地验证 core-service 中的内容接口：

```bash
curl http://127.0.0.1:8000/v1/equipment/cable-machine
```

正式环境通过 Nginx、Kubernetes Ingress 或云 API 网关统一域名和 TLS，不额外创建业务 gateway-service。
