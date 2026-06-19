package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sms-platform/internal/config"
	"sms-platform/internal/epay"
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

func TestPayForSMSOrderThenAcquireEndToEnd(t *testing.T) {
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

	purchase := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"6","Service":"tg","payType":2}`))
	purchase.Header.Set("content-type", "application/json")
	purchase.AddCookie(session)
	purchaseResult := httptest.NewRecorder()
	h.ServeHTTP(purchaseResult, purchase)
	if purchaseResult.Code != http.StatusCreated {
		t.Fatalf("purchase status=%d body=%s", purchaseResult.Code, purchaseResult.Body.String())
	}
	var checkout struct {
		ID       string `json:"id"`
		PriceFen int64  `json:"priceFen"`
		URL      string `json:"checkoutUrl"`
	}
	if err = json.NewDecoder(purchaseResult.Body).Decode(&checkout); err != nil {
		t.Fatal(err)
	}
	if checkout.PriceFen != 460 {
		t.Fatalf("price=%d, want 460", checkout.PriceFen)
	}
	pending, err := db.GetSMS(checkout.ID, 1)
	if err != nil || pending.Status != "awaiting_payment" || pending.Phone != "" {
		t.Fatalf("order before payment=%+v err=%v", pending, err)
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

	order, err := db.GetSMS(checkout.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if order.Phone != "628001" || order.PriceFen != 460 || !order.AutoReplace {
		t.Fatalf("unexpected order: %+v", order)
	}
	var balance int64
	if err = db.DB.QueryRow("SELECT balance_fen FROM users WHERE email=?", "flow@example.com").Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("balance=%d, want unchanged balance 0", balance)
	}
}

func TestYishoumiSMSPaymentCallbackAcquiresOnlyOnce(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "direct-payment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("direct@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{ID: "SDIRECT", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: true, CreatedAt: now}
	payment := store.Recharge{ID: "PDIRECT", UserID: u.ID, AmountFen: 460, Provider: "yishoumi", PayType: "2", Token: "token", Reference: o.ID, CreatedAt: now}
	if err = db.CreateSMSPayment(u, o, payment); err != nil {
		t.Fatal(err)
	}
	acquireCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "getNumberV2" {
			http.Error(w, "unexpected action", http.StatusBadRequest)
			return
		}
		acquireCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "paid-activation", "phoneNumber": "628002", "activationCost": .5})
	}))
	defer upstream.Close()
	cfg := config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840", PayProvider: "yishoumi", YSMAppID: "app-1", YSMSecret: "secret"}
	h := New(cfg, db).Routes()
	values := url.Values{"appid": {"app-1"}, "mch_orderid": {payment.ID}, "total_fee": {"460"}, "ysm_orderid": {"ysm-direct-1"}, "state": {"SUCCESS"}}
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
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acquireCalls != 1 || got.Phone != "628002" || got.Status != "waiting" {
		t.Fatalf("acquireCalls=%d order=%+v", acquireCalls, got)
	}
	var balance int64
	if err = db.DB.QueryRow("SELECT balance_fen FROM users WHERE id=?", u.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("direct payment unexpectedly credited balance: %d", balance)
	}
}

func TestEPaySMSPaymentCallbackVerifiesAndAcquiresOnlyOnce(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "epay-payment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("epay@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{ID: "SEPAY", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: true, CreatedAt: now}
	payment := store.Recharge{ID: "PEPAY", UserID: u.ID, AmountFen: 460, Provider: "epay", PayType: "2", Token: "token", Reference: o.ID, CreatedAt: now}
	if err = db.CreateSMSPayment(u, o, payment); err != nil {
		t.Fatal(err)
	}
	acquireCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "getNumberV2" {
			http.Error(w, "unexpected action", http.StatusBadRequest)
			return
		}
		acquireCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "epay-activation", "phoneNumber": "628003", "activationCost": .5})
	}))
	defer upstream.Close()
	cfg := config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840", PayProvider: "epay", EPayPID: "1000", EPayKey: "secret", EPayURL: "https://50pay.example"}
	h := New(cfg, db).Routes()
	values := url.Values{"pid": {"1000"}, "out_trade_no": {payment.ID}, "trade_no": {"50pay-trade-1"}, "trade_status": {"TRADE_SUCCESS"}, "type": {"wxpay"}, "money": {"4.60"}, "sign_type": {"MD5"}}
	values.Set("sign", epay.Sign(values, cfg.EPayKey))

	wrongAmount := cloneValues(values)
	wrongAmount.Set("money", "4.59")
	wrongAmount.Set("sign", epay.Sign(wrongAmount, cfg.EPayKey))
	badRequest := httptest.NewRequest(http.MethodGet, "/api/payments/epay/notify?"+wrongAmount.Encode(), nil)
	badResult := httptest.NewRecorder()
	h.ServeHTTP(badResult, badRequest)
	if badResult.Code != http.StatusBadRequest {
		t.Fatalf("wrong amount status=%d, want 400", badResult.Code)
	}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/payments/epay/notify?"+values.Encode(), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "success" {
			t.Fatalf("callback %d: status=%d body=%q", i, w.Code, w.Body.String())
		}
	}
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acquireCalls != 1 || got.Phone != "628003" || got.Status != "waiting" {
		t.Fatalf("acquireCalls=%d order=%+v", acquireCalls, got)
	}
}

func cloneValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
}

func TestUnlimitedAutoReplaceCancelsBeforeEveryAcquireAndStopsOnCode(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "unlimited-replace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("unlimited@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := store.SMSOrder{ID: "SUNLIMITED", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMS(o.ID, "activation-0", "10000", .5); err != nil {
		t.Fatal(err)
	}
	var actions []string
	statusCalls, acquireCalls := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		actions = append(actions, action)
		switch action {
		case "getStatus":
			statusCalls++
			if statusCalls == 3 {
				w.Write([]byte("STATUS_OK:654321"))
			} else {
				w.Write([]byte("STATUS_WAIT_CODE"))
			}
		case "setStatus":
			w.Write([]byte("ACCESS_CANCEL"))
		case "getNumberV2":
			acquireCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"activationId": fmt.Sprintf("activation-%d", acquireCalls), "phoneNumber": fmt.Sprintf("1000%d", acquireCalls), "activationCost": .5})
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	s := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840", AutoReplaceAfter: 2 * time.Minute, AutoReplaceMax: 0}, db)
	for i := 0; i < 3; i++ {
		if _, err = db.DB.Exec("UPDATE sms_orders SET last_number_at=? WHERE id=?", time.Now().Add(-3*time.Minute).UTC().Format(time.RFC3339), o.ID); err != nil {
			t.Fatal(err)
		}
		s.runAutoReplaceBatch(t.Context())
	}
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantActions := "getStatus,setStatus,getNumberV2,getStatus,setStatus,getNumberV2,getStatus"
	if strings.Join(actions, ",") != wantActions {
		t.Fatalf("actions=%v, want %s", actions, wantActions)
	}
	if got.Code != "654321" || got.Status != "code_received" || got.ReplaceAttempts != 2 {
		t.Fatalf("unexpected final order: %+v", got)
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

func TestYishoumiNotifyRejectsWrongAmount(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, _ := db.Register("wrong@example.com", "password123")
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{ID: "SWRONG", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 500, AutoReplace: true, CreatedAt: now}
	r := store.Recharge{ID: "PWRONG", UserID: u.ID, AmountFen: 500, Provider: "yishoumi", PayType: "2", Token: "token", Reference: o.ID, CreatedAt: now}
	if err = db.CreateSMSPayment(u, o, r); err != nil {
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
