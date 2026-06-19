# 云码台

基于 HeroSMS 的接码平台，支持实时国家/服务/价格、按单支付、支付成功后取号、查码和持续自动换号。售价按上游美元成本换算人民币后固定加 `¥1.00`。

默认等待 180 秒仍未收到验证码时，系统会先向 HeroSMS 查询一次；确认无验证码并且上游取消成功后，才按原锁定成本获取新号码。默认持续换号直到收到验证码。新号码暂时无库存时会保留已支付订单并继续重试，不会重复调用支付接口。

登录使用邮箱作为账户标识，浏览器 Cookie 只保存随机会话令牌，不保存邮箱或密码。会话有效期为 30 天，活跃期间自动续期；退出登录会同步删除服务端会话。

每次购买先锁定 HeroSMS 报价并加 `¥1.00`，再为该接码订单调用支付 API；只有服务器验签确认付款成功，系统才会向 HeroSMS 取号。支付支持 50Pay（彩虹易支付兼容协议）和易收米 API，本地默认使用沙箱支付。

## 运行

要求 Go 1.24+。

```powershell
Copy-Item .env.example .env
# 编辑 .env，填写 HEROSMS_API_KEY
go run .
```

打开 `http://localhost:3000`。本仓库当前 `.env` 已包含本机使用的 HeroSMS 密钥且被 Git 忽略。

自动换号可通过 `SMS_AUTO_REPLACE_AFTER_SECONDS`、`SMS_AUTO_REPLACE_MAX_ATTEMPTS` 和 `SMS_AUTO_REPLACE_SCAN_SECONDS` 调整。`SMS_AUTO_REPLACE_MAX_ATTEMPTS=0` 表示持续换号。HeroSMS 前 120 秒不允许取消，因此等待时间会被限制在 120 至 900 秒。

## 启用易收米

在 `.env` 设置：

```dotenv
PAY_PROVIDER=yishoumi
YSM_APP_ID=平台下发的通道ID
YSM_SECRET=平台下发的密钥
APP_BASE_URL=https://你的公网HTTPS域名
```

易收米异步通知地址为：`https://你的域名/api/payments/yishoumi/notify`。接码订单只在通知签名、APPID、订单状态、订单号和金额全部校验通过后取号；同一支付回调重复到达也只能获取一个号码。

## 启用 50Pay

50Pay SDK 使用彩虹易支付兼容协议。先配置公网 HTTPS 域名，再在 `.env` 设置：

```dotenv
PAY_PROVIDER=epay
EPAY_BASE_URL=https://50pay.xiajuan88.com
EPAY_PID=商户ID
EPAY_KEY=商户密钥
APP_BASE_URL=https://你的公网HTTPS域名
```

异步通知地址为 `https://你的域名/api/payments/epay/notify`，同步返回地址为 `https://你的域名/api/payments/epay/return`。平台会校验 MD5 签名、商户号、`TRADE_SUCCESS` 状态、订单号、精确金额和第三方交易号；通知重复到达只会取一次号码。商户密钥只应保存在服务器 `.env`，不能提交到 Git。

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
