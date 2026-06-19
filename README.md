# 云码台

基于 HeroSMS 的接码平台，支持实时国家/服务/价格、取号查码、自动换号、取消退款、余额账本和充值订单。售价按上游美元成本换算人民币后固定加 `¥1.00`。

订单可选择自动换号。默认等待 180 秒仍未收到验证码时，系统会先向 HeroSMS 查询一次，确认无验证码后取消旧号码并按原锁定成本获取新号码，最多换 2 次。换号不会再次扣用户余额；旧号取消后若新号获取失败，订单自动退款。

登录使用邮箱作为账户标识，浏览器 Cookie 只保存随机会话令牌，不保存邮箱或密码。会话有效期为 30 天，活跃期间自动续期；退出登录会同步删除服务端会话。

支付支持易收米 API；本地默认使用沙箱支付。易收米启用前应先取得其对实际业务类目的书面准入确认，并用真实身份完成签约。

## 运行

要求 Go 1.24+。

```powershell
Copy-Item .env.example .env
# 编辑 .env，填写 HEROSMS_API_KEY
go run .
```

打开 `http://localhost:3000`。本仓库当前 `.env` 已包含本机使用的 HeroSMS 密钥且被 Git 忽略。

自动换号可通过 `SMS_AUTO_REPLACE_AFTER_SECONDS`、`SMS_AUTO_REPLACE_MAX_ATTEMPTS` 和 `SMS_AUTO_REPLACE_SCAN_SECONDS` 调整。HeroSMS 前 120 秒不允许取消，因此等待时间会被限制在 120 至 900 秒。

## 启用易收米

在 `.env` 设置：

```dotenv
PAY_PROVIDER=yishoumi
YSM_APP_ID=平台下发的通道ID
YSM_SECRET=平台下发的密钥
APP_BASE_URL=https://你的公网HTTPS域名
```

易收米异步通知地址为：`https://你的域名/api/payments/yishoumi/notify`。充值只在通知签名、APPID、订单状态、订单号和金额全部校验通过后入账，且同一订单只能入账一次。

## 检查

```powershell
go test ./...
go vet ./...
```

## Docker

```powershell
Copy-Item .env.example .env
# 编辑 .env 后启动
docker compose up -d --build
```

健康检查地址：`GET /healthz`。
