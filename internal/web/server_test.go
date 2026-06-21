package web

import (
	"context"
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

type verificationMailer struct {
	to, code string
	err      error
}

func (m *verificationMailer) SendVerification(_ context.Context, to, code string) error {
	m.to, m.code = to, code
	return m.err
}

type verificationTurnstile struct {
	token, ip string
	err       error
}

func TestRemoteIPOnlyTrustsForwardedHeaderFromLoopbackProxy(t *testing.T) {
	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.RemoteAddr = "127.0.0.1:1234"
	proxied.Header.Set("X-Forwarded-For", "203.0.113.9, 127.0.0.1")
	if got := remoteIP(proxied); got != "203.0.113.9" {
		t.Fatalf("proxied IP=%q", got)
	}
	direct := httptest.NewRequest(http.MethodGet, "/", nil)
	direct.RemoteAddr = "198.51.100.7:5678"
	direct.Header.Set("X-Forwarded-For", "203.0.113.10")
	if got := remoteIP(direct); got != "198.51.100.7" {
		t.Fatalf("direct IP=%q", got)
	}
}

func (v *verificationTurnstile) Verify(_ context.Context, token, ip string) error {
	v.token, v.ip = token, ip
	return v.err
}

func TestEmailVerifiedRegistrationFlow(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "verified-register.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{BaseURL: "https://example.test", EmailVerificationRequired: true, TurnstileSiteKey: "site-key", TurnstileSecret: "secret"}
	server := New(cfg, db)
	mail := &verificationMailer{}
	challenge := &verificationTurnstile{}
	server.Mailer, server.Turnstile = mail, challenge
	handler := server.Routes()

	configRequest := httptest.NewRequest(http.MethodGet, "/api/auth/config", nil)
	configResult := httptest.NewRecorder()
	handler.ServeHTTP(configResult, configRequest)
	if configResult.Code != http.StatusOK || !strings.Contains(configResult.Body.String(), `"emailVerificationRequired":true`) || !strings.Contains(configResult.Body.String(), "site-key") {
		t.Fatalf("config=%s", configResult.Body.String())
	}

	send := httptest.NewRequest(http.MethodPost, "/api/auth/email-code", strings.NewReader(`{"email":"verified@example.com","turnstileToken":"challenge-token"}`))
	send.Header.Set("content-type", "application/json")
	send.RemoteAddr = "203.0.113.8:4321"
	sendResult := httptest.NewRecorder()
	handler.ServeHTTP(sendResult, send)
	if sendResult.Code != http.StatusOK || mail.to != "verified@example.com" || len(mail.code) != 6 || challenge.token != "challenge-token" || challenge.ip != "203.0.113.8" {
		t.Fatalf("send status=%d body=%s mail=%+v challenge=%+v", sendResult.Code, sendResult.Body.String(), mail, challenge)
	}

	repeat := httptest.NewRequest(http.MethodPost, "/api/auth/email-code", strings.NewReader(`{"email":"verified@example.com","turnstileToken":"challenge-token"}`))
	repeat.Header.Set("content-type", "application/json")
	repeat.RemoteAddr = "203.0.113.8:4321"
	repeatResult := httptest.NewRecorder()
	handler.ServeHTTP(repeatResult, repeat)
	if repeatResult.Code != http.StatusTooManyRequests {
		t.Fatalf("repeat status=%d body=%s", repeatResult.Code, repeatResult.Body.String())
	}

	wrong := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"Email":"verified@example.com","Password":"password123","Code":"000000"}`))
	wrong.Header.Set("content-type", "application/json")
	wrongResult := httptest.NewRecorder()
	handler.ServeHTTP(wrongResult, wrong)
	if wrongResult.Code != http.StatusBadRequest {
		t.Fatalf("wrong code status=%d body=%s", wrongResult.Code, wrongResult.Body.String())
	}

	registerBody := fmt.Sprintf(`{"Email":"verified@example.com","Password":"password123","Code":"%s"}`, mail.code)
	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(registerBody))
	register.Header.Set("content-type", "application/json")
	registerResult := httptest.NewRecorder()
	handler.ServeHTTP(registerResult, register)
	if registerResult.Code != http.StatusCreated || len(registerResult.Result().Cookies()) != 1 {
		t.Fatalf("register status=%d body=%s", registerResult.Code, registerResult.Body.String())
	}

	reuse := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(registerBody))
	reuse.Header.Set("content-type", "application/json")
	reuseResult := httptest.NewRecorder()
	handler.ServeHTTP(reuseResult, reuse)
	if reuseResult.Code != http.StatusBadRequest {
		t.Fatalf("reuse status=%d body=%s", reuseResult.Code, reuseResult.Body.String())
	}
}

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

func TestAdminOverviewSettingsAndSupportFlow(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, userToken, err := db.Register("customer@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get-balance" {
			_, _ = w.Write([]byte(`{"balance":99.5}`))
			return
		}
		if r.URL.Query().Get("action") == "getBalance" {
			_, _ = w.Write([]byte("ACCESS_BALANCE:7.25"))
			return
		}
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer upstream.Close()
	cfg := config.Config{BaseURL: "https://example.test", HeroKey: "hero", HeroURL: upstream.URL, HeroCurrency: "840", SMSManToken: "smsman", SMSManURL: upstream.URL, AdminEmail: "admin@example.com", AdminPassword: "strong-admin-password"}
	handler := New(cfg, db).Routes()

	userCookie := &http.Cookie{Name: "session", Value: userToken}
	send := httptest.NewRequest(http.MethodPost, "/api/support", strings.NewReader(`{"body":"Need help"}`))
	send.Header.Set("content-type", "application/json")
	send.AddCookie(userCookie)
	sendResult := httptest.NewRecorder()
	handler.ServeHTTP(sendResult, send)
	if sendResult.Code != http.StatusCreated {
		t.Fatalf("support status=%d body=%s", sendResult.Code, sendResult.Body.String())
	}

	login := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"Email":"admin@example.com","Password":"strong-admin-password"}`))
	login.Header.Set("content-type", "application/json")
	loginResult := httptest.NewRecorder()
	handler.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResult.Code, loginResult.Body.String())
	}
	adminCookie := loginResult.Result().Cookies()[0]

	overview := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	overview.AddCookie(adminCookie)
	overviewResult := httptest.NewRecorder()
	handler.ServeHTTP(overviewResult, overview)
	if overviewResult.Code != http.StatusOK || !strings.Contains(overviewResult.Body.String(), `"amount":7.25`) || !strings.Contains(overviewResult.Body.String(), `"amount":99.5`) {
		t.Fatalf("overview status=%d body=%s", overviewResult.Code, overviewResult.Body.String())
	}

	chats := httptest.NewRequest(http.MethodGet, "/api/admin/chats", nil)
	chats.AddCookie(adminCookie)
	chatsResult := httptest.NewRecorder()
	handler.ServeHTTP(chatsResult, chats)
	if chatsResult.Code != http.StatusOK || !strings.Contains(chatsResult.Body.String(), "customer@example.com") {
		t.Fatalf("chats=%s", chatsResult.Body.String())
	}

	reply := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/chats/%d", user.ID), strings.NewReader(`{"body":"We are here"}`))
	reply.Header.Set("content-type", "application/json")
	reply.AddCookie(adminCookie)
	replyResult := httptest.NewRecorder()
	handler.ServeHTTP(replyResult, reply)
	if replyResult.Code != http.StatusCreated {
		t.Fatalf("reply status=%d body=%s", replyResult.Code, replyResult.Body.String())
	}

	settings := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"contactTitle":"Telegram","contactValue":"@cloudsms","contactURL":"https://t.me/cloudsms","supportHours":"24/7"}`))
	settings.Header.Set("content-type", "application/json")
	settings.AddCookie(adminCookie)
	settingsResult := httptest.NewRecorder()
	handler.ServeHTTP(settingsResult, settings)
	if settingsResult.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", settingsResult.Code, settingsResult.Body.String())
	}

	public := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	publicResult := httptest.NewRecorder()
	handler.ServeHTTP(publicResult, public)
	if publicResult.Code != http.StatusOK || !strings.Contains(publicResult.Body.String(), "@cloudsms") {
		t.Fatalf("public settings=%s", publicResult.Body.String())
	}

	messages := httptest.NewRequest(http.MethodGet, "/api/support", nil)
	messages.AddCookie(userCookie)
	messagesResult := httptest.NewRecorder()
	handler.ServeHTTP(messagesResult, messages)
	if messagesResult.Code != http.StatusOK || !strings.Contains(messagesResult.Body.String(), "We are here") {
		t.Fatalf("messages=%s", messagesResult.Body.String())
	}

	pricingUpdate := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"markupCNY":"2.5","usdCnyRate":"7.4","smsmanCnyRate":"0.09"}`))
	pricingUpdate.Header.Set("content-type", "application/json")
	pricingUpdate.AddCookie(adminCookie)
	pricingResult := httptest.NewRecorder()
	handler.ServeHTTP(pricingResult, pricingUpdate)
	if pricingResult.Code != http.StatusOK || !strings.Contains(pricingResult.Body.String(), `"markupCNY":"2.5"`) {
		t.Fatalf("pricing settings status=%d body=%s", pricingResult.Code, pricingResult.Body.String())
	}

	createAnnouncement := httptest.NewRequest(http.MethodPost, "/api/admin/announcements", strings.NewReader(`{"title":"Maintenance","body":"Tonight at 23:00","active":true}`))
	createAnnouncement.Header.Set("content-type", "application/json")
	createAnnouncement.AddCookie(adminCookie)
	announcementResult := httptest.NewRecorder()
	handler.ServeHTTP(announcementResult, createAnnouncement)
	if announcementResult.Code != http.StatusCreated {
		t.Fatalf("announcement status=%d body=%s", announcementResult.Code, announcementResult.Body.String())
	}
	publicAnnouncements := httptest.NewRequest(http.MethodGet, "/api/announcements", nil)
	publicAnnouncementsResult := httptest.NewRecorder()
	handler.ServeHTTP(publicAnnouncementsResult, publicAnnouncements)
	if publicAnnouncementsResult.Code != http.StatusOK || !strings.Contains(publicAnnouncementsResult.Body.String(), "Maintenance") {
		t.Fatalf("public announcements=%s", publicAnnouncementsResult.Body.String())
	}

	users := httptest.NewRequest(http.MethodGet, "/api/admin/users?q=customer", nil)
	users.AddCookie(adminCookie)
	usersResult := httptest.NewRecorder()
	handler.ServeHTTP(usersResult, users)
	if usersResult.Code != http.StatusOK || !strings.Contains(usersResult.Body.String(), "customer@example.com") {
		t.Fatalf("users=%s", usersResult.Body.String())
	}
	disable := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/users/%d", user.ID), strings.NewReader(`{"disabled":true}`))
	disable.Header.Set("content-type", "application/json")
	disable.AddCookie(adminCookie)
	disableResult := httptest.NewRecorder()
	handler.ServeHTTP(disableResult, disable)
	if disableResult.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disableResult.Code, disableResult.Body.String())
	}
	disabledRequest := httptest.NewRequest(http.MethodGet, "/api/support", nil)
	disabledRequest.AddCookie(userCookie)
	disabledResult := httptest.NewRecorder()
	handler.ServeHTTP(disabledResult, disabledRequest)
	if disabledResult.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session status=%d", disabledResult.Code)
	}

	for _, path := range []string{"/api/admin/orders", "/api/admin/payments", "/api/admin/audit"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(adminCookie)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, result.Code, result.Body.String())
		}
	}
}

