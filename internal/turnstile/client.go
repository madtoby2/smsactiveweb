package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const siteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type Verifier interface {
	Verify(context.Context, string, string) error
}

type Client struct {
	Secret string
	HTTP   *http.Client
	URL    string
}

func New(secret string) *Client {
	return &Client{Secret: secret, HTTP: &http.Client{Timeout: 10 * time.Second}, URL: siteverifyURL}
}

func (c *Client) Verify(ctx context.Context, token, remoteIP string) error {
	if c.Secret == "" || strings.TrimSpace(token) == "" {
		return errors.New("human verification is required")
	}
	form := url.Values{"secret": {c.Secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		Success bool `json:"success"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil || !result.Success {
		return errors.New("human verification failed")
	}
	return nil
}
