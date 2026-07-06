package hero

import (
	"context"
	"encoding/json"
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

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"

type Client struct {
	Key, BaseURL, Currency string
	HTTP                   *http.Client
}
type Country struct {
	ID  int    `json:"id"`
	Eng string `json:"eng"`
	Chn string `json:"chn"`
}
type Service struct {
	Code string `json:"code"`
	Name string `json:"name"`
}
type Offer struct {
	Service       string  `json:"service"`
	Country       string  `json:"country"`
	Cost          float64 `json:"cost"`
	Count         int     `json:"count"`
	PhysicalCount int     `json:"physicalCount,omitempty"`
}
type Activation struct {
	ID, Phone string
	Cost      float64
}

type APIError struct{ Code string }

func (e *APIError) Error() string { return "HeroSMS: " + e.Code }
func ErrorCode(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func New(key, baseURL, currency string) *Client {
	return &Client{key, baseURL, currency, &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) call(ctx context.Context, action string, params map[string]string) ([]byte, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("api_key", c.Key)
	q.Set("action", action)
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HeroSMS HTTP %d", resp.StatusCode)
	}
	s := strings.TrimSpace(string(b))
	known := []string{"BAD_KEY", "BAD_ACTION", "BAD_SERVICE", "BAD_COUNTRY", "NO_NUMBERS", "NO_BALANCE", "WRONG_ACTIVATION_ID", "EARLY_CANCEL_DENIED", "FREE_CANCELLATION_EXPIRED", "OTP_RECEIVED", "NEW_OTP_RECEIVED"}
	var payload struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(b, &payload)
	for _, p := range known {
		if strings.HasPrefix(s, p) || payload.Title == p {
			return nil, &APIError{Code: p}
		}
	}
	return b, nil
}

func (c *Client) Countries(ctx context.Context) ([]Country, error) {
	b, e := c.call(ctx, "getCountries", nil)
	if e != nil {
		return nil, e
	}
	var out []Country
	if json.Unmarshal(b, &out) == nil {
		return out, nil
	}
	var keyed map[string]Country
	if e = json.Unmarshal(b, &keyed); e != nil {
		return nil, e
	}
	for _, country := range keyed {
		out = append(out, country)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (c *Client) Services(ctx context.Context, country string) ([]Service, error) {
	b, e := c.call(ctx, "getServicesList", map[string]string{"country": country})
	if e != nil {
		return nil, e
	}
	var x struct {
		Services []Service `json:"services"`
	}
	e = json.Unmarshal(b, &x)
	return x.Services, e
}

func (c *Client) Offers(ctx context.Context, country string) ([]Offer, error) {
	b, e := c.call(ctx, "getPrices", map[string]string{"country": country, "currency": c.Currency})
	if e != nil {
		return nil, e
	}
	var raw any
	if e = json.Unmarshal(b, &raw); e != nil {
		return nil, e
	}
	root := raw
	if m, ok := raw.(map[string]any); ok {
		if v, yes := m[country]; yes {
			root = v
		}
		if v, yes := m["data"]; yes {
			root = v
			if dm, ok := v.(map[string]any); ok {
				if cv, yes := dm[country]; yes {
					root = cv
				}
			}
		}
	}
	var out []Offer
	if country == "" {
		if m, ok := root.(map[string]any); ok {
			for countryID, services := range m {
				if countryID == "data" {
					if data, dataOK := services.(map[string]any); dataOK {
						for nestedCountry, nestedServices := range data {
							appendCountryOffers(&out, nestedCountry, nestedServices)
						}
					}
					continue
				}
				appendCountryOffers(&out, countryID, services)
			}
		}
		return out, nil
	}
	if arr, ok := root.([]any); ok {
		for _, v := range arr {
			if m, ok := v.(map[string]any); ok {
				for k, vv := range m {
					appendOffer(&out, country, k, vv)
				}
			}
		}
	}
	if m, ok := root.(map[string]any); ok {
		for k, v := range m {
			appendOffer(&out, country, k, v)
		}
	}
	return out, nil
}
func appendCountryOffers(out *[]Offer, country string, value any) {
	if services, ok := value.(map[string]any); ok {
		for service, offer := range services {
			appendOffer(out, country, service, offer)
		}
	}
}
func appendOffer(out *[]Offer, country, service string, value any) {
	if m, ok := value.(map[string]any); ok {
		cost, _ := number(m["cost"])
		if cost == 0 {
			cost, _ = number(m["price"])
		}
		physicalCount, _ := number(m["physicalCount"])
		resolvedPhysicalCount := int(physicalCount)
		if cost > 0 {
			*out = append(*out, Offer{
				Service:       service,
				Country:       country,
				Cost:          cost,
				Count:         resolvedPhysicalCount,
				PhysicalCount: resolvedPhysicalCount,
			})
		}
	}
}
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		n, e := strconv.ParseFloat(x, 64)
		return n, e == nil
	}
	return 0, false
}

func (c *Client) Acquire(ctx context.Context, country, service string, maxPrice float64) (Activation, error) {
	b, e := c.call(ctx, "getNumberV2", map[string]string{"country": country, "service": service, "maxPrice": strconv.FormatFloat(maxPrice, 'f', 4, 64), "fixedPrice": "true", "currency": c.Currency})
	if e != nil {
		return Activation{}, e
	}
	var m map[string]any
	if json.Unmarshal(b, &m) == nil {
		id := fmt.Sprint(m["activationId"])
		phone := fmt.Sprint(m["phoneNumber"])
		cost, _ := number(m["activationCost"])
		if id != "<nil>" && phone != "<nil>" {
			return Activation{id, phone, cost}, nil
		}
	}
	p := strings.Split(strings.TrimSpace(string(b)), ":")
	if len(p) == 3 && p[0] == "ACCESS_NUMBER" {
		return Activation{p[1], p[2], maxPrice}, nil
	}
	return Activation{}, fmt.Errorf("HeroSMS unexpected response")
}
func (c *Client) Status(ctx context.Context, id string) (string, error) {
	b, e := c.call(ctx, "getStatus", map[string]string{"id": id})
	return strings.Trim(strings.TrimSpace(string(b)), `"`), e
}
func (c *Client) SetStatus(ctx context.Context, id, status string) (string, error) {
	b, e := c.call(ctx, "setStatus", map[string]string{"id": id, "status": status})
	return strings.Trim(strings.TrimSpace(string(b)), `"`), e
}

func (c *Client) Balance(ctx context.Context) (float64, error) {
	b, err := c.call(ctx, "getBalance", nil)
	if err != nil {
		return 0, err
	}
	value := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if parts := strings.SplitN(value, ":", 2); len(parts) == 2 {
		value = parts[1]
	}
	if balance, parseErr := strconv.ParseFloat(value, 64); parseErr == nil {
		return balance, nil
	}
	var payload map[string]any
	if json.Unmarshal(b, &payload) == nil {
		for _, key := range []string{"balance", "amount"} {
			if balance, ok := number(payload[key]); ok {
				return balance, nil
			}
		}
	}
	return 0, fmt.Errorf("HeroSMS returned an invalid balance")
}

// CancellationSucceeded only accepts responses that explicitly confirm the
// activation was cancelled or refunded. Other ACCESS_* responses represent
// different lifecycle transitions and must never trigger acquisition of a new
// number.
func CancellationSucceeded(response string) bool {
	response = strings.TrimSpace(response)
	if strings.EqualFold(strings.Trim(response, `"`), "ACCESS_CANCEL") {
		return true
	}
	var payload struct {
		Title string `json:"title"`
	}
	if json.Unmarshal([]byte(response), &payload) != nil {
		return false
	}
	return strings.EqualFold(payload.Title, "CANCELED") || strings.EqualFold(payload.Title, "REFUNDED")
}
