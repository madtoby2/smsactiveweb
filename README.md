# smsactiveweb

`smsactiveweb` 是一个面向短信验证码接收场景的接码平台项目，核心目标是**解决 ChatGPT 等国际平台注册、登录、验证过程中验证码难收、号码不稳定、支付链路不顺**的问题。

**云码台**：  
[https://yunmatai.xyz](https://yunmatai.xyz)

## 项目定位

如果你在使用 ChatGPT、OpenAI、Yahoo、Uber、Telegram、Google 等服务时遇到下面这些问题，这个项目就是为此设计的：

- 找不到可用的海外号码
- 收到验证码慢，甚至收不到
- 号码刚拿到就失效
- 支付后取号流程混乱
- 订单关闭、退款、换号逻辑不清晰

`smsactiveweb` 把这些环节串成了一条完整链路：

1. 用户选择服务和国家
2. 平台实时比价并生成订单
3. 用户完成支付
4. 平台向上游取号
5. 平台轮询验证码
6. 用户收到验证码或手动换号
7. 必要时执行关闭订单与退款

## 云码台能做什么

云码台是这个项目的实际产品化形态，主打：

- **解决 ChatGPT 验证码接收问题**
- 覆盖多种热门国际服务
- 按次支付，不走预充值心智
- 实时库存、实时价格
- 支持订单状态跟踪
- 支持人工/后台关闭订单
- 支持支付完成后的取号与收码流程

如果你只是想直接使用成品，而不是自己部署代码，可以直接访问：

- 官网：[https://yunmatai.xyz](https://yunmatai.xyz)
- 联系方式页：[https://yunmatai.xyz/contact.html](https://yunmatai.xyz/contact.html)

## 当前技术能力

项目目前包含这些主要能力：

- 多上游接码供应链整合
- 国家 / 服务目录映射
- 实时库存与报价展示
- 用户注册 / 登录
- 邮箱验证码与 Turnstile 人机验证
- 订单创建、支付、取号、查码
- 手动换号
- 管理后台
- 订单关闭与退款链路
- 公告、客服、邮件日志、审计日志

## 技术栈

- Go
- SQLite
- 原生 HTML / CSS / JavaScript
- Cloudflare Turnstile
- Resend / SMTP
- 50Pay / 易收米 / Sandbox 支付适配

## 本地运行

要求：

- Go 1.24+

### 1. 准备配置

```powershell
Copy-Item .env.example .env
```

然后按需编辑 `.env`。

至少需要关注：

- `APP_BASE_URL`
- `HEROSMS_API_KEY`
- `PAY_PROVIDER`
- `ADMIN_EMAIL`
- `ADMIN_PASSWORD`

如果要启用邮箱验证，还需要配置：

- `RESEND_API_KEY` / `RESEND_FROM`
或
- `SMTP_HOST` / `SMTP_PORT` / `SMTP_FROM`

以及：

- `TURNSTILE_SITE_KEY`
- `TURNSTILE_SECRET`

### 2. 启动

```powershell
go run .
```

默认访问：

- [http://localhost:3000](http://localhost:3000)

## Docker 运行

```powershell
Copy-Item .env.example .env
docker compose up -d --build
```

## 重要配置项

### 接码与价格

- `HEROSMS_API_KEY`
- `HEROSMS_BASE_URL`
- `SMSMAN_API_TOKEN`
- `SMSMAN_BASE_URL`
- `SMSMAN_PRICE_CNY_RATE`
- `USD_CNY_RATE`
- `PRICE_MARKUP_CNY`

说明：

- 当前价格逻辑会在上游成本基础上加固定加价
- 小于 `¥1.00` 的订单会被自动抬到 `¥1.00`

### 支付

#### Sandbox

```dotenv
PAY_PROVIDER=sandbox
```

#### 易收米

```dotenv
PAY_PROVIDER=yishoumi
YSM_APP_ID=
YSM_SECRET=
YSM_BASE_URL=https://www.yishoumi.cn
```

#### 50Pay

```dotenv
PAY_PROVIDER=epay
EPAY_BASE_URL=https://50pay.xiajuan88.com
EPAY_PID=
EPAY_KEY=
EPAY_PLATFORM_PUBLIC_KEY=
EPAY_MERCHANT_PRIVATE_KEY=
```

### 邮箱验证

#### Resend

```dotenv
RESEND_API_KEY=re_xxxxxxxxx
RESEND_FROM=noreply@your-domain.com
```

> `RESEND_FROM` 建议使用你已经在 Resend 后台验证过域名的发件地址。

#### SMTP

```dotenv
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=
SMTP_PASSWORD=
SMTP_FROM=Cloud SMS <no-reply@example.com>
```

### 人机验证

```dotenv
TURNSTILE_SITE_KEY=
TURNSTILE_SECRET=
```

## 管理后台

管理后台地址：

- `/admin.html`

可以管理：

- 数据概览
- 订单
- 支付流水
- 用户
- 公告
- 客服
- 审计
- 系统设置
- 邮件日志

## 数据库

默认数据库路径：

```text
data/platform.db
```

## 健康检查

```text
GET /healthz
```

返回示例：

```json
{"status":"ok"}
```

## 测试

```powershell
go test ./...
go vet ./...
```

## 适用场景

这个项目尤其适合：

- 想快速搭建一个面向 ChatGPT 验证码接收的成品站点
- 需要把接码、支付、收码、退款串成完整流程
- 需要一个可继续二开、可自部署、可接后台运营的接码平台

## 品牌说明

如果你更关心“直接可用的产品”，而不是自己改代码和部署服务器，建议直接访问：

- [https://yunmatai.xyz](https://yunmatai.xyz)

云码台当前重点解决的就是：

> **ChatGPT 验证码接收问题，以及海外平台注册时验证码难获取、号码不稳定、支付后流程不顺的问题。**

## 免责声明

请仅在合法、合规、符合目标平台条款及当地法律法规的前提下使用本项目。  
部署者和运营者应自行承担由实际使用方式带来的合规、风控与业务责任。
