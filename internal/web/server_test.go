package web

import (
	"encoding/json"
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

func TestAutoReplaceCancelsThenAcquiresWithoutChargingAgain(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "replace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("replace@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := store.SMSOrder{ID: "SREPLACE", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMS(o.ID, "old-id", "10001", .5); err != nil {
		t.Fatal(err)
	}
	var actions []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		actions = append(actions, action)
		switch action {
		case "getStatus":
			w.Write([]byte("STATUS_WAIT_CODE"))
		case "setStatus":
			w.Write([]byte("ACCESS_CANCEL"))
		case "getNumberV2":
			_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "new-id", "phoneNumber": "10002", "activationCost": .5})
		default:
			http.Error(w, "unexpected", 400)
		}
	}))
	defer upstream.Close()
	s := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840", AutoReplaceMax: 2}, db)
	current, _ := db.GetSMS(o.ID, u.ID)
	claimed, err := db.ClaimAutoReplace(o.ID, current.UpstreamID)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	s.replaceNumber(t.Context(), current)
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamID != "new-id" || got.Phone != "10002" || got.ReplaceAttempts != 1 || got.Status != "waiting" {
		t.Fatalf("unexpected replacement: %+v", got)
	}
	var balance int64
	if err = db.DB.QueryRow("SELECT balance_fen FROM users WHERE id=?", u.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 540 {
		t.Fatalf("balance=%d, want 540", balance)
	}
	want := []string{"getStatus", "setStatus", "getNumberV2"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions=%v, want %v", actions, want)
	}
}

func TestAuthenticatedRequestRefreshesPersistentSessionCookie(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, token, err := db.Register("cookie@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	h := New(config.Config{PayProvider: "sandbox"}, db).Routes()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].HttpOnly != true || cookies[0].MaxAge != 30*86400 {
		t.Fatalf("unexpected session cookies: %+v", cookies)
	}
}

func TestRechargeThenPurchaseEndToEnd(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getPrices":
			w.Write([]byte(`{"6":{"tg":{"cost":0.5,"count":3}}}`))
		case "getNumberV2":
			_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "flow-activation", "phoneNumber": "628001", "activationCost": .5})
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	h := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", AllowLiveSMSInSandbox: true}, db).Routes()

	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"Email":"flow@example.com","Password":"password123"}`))
	register.Header.Set("content-type", "application/json")
	registerResult := httptest.NewRecorder()
	h.ServeHTTP(registerResult, register)
	if registerResult.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", registerResult.Code, registerResult.Body.String())
	}
	session := registerResult.Result().Cookies()[0]

	recharge := httptest.NewRequest(http.MethodPost, "/api/recharges", strings.NewReader(`{"amountFen":500,"payType":2}`))
	recharge.Header.Set("content-type", "application/json")
	recharge.AddCookie(session)
	rechargeResult := httptest.NewRecorder()
	h.ServeHTTP(rechargeResult, recharge)
	if rechargeResult.Code != http.StatusCreated {
		t.Fatalf("recharge status=%d body=%s", rechargeResult.Code, rechargeResult.Body.String())
	}
	var checkout struct {
		URL string `json:"checkoutUrl"`
	}
	if err = json.NewDecoder(rechargeResult.Body).Decode(&checkout); err != nil {
		t.Fatal(err)
	}
	checkoutURL, err := url.Parse(checkout.URL)
	if err != nil {
		t.Fatal(err)
	}
	payForm := url.Values{"token": {checkoutURL.Query().Get("token")}}
	pay := httptest.NewRequest(http.MethodPost, checkoutURL.Path, strings.NewReader(payForm.Encode()))
	pay.Header.Set("content-type", "application/x-www-form-urlencoded")
	payResult := httptest.NewRecorder()
	h.ServeHTTP(payResult, pay)
	if payResult.Code != http.StatusSeeOther {
		t.Fatalf("pay status=%d body=%s", payResult.Code, payResult.Body.String())
	}

	purchase := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"6","Service":"tg","autoReplace":true}`))
	purchase.Header.Set("content-type", "application/json")
	purchase.AddCookie(session)
	purchaseResult := httptest.NewRecorder()
	h.ServeHTTP(purchaseResult, purchase)
	if purchaseResult.Code != http.StatusCreated {
		t.Fatalf("purchase status=%d body=%s", purchaseResult.Code, purchaseResult.Body.String())
	}
	var order store.SMSOrder
	if err = json.NewDecoder(purchaseResult.Body).Decode(&order); err != nil {
		t.Fatal(err)
	}
	if order.Phone != "628001" || order.PriceFen != 460 || !order.AutoReplace {
		t.Fatalf("unexpected order: %+v", order)
	}
	var balance int64
	if err = db.DB.QueryRow("SELECT balance_fen FROM users WHERE email=?", "flow@example.com").Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 40 {
		t.Fatalf("balance=%d, want 40", balance)
	}
}

func TestAutoReplaceRecoversAfterUpstreamWasCancelled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "recover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("recover@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := store.SMSOrder{ID: "SRECOVER", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMS(o.ID, "cancelled-id", "10001", .5); err != nil {
		t.Fatal(err)
	}
	if claimed, e := db.ClaimAutoReplace(o.ID, "cancelled-id"); e != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, e)
	}
	setStatusCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getStatus":
			w.Write([]byte("STATUS_CANCEL"))
		case "getNumberV2":
			_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "recovered-id", "phoneNumber": "10002", "activationCost": .5})
		case "setStatus":
			setStatusCalls++
			w.Write([]byte("ACCESS_CANCEL"))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	s := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840", AutoReplaceMax: 2}, db)
	stuck, err := db.ListReplacing(20)
	if err != nil || len(stuck) != 1 {
		t.Fatalf("replacing=%v err=%v", stuck, err)
	}
	s.replaceNumber(t.Context(), stuck[0])
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamID != "recovered-id" || got.Status != "waiting" || got.ReplaceAttempts != 1 {
		t.Fatalf("unexpected recovered order: %+v", got)
	}
	if setStatusCalls != 0 {
		t.Fatalf("cancel called %d times after upstream was already cancelled", setStatusCalls)
	}
}

func TestYishoumiNotifyRejectsWhenProviderIsDisabled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "disabled-provider.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/payments/yishoumi/notify", strings.NewReader("state=SUCCESS"))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	New(config.Config{PayProvider: "sandbox"}, db).Routes().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

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
