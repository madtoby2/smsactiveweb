package epay

import (
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	PID, Key, BaseURL           string
	PlatformPublicKey           string
	MerchantPrivateKey          string
	HTTPClient                  *http.Client
}

type RefundResult struct {
	Code         int               `json:"code"`
	Msg          string            `json:"msg"`
	TradeNo      string            `json:"trade_no"`
	OutRefundNo  string            `json:"out_refund_no"`
	RefundAmount string            `json:"money"`
	Raw          map[string]string `json:"raw"`
}

func New(pid, key, baseURL string, options ...func(*Client)) *Client {
	client := &Client{PID: pid, Key: key, BaseURL: strings.TrimRight(baseURL, "/")}
	for _, option := range options {
		option(client)
	}
	return client
}

func WithRefundKeys(platformPublicKey, merchantPrivateKey string) func(*Client) {
	return func(client *Client) {
		client.PlatformPublicKey = platformPublicKey
		client.MerchantPrivateKey = merchantPrivateKey
	}
}

func Sign(values url.Values, key string) string {
	keys := make([]string, 0, len(values))
	for name := range values {
		if name != "sign" && name != "sign_type" && values.Get(name) != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	var source strings.Builder
	for i, name := range keys {
		if i > 0 {
			source.WriteByte('&')
		}
		source.WriteString(name)
		source.WriteByte('=')
		source.WriteString(values.Get(name))
	}
	source.WriteString(key)
	sum := md5.Sum([]byte(source.String()))
	return hex.EncodeToString(sum[:])
}

func Verify(values url.Values, key string) bool {
	provided := strings.ToLower(values.Get("sign"))
	expected := Sign(values, key)
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) CheckoutURL(orderID string, amountFen int64, payType int, notifyURL, returnURL string) (string, error) {
	method, err := paymentMethod(payType)
	if err != nil {
		return "", err
	}
	if orderID == "" || amountFen <= 0 || c.PID == "" || c.Key == "" || c.BaseURL == "" {
		return "", errors.New("invalid epay checkout parameters")
	}
	values := url.Values{
		"pid":          {c.PID},
		"type":         {method},
		"notify_url":   {notifyURL},
		"return_url":   {returnURL},
		"out_trade_no": {orderID},
		"name":         {"SMS activation order"},
		"money":        {FormatMoney(amountFen)},
	}
	values.Set("sign", Sign(values, c.Key))
	values.Set("sign_type", "MD5")
	return c.BaseURL + "/submit.php?" + values.Encode(), nil
}

func (c *Client) Refund(tradeNo string, amountFen int64, outRefundNo string) (RefundResult, error) {
	if c.PID == "" || c.BaseURL == "" || strings.TrimSpace(tradeNo) == "" || strings.TrimSpace(outRefundNo) == "" || amountFen <= 0 {
		return RefundResult{}, errors.New("invalid epay refund parameters")
	}
	if strings.TrimSpace(c.PlatformPublicKey) == "" || strings.TrimSpace(c.MerchantPrivateKey) == "" {
		return RefundResult{}, errors.New("50Pay refund RSA keys are not configured")
	}
	values := url.Values{
		"pid":           {c.PID},
		"timestamp":     {strconv.FormatInt(time.Now().Unix(), 10)},
		"trade_no":      {strings.TrimSpace(tradeNo)},
		"money":         {FormatMoney(amountFen)},
		"out_refund_no": {strings.TrimSpace(outRefundNo)},
	}
	signature, err := c.rsaPrivateSign(signContent(values))
	if err != nil {
		return RefundResult{}, err
	}
	values.Set("sign", signature)
	values.Set("sign_type", "RSA")
	request, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/pay/refund", strings.NewReader(values.Encode()))
	if err != nil {
		return RefundResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return RefundResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return RefundResult{}, err
	}
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		return RefundResult{}, fmt.Errorf("invalid 50Pay refund response: %w", err)
	}
	code, _ := payload["code"].(float64)
	if int(code) != 0 {
		if message := stringField(payload, "msg"); message != "" {
			return RefundResult{}, errors.New(message)
		}
		return RefundResult{}, fmt.Errorf("50Pay refund failed with code %d", int(code))
	}
	if err = c.verifyRefundResponse(payload); err != nil {
		return RefundResult{}, err
	}
	raw := map[string]string{}
	for key, value := range payload {
		raw[key] = fmt.Sprint(value)
	}
	result := RefundResult{
		Code:         int(code),
		Msg:          stringField(payload, "msg"),
		TradeNo:      stringField(payload, "trade_no"),
		OutRefundNo:  stringField(payload, "out_refund_no"),
		RefundAmount: stringField(payload, "money"),
		Raw:          raw,
	}
	if result.TradeNo == "" {
		result.TradeNo = tradeNo
	}
	if result.OutRefundNo == "" {
		result.OutRefundNo = outRefundNo
	}
	if result.RefundAmount == "" {
		result.RefundAmount = FormatMoney(amountFen)
	}
	return result, nil
}

