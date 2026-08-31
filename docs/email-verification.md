# 自建邮箱验证码方案

## 结论

邮箱验证码属于 `core-service` 的身份业务，不新增微服务。core 通过 SMTP 投递邮件，验证码生成、频率限制、过期、校验和一次性消费都由 core 管理。

推荐分两套环境：

| 环境 | 开源组件 | 用途 |
|---|---|---|
| 本地开发、自动化测试 | [Mailpit](https://mailpit.axllent.org/docs/install/docker/) | 捕获 SMTP 邮件并在 Web UI 查看，不向公网投递 |
| 自建生产投递 | [Maddy](https://maddy.email/tutorials/setting-up/) | 轻量 SMTP/MTA，适合当前项目规模 |
| 大批量事务邮件运营 | [Postal](https://docs.postalserver.io/getting-started/) | 提供 Web 管理、队列、日志、统计等完整投递平台 |

不建议当前使用 mailcow。它是一整套邮箱托管系统，包含 IMAP、Webmail、反垃圾等大量能力；本项目只需要发送验证码，运维成本不划算。

## 本地开发

Mailpit 默认 SMTP 端口为 `1025`，Web UI 为 `8025`：

```bash
docker run -d \
  --restart unless-stopped \
  --name tiehu-mailpit \
  -p 127.0.0.1:1025:1025 \
  -p 127.0.0.1:8025:8025 \
  axllent/mailpit:v1.27
```

core-service 未来的本地 SMTP 配置：

```text
host: 127.0.0.1
port: 1025
tls: false
from: no-reply@tiehu.local
```

浏览器打开 `http://127.0.0.1:8025` 查看验证码邮件。Mailpit 仅用于开发，不能承担真实邮件投递。

## 生产自建 SMTP

当前规模优先选 Maddy。服务器至少需要：

- 独立公网 IP，并确认出站 TCP 25 端口没有被机房封禁。
- 邮件域名和固定主机名，例如 `mail.example.com`。
- 正确的 A/AAAA、MX、PTR/rDNS、SPF、DKIM、DMARC 记录。
- TLS 证书、队列监控、退信处理、日志脱敏、备份和升级流程。

自建软件不等于零外部依赖：域名、DNS 和具备良好 IP 信誉的服务器仍然必需。缺少 PTR、SPF、DKIM 或 DMARC 时，验证码邮件很容易进入垃圾箱或被 Gmail、Outlook、QQ 邮箱拒收。

## 推荐接口

第一阶段只支持注册用途，后续再扩展找回密码：

```http
POST /v1/auth/email-verification-codes
Content-Type: application/json

{"email":"user@example.com"}
```

响应不返回验证码：

```json
{
  "verification_id": "opaque-random-id",
  "expires_in_seconds": 600
}
```

注册接口增加两个字段：

```json
{
  "email": "user@example.com",
  "verification_id": "opaque-random-id",
  "verification_code": "123456",
  "password": "strong-password",
  "nickname": "用户"
}
```

`purpose` 属于有限集合。支持找回密码时，必须在 Proto 和 biz 中定义枚举，例如 `REGISTER`、`RESET_PASSWORD`，禁止直接用字符串判断。

## 数据与安全规则

建议新增 `email_verifications` 表：

```text
id                    随机 UUID
email                 规范化邮箱
purpose               受约束的枚举值
code_hash             验证码 HMAC-SHA256，不保存明文
expires_at            过期时间
attempt_count          已失败次数
consumed_at            一次性消费时间
created_at             创建时间
```

必须满足：

- 使用 `crypto/rand` 生成 6 位验证码和不可猜测的 `verification_id`。
- 验证码有效期建议 10 分钟，同一邮箱至少间隔 60 秒才能重发。
- 单验证码最多尝试 5 次；成功后在事务中消费并创建用户。
- 新验证码创建后使该邮箱之前未消费的同用途验证码失效。
- `code_hash` 使用服务端密钥做 HMAC-SHA256，密钥只从环境变量或 Secret 注入。
- 日志、错误响应、Tracing 和指标 Label 都不能包含验证码、密码或完整邮箱。
- 发送接口对邮箱和客户端 IP 同时限流；无论邮箱是否已注册，都返回一致响应。
- 邮件投递放入 outbox/异步队列，避免 SMTP 延迟占用注册 HTTP 请求。

## 代码边界

实现时保持现有 Kratos 分层：

```text
service -> biz EmailVerificationUsecase <- data SMTPEmailSender
                                      <- data EmailVerificationRepo
```

- `biz` 定义 `EmailSender`、验证码规则和 Repo 接口。
- `data` 实现 SMTP 客户端、GORM Repo 和事务。
- `service` 只转换 Proto DTO。
- SMTP 主机、端口、TLS、账号、密码和发件人通过配置注入，禁止写死。
