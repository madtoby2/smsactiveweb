# 云码台

基于 HeroSMS 的接码平台，支持实时国家/服务/价格、取号查码、取消退款、余额账本和充值订单。售价按上游美元成本换算人民币后固定加 `¥1.00`。

支付支持易收米 API；本地默认使用沙箱支付。易收米启用前应先取得其对实际业务类目的书面准入确认，并用真实身份完成签约。

## 运行

要求 Go 1.24+。

```powershell
Copy-Item .env.example .env
# 编辑 .env，填写 HEROSMS_API_KEY
go run .
```

打开 `http://localhost:3000`。本仓库当前 `.env` 已包含本机使用的 HeroSMS 密钥且被 Git 忽略。

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