func TestAdminCanConfigureSMTPWithoutReadingPasswordBack(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "smtp-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{TurnstileSiteKey: "site-key", TurnstileSecret: "secret", SMTPPort: 587}
	handler := New(cfg, db).Routes()
	token, _ := store.Token()
	if err = db.CreateAdminSession(token); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "admin_session", Value: token}

	update := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"smtpHost":"smtp.example.com","smtpPort":"465","smtpUser":"mailer@example.com","smtpPassword":"smtp-secret-value","smtpFrom":"云码台 <no-reply@example.com>","emailVerificationRequired":"true"}`))
	update.Header.Set("content-type", "application/json")
	update.AddCookie(cookie)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, update)
	if result.Code != http.StatusOK {
		t.Fatalf("SMTP settings status=%d body=%s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "smtp-secret-value") || !strings.Contains(result.Body.String(), `"smtpPasswordConfigured":true`) {
		t.Fatalf("SMTP password leaked or status missing: %s", result.Body.String())
	}
	values, err := db.Settings()
	if err != nil || values["smtpPassword"] != "smtp-secret-value" {
		t.Fatalf("SMTP password was not stored: err=%v configured=%v", err, values["smtpPassword"] != "")
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/api/auth/config", nil)
	configResult := httptest.NewRecorder()
	handler.ServeHTTP(configResult, configRequest)
	if configResult.Code != http.StatusOK || !strings.Contains(configResult.Body.String(), `"emailVerificationRequired":true`) {
		t.Fatalf("auth config did not update immediately: %s", configResult.Body.String())
	}

	unchanged := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"smtpHost":"smtp2.example.com","smtpPort":"587","smtpUser":"mailer@example.com","smtpPassword":"","smtpFrom":"no-reply@example.com","emailVerificationRequired":"true"}`))
	unchanged.Header.Set("content-type", "application/json")
	unchanged.AddCookie(cookie)
	unchangedResult := httptest.NewRecorder()
	handler.ServeHTTP(unchangedResult, unchanged)
	values, _ = db.Settings()
	if unchangedResult.Code != http.StatusOK || values["smtpPassword"] != "smtp-secret-value" {
		t.Fatalf("blank SMTP password unexpectedly replaced the saved password")
	}
}

