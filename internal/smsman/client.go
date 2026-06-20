package smsman

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

type Client struct {
	Token, BaseURL string
	HTTP           *http.Client
}

type Item struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Activation struct {
	ID, Phone string
}

type Quote struct {
	ApplicationID int
	Price         float64
	Count         int
}

var ErrSMSPending = errors.New("SMS-Man code is pending")

type APIError struct {
	Code, Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return "SMS-Man: " + e.Code
	}
	return "SMS-Man: " + e.Code + ": " + e.Message
}

func ErrorCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func New(token, baseURL string) *Client {
	return &Client{Token: token, BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) call(ctx context.Context, method string, params url.Values) ([]byte, error) {
	u, err := url.Parse(c.BaseURL + "/" + strings.TrimLeft(method, "/"))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("token", c.Token)
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SMS-Man HTTP %d", resp.StatusCode)
	}
	var failure struct {
		Code    string `json:"error_code"`
		Message string `json:"error_msg"`
	}
	if json.Unmarshal(body, &failure) == nil && (failure.Code != "" || failure.Message != "") {
		return nil, &APIError{Code: failure.Code, Message: failure.Message}
	}
	return body, nil
}

func (c *Client) Countries(ctx context.Context) ([]Item, error) {
	return c.items(ctx, "countries")
}

func (c *Client) Applications(ctx context.Context) ([]Item, error) {
	return c.items(ctx, "applications")
}

func (c *Client) items(ctx context.Context, method string) ([]Item, error) {
	body, err := c.call(ctx, method, nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(raw))
	for key, value := range raw {
		var record struct {
			ID    json.Number `json:"id"`
			Name  string      `json:"name"`
			Title string      `json:"title"`
		}
		if json.Unmarshal(value, &record) != nil {
			continue
		}
		id, _ := strconv.Atoi(record.ID.String())
		if id == 0 {
			id, _ = strconv.Atoi(key)
		}
		name := record.Name
		if name == "" {
			name = record.Title
		}
		if id > 0 && name != "" {
			items = append(items, Item{ID: id, Name: name})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// Limits returns the provider payload unchanged because SMS-Man can return
// either a single country/application record or a nested availability table.
func (c *Client) Limits(ctx context.Context, countryID, applicationID int) (json.RawMessage, error) {
	params := url.Values{}
	if countryID > 0 {
		params.Set("country_id", strconv.Itoa(countryID))
	}
	if applicationID > 0 {
		params.Set("application_id", strconv.Itoa(applicationID))
	}
	body, err := c.call(ctx, "limits", params)
	return json.RawMessage(body), err
}

// Quotes normalizes the different nested limit payloads returned by SMS-Man
// into application-scoped prices and inventory counts.
func (c *Client) Quotes(ctx context.Context, countryID int) (map[int]Quote, error) {
	raw, err := c.Limits(ctx, countryID, 0)
	if err != nil {
		return nil, err
	}
	var payload any
	if err = json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	out := map[int]Quote{}
	walkQuotes(payload, 0, out)
	return out, nil
}

func walkQuotes(value any, applicationHint int, out map[int]Quote) {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			walkQuotes(item, applicationHint, out)
		}
	case map[string]any:
		applicationID := int(number(current["application_id"]))
		if applicationID == 0 {
			applicationID = int(number(current["applicationId"]))
		}
		if applicationID == 0 {
			applicationID = applicationHint
		}
		price := number(current["price"])
		if price == 0 {
			price = number(current["cost"])
		}
		count := int(number(current["count"]))
		if applicationID > 0 && price > 0 && count > 0 {
			quote := Quote{ApplicationID: applicationID, Price: price, Count: count}
			if existing, ok := out[applicationID]; !ok || quote.Price < existing.Price {
				out[applicationID] = quote
			}
		}
		for key, child := range current {
			hint := applicationHint
			if id, parseErr := strconv.Atoi(key); parseErr == nil {
				hint = id
			}
			walkQuotes(child, hint, out)
		}
	}
}

func number(value any) float64 {
	switch current := value.(type) {
	case float64:
		return current
	case json.Number:
		result, _ := current.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(current, 64)
		return result
	}
	return 0
}

func (c *Client) Acquire(ctx context.Context, countryID, applicationID int) (Activation, error) {
	body, err := c.call(ctx, "get-number", url.Values{
		"country_id":     {strconv.Itoa(countryID)},
		"application_id": {strconv.Itoa(applicationID)},
	})
	if err != nil {
		return Activation{}, err
	}
	var response struct {
		RequestID json.Number `json:"request_id"`
		Number    string      `json:"number"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return Activation{}, err
	}
	if response.RequestID.String() == "" || response.Number == "" {
		return Activation{}, errors.New("SMS-Man returned an incomplete activation")
	}
	return Activation{ID: response.RequestID.String(), Phone: response.Number}, nil
}

func (c *Client) SMS(ctx context.Context, requestID string) (string, error) {
	body, err := c.call(ctx, "get-sms", url.Values{"request_id": {requestID}})
	if err != nil {
		return "", err
	}
	var response struct {
		Code string `json:"sms_code"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if response.Code == "" {
		return "", ErrSMSPending
	}
	return response.Code, nil
}

func IsPending(err error) bool {
	if errors.Is(err, ErrSMSPending) {
		return true
	}
	code := strings.ToLower(ErrorCode(err))
	return code == "wait_sms" || code == "sms_not_found" || code == "no_sms"
}

func (c *Client) Reject(ctx context.Context, requestID string) error {
	body, err := c.call(ctx, "set-status", url.Values{"request_id": {requestID}, "status": {"reject"}})
	if err != nil {
		return err
	}
	var response struct {
		Success bool   `json:"success"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(body, &response) != nil || (!response.Success && !strings.EqualFold(response.Status, "success")) {
		return errors.New("SMS-Man did not confirm rejection")
	}
	return nil
}
