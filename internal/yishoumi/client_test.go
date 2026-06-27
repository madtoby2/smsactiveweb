package yishoumi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSign(t *testing.T) {
	v := url.Values{"z": {"2"}, "a": {"1"}, "sign": {"ignored"}, "empty": {""}}
	if Sign(v, "key") != "8ec209fe9f747b11926e9150f67cdcce059afa366cc6417102cbfe6753dcd1f9" {
		t.Fatal("unexpected signature")
	}
}

func TestCreateVerifiesSignedResponse(t *testing.T) {
	secret := "secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/u/payment" || r.Method != http.MethodPost {
			http.Error(w, "bad request", 400)
			return
		}
		_ = r.ParseForm()
		if !Verify(r.PostForm, secret) {
			http.Error(w, "bad sign", 400)
			return
		}
		v := url.Values{"code": {"0"}, "msg": {"SUCCESS!"}, "ordeid": {r.PostForm.Get("mch_orderid")}, "url": {"https://pay.example/checkout"}}
		v.Set("sign", Sign(v, secret))
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "SUCCESS!", "ordeid": v.Get("ordeid"), "url": v.Get("url"), "sign": v.Get("sign")})
	}))
	defer server.Close()
	c := New("app", secret, server.URL)
	out, err := c.Create(context.Background(), "R123456", 100, 2, "https://merchant.example/notify", "https://merchant.example/return")
	if err != nil {
		t.Fatal(err)
	}
	if out.URL != "https://pay.example/checkout" {
		t.Fatalf("url=%s", out.URL)
	}
}