func paymentMethod(payType int) (string, error) {
	switch payType {
	case 1, 11:
		return "alipay", nil
	case 2, 3:
		return "wxpay", nil
	default:
		return "", fmt.Errorf("unsupported epay method %d", payType)
	}
}

func FormatMoney(fen int64) string {
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}

func ParseMoneyFen(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, errors.New("invalid money")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("invalid money")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("invalid money")
	}
	fraction := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 2 {
			return 0, errors.New("invalid money")
		}
		fractionText := parts[1]
		if len(fractionText) == 1 {
			fractionText += "0"
		}
		fraction, err = strconv.ParseInt(fractionText, 10, 64)
		if err != nil {
			return 0, errors.New("invalid money")
		}
	}
	if whole > (1<<63-1-fraction)/100 {
		return 0, errors.New("money overflow")
	}
	return whole*100 + fraction, nil
}

func signContent(values url.Values) string {
	keys := make([]string, 0, len(values))
	for name := range values {
		if name != "sign" && name != "sign_type" && values.Get(name) != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		parts = append(parts, name+"="+values.Get(name))
	}
	return strings.Join(parts, "&")
}

func stringField(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}

func (c *Client) verifyRefundResponse(payload map[string]any) error {
	signature := stringField(payload, "sign")
	timestamp := stringField(payload, "timestamp")
	if signature == "" || timestamp == "" {
		return errors.New("50Pay refund response is missing signature fields")
	}
	timestampValue, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("50Pay refund response timestamp is invalid")
	}
	now := time.Now().Unix()
	if now-timestampValue > 300 || timestampValue-now > 300 {
		return errors.New("50Pay refund response timestamp is out of range")
	}
	values := url.Values{}
	for key, value := range payload {
		if value == nil {
			continue
		}
		if key == "sign" || key == "sign_type" {
			continue
		}
		values.Set(key, stringField(payload, key))
	}
	return c.rsaPublicVerify(signContent(values), signature)
}

func (c *Client) rsaPrivateSign(message string) (string, error) {
	key, err := parsePrivateKey(c.MerchantPrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid 50Pay merchant private key: %w", err)
	}
	hash := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func (c *Client) rsaPublicVerify(message, signature string) error {
	key, err := parsePublicKey(c.PlatformPublicKey)
	if err != nil {
		return fmt.Errorf("invalid 50Pay platform public key: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return errors.New("invalid 50Pay response signature")
	}
	hash := sha256.Sum256([]byte(message))
	if err = rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], decoded); err != nil {
		return errors.New("50Pay refund response signature verification failed")
	}
	return nil
}

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	text := normalizePEM(raw, "PRIVATE KEY")
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		text = normalizePEM(raw, "RSA PRIVATE KEY")
		block, _ = pem.Decode([]byte(text))
		if block == nil {
			return nil, errors.New("missing private key block")
		}
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		privateKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not RSA")
		}
		return privateKey, nil
	}
	privateKey, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if pkcs1Err != nil {
		return nil, err
	}
	return privateKey, nil
}

func parsePublicKey(raw string) (*rsa.PublicKey, error) {
	text := normalizePEM(raw, "PUBLIC KEY")
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("missing public key block")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	publicKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return publicKey, nil
}

func normalizePEM(raw, kind string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "BEGIN "+kind) {
		return raw
	}
	chunks := []string{}
	for start := 0; start < len(raw); start += 64 {
		end := start + 64
		if end > len(raw) {
			end = len(raw)
		}
		chunks = append(chunks, raw[start:end])
	}
	return "-----BEGIN " + kind + "-----\n" + strings.Join(chunks, "\n") + "\n-----END " + kind + "-----"
}
