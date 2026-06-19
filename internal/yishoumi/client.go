package yishoumi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	AppID, Secret, BaseURL string
	HTTP                   *http.Client
}
type CreateResult struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	OrderID string `json:"ordeid"`
	Sign    string `json:"sign"`
	URL     string `json:"url"`
}

func New(appID, secret, base string) *Client {
	return &Client{appID, secret, strings.TrimRight(base, "/"), &http.Client{Timeout: 15 * time.Second}}
}
func Sign(values url.Values, secret string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "sign" && k != "hash" && values.Get(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values.Get(k))
	}
	b.WriteString(secret)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
func Verify(v url.Values, secret string) bool {
	got := v.Get("sign")
	if got == "" {
		got = v.Get("hash")
	}
	return got != "" && strings.EqualFold(got, Sign(v, secret))
}
func (c *Client) Create(ctx context.Context, order string, total int64, payType int, notify, callback string) (CreateResult, error) {
	v := url.Values{"appid": {c.AppID}, "mch_orderid": {order}, "description": {"Balance recharge"}, "total": {strconv.FormatInt(total, 10)}, "payType": {strconv.Itoa(payType)}, "notify_url": {notify}, "nopay_url": {callback}, "callback_url": {callback}, "time": {strconv.FormatInt(time.Now().Unix(), 10)}, "nonce_str": {fmt.Sprintf("%d", time.Now().UnixNano())}}
	v.Set("sign", Sign(v, c.Secret))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/u/payment", strings.NewReader(v.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return CreateResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CreateResult{}, err
	}
	var out CreateResult
	if err = json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	if out.Code != 0 {
		return out, fmt.Errorf("Yishoumi: %s", out.Msg)
	}
	values := url.Values{"code": {strconv.Itoa(out.Code)}, "msg": {out.Msg}, "ordeid": {out.OrderID}, "url": {out.URL}, "sign": {out.Sign}}
	if !Verify(values, c.Secret) {
		return out, fmt.Errorf("invalid Yishoumi response signature")
	}
	return out, nil
}