func TestAutoReplaceDoesNotAcquireWithoutCancellationConfirmation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "cancel-confirmation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("cancel-confirmation@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := store.SMSOrder{ID: "SCANCELCONFIRM", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMS(o.ID, "old-id", "10001", .5); err != nil {
		t.Fatal(err)
	}
	acquireCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getStatus":
			_, _ = w.Write([]byte("STATUS_WAIT_CODE"))
		case "setStatus":
			_, _ = w.Write([]byte("ACCESS_ACTIVATION"))
		case "getNumberV2":
			acquireCalls++
			_, _ = w.Write([]byte(`{"activationId":"must-not-exist","phoneNumber":"10002","activationCost":0.5}`))
		}
	}))
	defer upstream.Close()
	s := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840"}, db)
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
	if acquireCalls != 0 || got.UpstreamID != "old-id" || got.Status != "waiting" {
		t.Fatalf("acquireCalls=%d order=%+v", acquireCalls, got)
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

func TestProfileReturnsCurrentUserSummaryAndOrderHistory(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "profile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, token, err := db.Register("profile@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	order := store.SMSOrder{ID: "profile-order", Country: "6", Service: "tg", PriceFen: 250, CreatedAt: now}
	payment := store.Recharge{ID: "profile-payment", AmountFen: 250, Provider: "sandbox", PayType: "2", Token: "token", CreatedAt: now}
	if err = db.CreateSMSPayment(user, order, payment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CompleteSMSPayment(payment.ID, "provider-1"); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateSMS(order.ID, "code_received", "123456"); err != nil {
		t.Fatal(err)
	}

	handler := New(config.Config{}, db).Routes()
	request := httptest.NewRequest(http.MethodGet, "/api/profile", nil)
	request.AddCookie(&http.Cookie{Name: "session", Value: token})
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", result.Code, result.Body.String())
	}
	var response struct {
		Profile store.UserProfile `json:"profile"`
		Orders  []store.SMSOrder  `json:"orders"`
	}
	if err = json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Profile.Email != "profile@example.com" || response.Profile.OrdersTotal != 1 || response.Profile.OrdersSuccessful != 1 || response.Profile.SpentFen != 250 {
		t.Fatalf("unexpected profile: %+v", response.Profile)
	}
	if len(response.Orders) != 1 || response.Orders[0].ID != order.ID {
		t.Fatalf("unexpected order history: %+v", response.Orders)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/profile", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized profile status=%d", unauthorized.Code)
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
		case "getCountries":
			_, _ = w.Write([]byte(`{"6":{"id":6,"eng":"Indonesia","chn":"印度尼西亚"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"tg","name":"Telegram"}]}`))
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

func TestAggregatedQuoteSelectsSMSManAndRoutesPaidOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aggregate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	heroUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"2":{"id":2,"eng":"Kazakhstan","chn":"哈萨克斯坦"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"tg","name":"Telegram"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"2":{"tg":{"cost":1,"count":5}}}`))
		default:
			http.Error(w, "HeroSMS should not acquire the more expensive number", http.StatusBadRequest)
		}
	}))
	defer heroUpstream.Close()
	smsActions := []string{}
	smsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		smsActions = append(smsActions, r.URL.Path)
		switch r.URL.Path {
		case "/countries":
			_, _ = w.Write([]byte(`{"99":{"id":99,"name":"Kazakhstan"}}`))
		case "/applications":
			_, _ = w.Write([]byte(`{"88":{"id":88,"name":"Telegram"}}`))
		case "/get-prices":
			_, _ = w.Write([]byte(`{"88":{"price":50,"count":4}}`))
		case "/get-number":
			if r.URL.Query().Get("country_id") != "99" || r.URL.Query().Get("application_id") != "88" {
				t.Fatal("wrong SMS-Man route")
			}
			_, _ = w.Write([]byte(`{"request_id":4321,"number":"77000000001"}`))
		case "/get-sms":
			_, _ = w.Write([]byte(`{"sms_code":"246810"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer smsUpstream.Close()
	cfg := config.Config{HeroKey: "hero", HeroURL: heroUpstream.URL, HeroCurrency: "840", SMSManToken: "smsman", SMSManURL: smsUpstream.URL, SMSManCNYRate: .08, USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", AllowLiveSMSInSandbox: true}
	server := New(cfg, db)
	h := server.Routes()

	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"Email":"aggregate@example.com","Password":"password123"}`))
	register.Header.Set("content-type", "application/json")
	registerResult := httptest.NewRecorder()
	h.ServeHTTP(registerResult, register)
	session := registerResult.Result().Cookies()[0]
	purchase := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"2","Service":"tg","payType":2}`))
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
	if checkout.PriceFen != 500 {
		t.Fatalf("aggregated price=%d, want 500", checkout.PriceFen)
	}
	pending, err := db.GetSMSByID(checkout.ID)
	if err != nil || pending.UpstreamProvider != "smsman" || pending.UpstreamCountry != "99" || pending.UpstreamService != "88" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	checkoutURL, _ := url.Parse(checkout.URL)
	pay := httptest.NewRequest(http.MethodPost, checkoutURL.Path, strings.NewReader(url.Values{"token": {checkoutURL.Query().Get("token")}}.Encode()))
	pay.Header.Set("content-type", "application/x-www-form-urlencoded")
	payResult := httptest.NewRecorder()
	h.ServeHTTP(payResult, pay)
	if payResult.Code != http.StatusSeeOther {
		t.Fatalf("pay status=%d body=%s", payResult.Code, payResult.Body.String())
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/orders/"+checkout.ID, nil)
	statusRequest.AddCookie(session)
	statusResult := httptest.NewRecorder()
	h.ServeHTTP(statusResult, statusRequest)
	got, err := db.GetSMSByID(checkout.ID)
	if err != nil || got.Phone != "77000000001" || got.Code != "246810" || got.Status != "code_received" {
		t.Fatalf("order=%+v err=%v actions=%v", got, err, smsActions)
	}
	if _, err = server.loadCatalog(t.Context(), "2"); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/countries", "/applications", "/get-prices"} {
		calls := 0
		for _, action := range smsActions {
			if action == endpoint {
				calls++
			}
		}
		if calls != 1 {
			t.Fatalf("%s calls=%d, want cached single call; actions=%v", endpoint, calls, smsActions)
		}
	}
}

