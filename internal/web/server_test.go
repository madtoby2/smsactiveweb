package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sms-platform/internal/config"
	"sms-platform/internal/store"
	"sms-platform/internal/yishoumi"
)

func TestYishoumiNotifyCreditsOnce(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("notify@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	r := store.Recharge{ID: "RNOTIFY", UserID: u.ID, AmountFen: 1234, Provider: "yishoumi", PayType: "2", Token: "token", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateRecharge(r); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{YSMAppID: "app-1", YSMSecret: "secret", PayProvider: "yishoumi"}
	h := New(cfg, db).Routes()
	values := url.Values{"appid": {"app-1"}, "mch_orderid": {r.ID}, "total_fee": {"1234"}, "transaction_id": {"wx-1"}, "ysm_orderid": {"ysm-1"}, "state": {"SUCCESS"}, "time": {"1710000000"}, "nonce_str": {"abc123"}}
	values.Set("sign", yishoumi.Sign(values, cfg.YSMSecret))
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/payments/yishoumi/notify", strings.NewReader(values.Encode()))
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "success" {
			t.Fatalf("callback %d: status=%d body=%q", i, w.Code, w.Body.String())
		}
	}
	var balance int64
	if err = db.DB.QueryRow("SELECT balance_fen FROM users WHERE id=?", u.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 1234 {
		t.Fatalf("balance=%d, want 1234", balance)
	}
}

func TestYishoumiNotifyRejectsWrongAmount(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, _ := db.Register("wrong@example.com", "password123")
	r := store.Recharge{ID: "RWRONG", UserID: u.ID, AmountFen: 500, Provider: "yishoumi", PayType: "2", Token: "token", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateRecharge(r); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{YSMAppID: "app-1", YSMSecret: "secret", PayProvider: "yishoumi"}
	h := New(cfg, db).Routes()
	v := url.Values{"appid": {"app-1"}, "mch_orderid": {r.ID}, "total_fee": {"499"}, "ysm_orderid": {"ysm-2"}, "state": {"SUCCESS"}}
	v.Set("sign", yishoumi.Sign(v, cfg.YSMSecret))
	req := httptest.NewRequest(http.MethodPost, "/api/payments/yishoumi/notify", strings.NewReader(v.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}
