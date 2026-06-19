package epay

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type Client struct {
	PID, Key, BaseURL string
}

func New(pid, key, baseURL string) *Client {
	return &Client{PID: pid, Key: key, BaseURL: strings.TrimRight(baseURL, "/")}
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