func TestGlobalCheapestQuotePurchasesItsCountryWithoutCountrySelection(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "global-cheapest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"6":{"id":6,"eng":"Indonesia","chn":"印度尼西亚"},"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"tg","name":"Telegram"}]}`))
		case "getPrices":
			if r.URL.Query().Get("country") != "" {
				t.Fatalf("expected global prices request")
			}
			_, _ = w.Write([]byte(`{"6":{"tg":{"cost":0.5,"count":2}},"7":{"tg":{"cost":0.2,"count":4}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := New(config.Config{HeroKey: "hero", HeroURL: upstream.URL, HeroCurrency: "840", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", AllowLiveSMSInSandbox: true}, db)
	handler := server.Routes()
	_, session, err := db.Register("global@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"","Service":"tg","payType":2}`))
	request.Header.Set("content-type", "application/json")
	request.AddCookie(&http.Cookie{Name: "session", Value: session})
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusCreated {
		t.Fatalf("purchase status=%d body=%s", result.Code, result.Body.String())
	}
	var checkout struct {
		ID       string `json:"id"`
		PriceFen int64  `json:"priceFen"`
	}
	if err = json.NewDecoder(result.Body).Decode(&checkout); err != nil {
		t.Fatal(err)
	}
	order, err := db.GetSMSByID(checkout.ID)
	if err != nil || order.Country != "7" || order.UpstreamCountry != "7" || checkout.PriceFen != 245 {
		t.Fatalf("order=%+v price=%d err=%v", order, checkout.PriceFen, err)
	}
}

func TestSMSManAutoReplaceRejectsBeforeAcquire(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "smsman-replace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("smsman-replace@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := store.SMSOrder{ID: "SSMSMANREPLACE", UserID: u.ID, UpstreamProvider: "smsman", UpstreamCountry: "99", UpstreamService: "88", Country: "2", Service: "tg", UpstreamCost: 50, PriceFen: 500, AutoReplace: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMSWithProvider(o.ID, "smsman", "old-request", "77000000001", 50); err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	smsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions = append(actions, r.URL.Path)
		switch r.URL.Path {
		case "/get-sms":
			_, _ = w.Write([]byte(`{"error_code":"wait_sms","error_msg":"Waiting"}`))
		case "/set-status":
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/get-number":
			_, _ = w.Write([]byte(`{"request_id":9876,"number":"77000000002"}`))
		}
	}))
	defer smsUpstream.Close()
	s := New(config.Config{HeroKey: "hero", SMSManToken: "smsman", SMSManURL: smsUpstream.URL, SMSManCNYRate: .08}, db)
	current, _ := db.GetSMSByID(o.ID)
	claimed, err := db.ClaimAutoReplace(o.ID, current.UpstreamID)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	s.replaceNumber(t.Context(), current)
	got, err := db.GetSMSByID(o.ID)
	if err != nil || got.UpstreamID != "9876" || got.Phone != "77000000002" || got.ReplaceAttempts != 1 {
		t.Fatalf("order=%+v err=%v", got, err)
	}
	if strings.Join(actions, ",") != "/get-sms,/set-status,/get-number" {
		t.Fatalf("actions=%v", actions)
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

func TestPublicSEOAndFooterAssets(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "seo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(config.Config{}, db).Routes()
	tests := []struct {
		path  string
		wants []string
	}{
		{"/", []string{`rel="canonical" href="https://35-212-227-118.sslip.io/"`, `href="/contact.html"`, `href="/api.html"`, `application/ld+json`}},
		{"/contact.html", []string{"联系我们", `rel="canonical"`}},
		{"/api.html", []string{"API 接入", "合作 API"}},
		{"/robots.txt", []string{"User-agent: *", "Sitemap: https://35-212-227-118.sslip.io/sitemap.xml"}},
		{"/sitemap.xml", []string{"<urlset", "https://35-212-227-118.sslip.io/contact.html", "https://35-212-227-118.sslip.io/api.html"}},
	}
	for _, test := range tests {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, req)
		if result.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", test.path, result.Code)
		}
		for _, want := range test.wants {
			if !strings.Contains(result.Body.String(), want) {
				t.Fatalf("GET %s missing %q", test.path, want)
			}
		}
	}
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
