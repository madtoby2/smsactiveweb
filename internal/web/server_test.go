package web

import (
	"context"
	"crypto"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
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
	cfg := config.Config{
		BaseURL:                   "https://example.test",
		EmailVerificationRequired: true,
		TurnstileSiteKey:          "site-key",
		TurnstileSecret:           "secret",
		ResendAPIKey:              "re_test_key",
		ResendFrom:                "onboarding@resend.dev",
	}
	server := New(cfg, db)
	mail := &verificationMailer{}
	challenge := &verificationTurnstile{}
	server.Mailer, server.Turnstile = mail, challenge
	handler := server.Routes()

	configRequest := httptest.NewRequest(http.MethodGet, "/api/auth/config", nil)
	configResult := httptest.NewRecorder()
	handler.ServeHTTP(configResult, configRequest)
	if configResult.Code != http.StatusOK || !strings.Contains(configResult.Body.String(), `"emailVerificationRequired":true`) || !strings.Contains(configResult.Body.String(), `"emailVerificationAvailable":true`) || !strings.Contains(configResult.Body.String(), "site-key") {
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

func TestRegisterFallsBackToTurnstileWhenEmailChannelUnavailable(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "turnstile-register.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{
		BaseURL:                   "https://example.test",
		EmailVerificationRequired: true,
		TurnstileSiteKey:          "site-key",
		TurnstileSecret:           "secret",
	}
	server := New(cfg, db)
	challenge := &verificationTurnstile{}
	server.Turnstile = challenge
	handler := server.Routes()

	configRequest := httptest.NewRequest(http.MethodGet, "/api/auth/config", nil)
	configResult := httptest.NewRecorder()
	handler.ServeHTTP(configResult, configRequest)
	if configResult.Code != http.StatusOK || !strings.Contains(configResult.Body.String(), `"emailVerificationRequired":true`) || !strings.Contains(configResult.Body.String(), `"emailVerificationAvailable":false`) {
		t.Fatalf("config=%s", configResult.Body.String())
	}

	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"Email":"turnstile@example.com","Password":"password123","TurnstileToken":"challenge-token"}`))
	register.Header.Set("content-type", "application/json")
	register.RemoteAddr = "203.0.113.9:4567"
	registerResult := httptest.NewRecorder()
	handler.ServeHTTP(registerResult, register)
	if registerResult.Code != http.StatusCreated || len(registerResult.Result().Cookies()) != 1 || challenge.token != "challenge-token" || challenge.ip != "203.0.113.9" {
		t.Fatalf("register status=%d body=%s challenge=%+v", registerResult.Code, registerResult.Body.String(), challenge)
	}
}

func TestCatalogIsPubliclyAccessible(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "catalog-public.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := New(config.Config{BaseURL: "https://example.test"}, db)
	handler := server.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "\"countries\"") || !strings.Contains(res.Body.String(), "\"services\"") {
		t.Fatalf("catalog response=%s", res.Body.String())
	}
}

func TestPublicCatalogReturnsFullCountryServiceMatrix(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "catalog-matrix.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"},"9":{"id":9,"eng":"Thailand","chn":"泰国"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"tg","name":"Telegram"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"7":{"tg":{"cost":0.2,"count":4,"physicalCount":4}},"9":{"tg":{"cost":0.3,"count":6,"physicalCount":6}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := New(config.Config{HeroKey: "hero", HeroURL: upstream.URL, HeroCurrency: "840", USDCNY: 7.2, Markup: 1}, db)
	handler := server.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Offers []struct {
			Service string `json:"service"`
			Country string `json:"country"`
			Count   int    `json:"count"`
		} `json:"offers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Offers) != 2 {
		t.Fatalf("offers=%+v, want 2 entries for the same service across two countries", payload.Offers)
	}
	countries := map[string]int{}
	for _, offer := range payload.Offers {
		if offer.Service != "tg" {
			t.Fatalf("unexpected service in offers: %+v", payload.Offers)
		}
		countries[offer.Country] = offer.Count
	}
	if countries["7"] != 4 || countries["9"] != 6 {
		t.Fatalf("countries=%v, want map[7:4 9:6]", countries)
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

func TestManualReplaceCancelsThenAcquiresNewNumber(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "manual-replace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, token, err := db.Register("manualreplace@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := store.SMSOrder{ID: "SMANUALREPLACE", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, AutoReplace: false, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = db.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMS(o.ID, "old-id", "10001", .5); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE sms_orders SET last_number_at=? WHERE id=?", time.Now().UTC().Add(-3*time.Minute).Format(time.RFC3339), o.ID); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "setStatus":
			w.Write([]byte("ACCESS_CANCEL"))
		case "getNumberV2":
			_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "new-id", "phoneNumber": "10002", "activationCost": .5})
		default:
			http.Error(w, "unexpected", 400)
		}
	}))
	defer upstream.Close()
	s := New(config.Config{BaseURL: "https://example.test", HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840"}, db)
	h := s.Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/orders/"+o.ID+"/replace", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	result := httptest.NewRecorder()
	h.ServeHTTP(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", result.Code, result.Body.String())
	}
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamID != "new-id" || got.Phone != "10002" || got.ReplaceAttempts != 1 || got.Status != "waiting" {
		t.Fatalf("unexpected replacement: %+v", got)
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

	update := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"smtpHost":"smtp.example.com","smtpPort":"465","smtpUser":"mailer@example.com","smtpPassword":"smtp-secret-value","smtpFrom":"浜戠爜鍙?<no-reply@example.com>","emailVerificationRequired":"true"}`))
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

func TestAdminCanConfigureResendWithoutReadingKeyBack(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "resend-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{TurnstileSiteKey: "site-key", TurnstileSecret: "secret", ResendFrom: "onboarding@resend.dev"}
	handler := New(cfg, db).Routes()
	token, _ := store.Token()
	if err = db.CreateAdminSession(token); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "admin_session", Value: token}

	update := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(`{"mailProvider":"resend","resendApiKey":"re_test_key","resendFrom":"onboarding@resend.dev","emailVerificationRequired":"true"}`))
	update.Header.Set("content-type", "application/json")
	update.AddCookie(cookie)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, update)
	if result.Code != http.StatusOK {
		t.Fatalf("Resend settings status=%d body=%s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "re_test_key") || !strings.Contains(result.Body.String(), `"resendApiKeyConfigured":true`) {
		t.Fatalf("Resend key leaked or status missing: %s", result.Body.String())
	}
	values, err := db.Settings()
	if err != nil || values["resendApiKey"] != "re_test_key" || values["mailProvider"] != "resend" {
		t.Fatalf("Resend settings were not stored correctly: err=%v values=%v", err, values)
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/api/auth/config", nil)
	configResult := httptest.NewRecorder()
	handler.ServeHTTP(configResult, configRequest)
	if configResult.Code != http.StatusOK || !strings.Contains(configResult.Body.String(), `"emailVerificationRequired":true`) {
		t.Fatalf("auth config did not update immediately for resend: %s", configResult.Body.String())
	}
}

func TestAdminOrderLogsIncludesAttemptsAndRefunds(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "admin-order-logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("orderlogs@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	order := store.SMSOrder{
		ID:               "SORDERLOG",
		UserID:           u.ID,
		Country:          "7",
		CountryName:      "马来西亚",
		Service:          "dr",
		UpstreamProvider: "hero",
		UpstreamCountry:  "7",
		UpstreamService:  "dr",
		UpstreamCost:     .1,
		PriceFen:         120,
		CreatedAt:        now,
	}
	payment := store.Recharge{
		ID:        "PORDERLOG",
		UserID:    u.ID,
		AmountFen: 120,
		Provider:  "epay",
		PayType:   "2",
		Token:     "token",
		Reference: order.ID,
		CreatedAt: now,
	}
	if err = db.CreateSMSPayment(u, order, payment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CompleteSMSPayment(payment.ID, "trade-order-log"); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMSWithProvider(order.ID, "hero", "hero-log-1", "60110000000", .1); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateSMS(order.ID, "code_received", "855362"); err != nil {
		t.Fatal(err)
	}
	if err = db.MarkRechargeRefunded(payment.ID, "refund-log-1"); err != nil {
		t.Fatal(err)
	}
	db.Audit("order.close.refunded", order.ID, "refund-log-1")
	adminToken, _ := store.Token()
	if err = db.CreateAdminSession(adminToken); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/orders/SORDERLOG/logs", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: adminToken})
	w := httptest.NewRecorder()
	New(config.Config{}, db).Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"订单创建"`, `"支付成功"`, `"号码分配"`, `"原路退款成功"`, `"管理员关闭并退款"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
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
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].HttpOnly != true || cookies[0].MaxAge != 24*60*60 {
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
			w.Write([]byte(`{"6":{"tg":{"cost":0.5,"count":3,"physicalCount":3}}}`))
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
	if order.Phone != "628001" || order.PriceFen != 460 || order.AutoReplace {
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
			_, _ = w.Write([]byte(`{"2":{"tg":{"cost":1,"count":5,"physicalCount":5}}}`))
		case "getNumberV2":
			if r.URL.Query().Get("country") != "2" || r.URL.Query().Get("service") != "tg" {
				t.Fatalf("wrong Hero route country=%q service=%q", r.URL.Query().Get("country"), r.URL.Query().Get("service"))
			}
			_, _ = w.Write([]byte(`{"activationId":"hero-agg-id","phoneNumber":"77000000001","activationCost":1}`))
		case "getStatus":
			_, _ = w.Write([]byte("STATUS_OK:246810"))
		default:
			http.NotFound(w, r)
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
	if checkout.PriceFen != 820 {
		t.Fatalf("aggregated price=%d, want 820", checkout.PriceFen)
	}
	pending, err := db.GetSMSByID(checkout.ID)
	if err != nil || pending.UpstreamProvider != "hero" || pending.UpstreamCountry != "2" || pending.UpstreamService != "tg" {
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

func TestGlobalRecommendedQuotePurchasesItsCountryWithoutCountrySelection(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"6":{"tg":{"cost":0.5,"count":2,"physicalCount":2}},"7":{"tg":{"cost":0.2,"count":4,"physicalCount":4}}}`))
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

func TestPurchaseUsesExplicitSelectedCountryQuote(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "selected-country-purchase.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"},"9":{"id":9,"eng":"Thailand","chn":"泰国"}}`))
		case "getServicesList":
			if country := r.URL.Query().Get("country"); country != "" && country != "9" {
				t.Fatalf("unexpected selected country for services=%q", country)
			}
			_, _ = w.Write([]byte(`{"services":[{"code":"tg","name":"Telegram"}]}`))
		case "getPrices":
			country := r.URL.Query().Get("country")
			if country == "" {
				_, _ = w.Write([]byte(`{"7":{"tg":{"cost":0.2,"count":4,"physicalCount":4}},"9":{"tg":{"cost":0.3,"count":6,"physicalCount":6}}}`))
				return
			}
			if country != "9" {
				t.Fatalf("unexpected selected country for prices=%q", country)
			}
			_, _ = w.Write([]byte(`{"9":{"tg":{"cost":0.3,"count":6,"physicalCount":6}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := New(config.Config{HeroKey: "hero", HeroURL: upstream.URL, HeroCurrency: "840", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", AllowLiveSMSInSandbox: true}, db)
	handler := server.Routes()
	_, session, err := db.Register("selected-country@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"9","Service":"tg","payType":2}`))
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
	if err != nil {
		t.Fatal(err)
	}
	if order.Country != "9" || order.UpstreamCountry != "9" {
		t.Fatalf("order=%+v, want selected country 9 to be preserved", order)
	}
	if checkout.PriceFen != 316 {
		t.Fatalf("price=%d, want 316", checkout.PriceFen)
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

func TestOrderStatusSyncsHeroCodeToLocalOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "order-sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, token, err := db.Register("sync@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{ID: "SSYNC", UserID: u.ID, Country: "4", CountryName: "???", Service: "dr", UpstreamProvider: "hero", UpstreamCountry: "4", UpstreamService: "dr", UpstreamCost: .1, PriceFen: 120, CreatedAt: now}
	payment := store.Recharge{ID: "PSYNC", UserID: u.ID, AmountFen: 120, Provider: "epay", PayType: "2", Token: "token", Reference: o.ID, CreatedAt: now}
	if err = db.CreateSMSPayment(u, o, payment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE recharges SET status='paid' WHERE id=?", payment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE sms_orders SET status='waiting' WHERE id=?", o.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMSWithProvider(o.ID, "hero", "hero-sync-1", "639551234567", .1); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getStatus":
			_, _ = w.Write([]byte("STATUS_OK:855362"))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	h := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840"}, db).Routes()
	req := httptest.NewRequest(http.MethodGet, "/api/orders/SSYNC", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "code_received" || got.Code != "855362" {
		t.Fatalf("unexpected synced order: %+v", got)
	}
}

func TestFinishOrderMarksHeroOrderFinished(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "finish-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, token, err := db.Register("finish@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{ID: "SFINISH", UserID: u.ID, Country: "4", CountryName: "???", Service: "dr", UpstreamProvider: "hero", UpstreamCountry: "4", UpstreamService: "dr", UpstreamCost: .1, PriceFen: 120, CreatedAt: now}
	payment := store.Recharge{ID: "PFINISH", UserID: u.ID, AmountFen: 120, Provider: "epay", PayType: "2", Token: "token", Reference: o.ID, CreatedAt: now}
	if err = db.CreateSMSPayment(u, o, payment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE recharges SET status='paid' WHERE id=?", payment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE sms_orders SET status='waiting' WHERE id=?", o.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMSWithProvider(o.ID, "hero", "hero-finish-1", "639551234567", .1); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateSMS(o.ID, "code_received", "855362"); err != nil {
		t.Fatal(err)
	}
	var setStatusCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getStatus":
			_, _ = w.Write([]byte("STATUS_OK:855362"))
		case "setStatus":
			setStatusCalls++
			if r.URL.Query().Get("status") != "6" {
				t.Fatalf("unexpected finish status=%q", r.URL.Query().Get("status"))
			}
			_, _ = w.Write([]byte("ACCESS_READY"))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	h := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840"}, db).Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/orders/SFINISH/finish", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if setStatusCalls != 1 || got.Status != "finished" {
		t.Fatalf("setStatusCalls=%d order=%+v", setStatusCalls, got)
	}
}

func TestCancelOrderRefundsPaidWaitingOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "cancel-refund.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, token, err := db.Register("cancelrefund@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{
		ID:               "SCANCELREFUND",
		UserID:           u.ID,
		Country:          "4",
		CountryName:      "菲律宾",
		Service:          "dr",
		UpstreamProvider: "hero",
		UpstreamCountry:  "4",
		UpstreamService:  "dr",
		UpstreamCost:     .1,
		PriceFen:         120,
		CreatedAt:        now,
	}
	payment := store.Recharge{
		ID:        "PCANCELREFUND",
		UserID:    u.ID,
		AmountFen: 120,
		Provider:  "epay",
		PayType:   "2",
		Token:     "token",
		Reference: o.ID,
		CreatedAt: now,
	}
	if err = db.CreateSMSPayment(u, o, payment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CompleteSMSPayment(payment.ID, "trade-cancel-1"); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMSWithProvider(o.ID, "hero", "hero-cancel-1", "639551234567", .1); err != nil {
		t.Fatal(err)
	}
	platformPublic, platformPrivate := testRSAKeyPair(t)
	_, merchantPrivate := testRSAKeyPair(t)
	var refundCalled bool
	var refundTradeNo, refundOutNo, refundMoney string
	var heroCancelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/pay/refund":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			refundCalled = true
			refundTradeNo = r.Form.Get("trade_no")
			refundOutNo = r.Form.Get("out_refund_no")
			refundMoney = r.Form.Get("money")
			payload := map[string]any{
				"code":          0,
				"msg":           "success",
				"trade_no":      refundTradeNo,
				"out_refund_no": refundOutNo,
				"money":         refundMoney,
				"timestamp":     strconv.FormatInt(time.Now().Unix(), 10),
			}
			signature := testRefundSignature(t, platformPrivate, payload)
			payload["sign"] = signature
			payload["sign_type"] = "RSA"
			_ = json.NewEncoder(w).Encode(payload)
		case r.URL.Query().Get("action") == "setStatus":
			heroCancelCalls++
			_, _ = w.Write([]byte("ACCESS_CANCEL"))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	h := New(config.Config{
		HeroKey:                "key",
		HeroURL:                upstream.URL,
		HeroCurrency:           "840",
		EPayPID:                "1001",
		EPayURL:                upstream.URL,
		EPayPlatformPublicKey:  platformPublic,
		EPayMerchantPrivateKey: merchantPrivate,
	}, db).Routes()
	if _, err = db.DB.Exec("UPDATE sms_orders SET last_number_at=? WHERE id=?", time.Now().UTC().Add(-3*time.Minute).Format(time.RFC3339), o.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orders/SCANCELREFUND/cancel", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if !refundCalled || refundTradeNo != "trade-cancel-1" || refundMoney != "1.20" {
		t.Fatalf("refundCalled=%v trade=%q money=%q", refundCalled, refundTradeNo, refundMoney)
	}
	if !strings.Contains(w.Body.String(), `"refunded":true`) {
		t.Fatalf("missing refunded flag: %s", w.Body.String())
	}
	got, err := db.GetSMS(o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" || !got.Refunded || heroCancelCalls != 1 {
		t.Fatalf("order=%+v heroCancelCalls=%d", got, heroCancelCalls)
	}
	recharge, err := db.GetRechargeByReference(o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recharge.Status != "refunded" || recharge.RefundProviderID != refundOutNo || recharge.RefundedAt == "" {
		t.Fatalf("recharge=%+v", recharge)
	}
}

func TestAdminCloseRefundsPaidWaitingOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "admin-close-refund.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("admincloserefund@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	o := store.SMSOrder{
		ID:               "SADMINCLOSE",
		UserID:           u.ID,
		Country:          "4",
		CountryName:      "菲律宾",
		Service:          "dr",
		UpstreamProvider: "hero",
		UpstreamCountry:  "4",
		UpstreamService:  "dr",
		UpstreamCost:     .1,
		PriceFen:         120,
		CreatedAt:        now,
	}
	payment := store.Recharge{
		ID:        "PADMINCLOSE",
		UserID:    u.ID,
		AmountFen: 120,
		Provider:  "50pay",
		PayType:   "2",
		Token:     "token",
		Reference: o.ID,
		CreatedAt: now,
	}
	if err = db.CreateSMSPayment(u, o, payment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CompleteSMSPayment(payment.ID, "trade-admin-1"); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMSWithProvider(o.ID, "hero", "hero-admin-1", "639551234568", .1); err != nil {
		t.Fatal(err)
	}
	adminToken, _ := store.Token()
	if err = db.CreateAdminSession(adminToken); err != nil {
		t.Fatal(err)
	}
	platformPublic, platformPrivate := testRSAKeyPair(t)
	_, merchantPrivate := testRSAKeyPair(t)
	var refundCalled bool
	var refundOutNo string
	var heroCancelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/pay/refund":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			refundCalled = true
			refundOutNo = r.Form.Get("out_refund_no")
			payload := map[string]any{
				"code":          0,
				"msg":           "success",
				"trade_no":      r.Form.Get("trade_no"),
				"out_refund_no": refundOutNo,
				"money":         r.Form.Get("money"),
				"timestamp":     strconv.FormatInt(time.Now().Unix(), 10),
			}
			payload["sign"] = testRefundSignature(t, platformPrivate, payload)
			payload["sign_type"] = "RSA"
			_ = json.NewEncoder(w).Encode(payload)
		case r.URL.Query().Get("action") == "setStatus":
			heroCancelCalls++
			_, _ = w.Write([]byte("ACCESS_CANCEL"))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	h := New(config.Config{
		HeroKey:                "key",
		HeroURL:                upstream.URL,
		HeroCurrency:           "840",
		EPayPID:                "1001",
		EPayURL:                upstream.URL,
		EPayPlatformPublicKey:  platformPublic,
		EPayMerchantPrivateKey: merchantPrivate,
	}, db).Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/orders/SADMINCLOSE/close", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: adminToken})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if !refundCalled || !strings.Contains(w.Body.String(), `"refunded":true`) {
		t.Fatalf("refundCalled=%v body=%s", refundCalled, w.Body.String())
	}
	order, err := db.GetSMSByID(o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != "admin_closed" || !order.Refunded || heroCancelCalls != 1 {
		t.Fatalf("order=%+v heroCancelCalls=%d", order, heroCancelCalls)
	}
	recharge, err := db.GetRechargeByReference(o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recharge.Status != "refunded" || recharge.RefundProviderID != refundOutNo || recharge.RefundedAt == "" {
		t.Fatalf("recharge=%+v", recharge)
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
		{"/", []string{`rel="canonical" href="https://yunmatai.xyz/"`, `href="/contact.html"`, `href="/api.html"`, `href="/cookie.html"`, `href="/privacy.html"`, `href="/terms.html"`, `application/ld+json`}},
		{"/contact.html", []string{"联系我们", `rel="canonical"`, `ContactPage`}},
		{"/api.html", []string{"API 接入", "合作 API"}},
		{"/cookie.html", []string{"Cookie 政策", `rel="canonical" href="https://yunmatai.xyz/cookie.html"`}},
		{"/robots.txt", []string{"User-agent: *", "Sitemap: https://yunmatai.xyz/sitemap.xml"}},
		{"/sitemap.xml", []string{"<urlset", "https://yunmatai.xyz/contact.html", "https://yunmatai.xyz/api.html", "https://yunmatai.xyz/cookie.html", "https://yunmatai.xyz/privacy.html", "https://yunmatai.xyz/terms.html"}},
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

func TestSitemapUsesCrawlerFriendlyHeaders(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "sitemap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(config.Config{}, db).Routes()
	req := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	result := httptest.NewRecorder()
	h.ServeHTTP(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml status=%d", result.Code)
	}
	if got := result.Header().Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := result.Header().Get("Content-Length"); got == "" {
		t.Fatal("Content-Length is empty")
	}
	if got := result.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("Content-Security-Policy=%q", got)
	}
	if !strings.Contains(result.Body.String(), `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`) {
		t.Fatal("sitemap body missing urlset")
	}
}

func TestHTMLPagesDisableCaching(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "html-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{AdminUIPath: "/console-test-hidden.html"}
	h := New(cfg, db).Routes()
	for _, path := range []string{"/", cfg.AdminUIPath, "/api.html", "/contact.html", "/cookie.html", "/privacy.html", "/terms.html"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, req)
		if result.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, result.Code)
		}
		if got := result.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
			t.Fatalf("GET %s Cache-Control=%q", path, got)
		}
		if got := result.Header().Get("Pragma"); got != "no-cache" {
			t.Fatalf("GET %s Pragma=%q", path, got)
		}
		if got := result.Header().Get("Expires"); got != "0" {
			t.Fatalf("GET %s Expires=%q", path, got)
		}
	}
}

func TestLegacyAdminPathReturnsNotFoundAndHiddenAdminPathServesPage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hidden-admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{AdminUIPath: "/console-test-hidden.html"}
	h := New(cfg, db).Routes()

	legacyReq := httptest.NewRequest(http.MethodGet, "/admin.html", nil)
	legacyRes := httptest.NewRecorder()
	h.ServeHTTP(legacyRes, legacyReq)
	if legacyRes.Code != http.StatusNotFound {
		t.Fatalf("GET /admin.html status=%d", legacyRes.Code)
	}

	hiddenReq := httptest.NewRequest(http.MethodGet, cfg.AdminUIPath, nil)
	hiddenRes := httptest.NewRecorder()
	h.ServeHTTP(hiddenRes, hiddenReq)
	if hiddenRes.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d", cfg.AdminUIPath, hiddenRes.Code)
	}
	if got := hiddenRes.Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive, nosnippet" {
		t.Fatalf("GET %s X-Robots-Tag=%q", cfg.AdminUIPath, got)
	}
	if !strings.Contains(hiddenRes.Body.String(), "ADMIN CONSOLE") {
		t.Fatalf("GET %s missing admin shell", cfg.AdminUIPath)
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

func TestSyncOrderDoesNotDowngradeReplacingOrderFromStaleUpstreamStatus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "sync-replacing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("sync-replacing@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	order := store.SMSOrder{
		ID:               "SREPLSYNC",
		UserID:           u.ID,
		UpstreamProvider: "hero",
		Country:          "9",
		Service:          "tg",
		UpstreamCost:     .5,
		PriceFen:         199,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if err = db.CreateSMS(u, order); err != nil {
		t.Fatal(err)
	}
	if err = db.ActivateSMS(order.ID, "old-upstream", "66000000001", .5); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimAutoReplace(order.ID, "old-upstream"); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getStatus":
			_, _ = w.Write([]byte("STATUS_CANCEL"))
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	s := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840"}, db)
	current, err := db.GetSMS(order.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := s.syncOrderForUser(t.Context(), u.ID, current)
	if got.Status != "replacing" {
		t.Fatalf("status=%s, want replacing", got.Status)
	}
	refreshed, err := db.GetSMS(order.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != "replacing" {
		t.Fatalf("db status=%s, want replacing", refreshed.Status)
	}
}

func TestRunAutoReplaceFulfillPaidOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "run-paid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := db.Register("paid-batch@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	order := store.SMSOrder{
		ID:               "SPAIDRUN",
		UserID:           u.ID,
		UpstreamProvider: "hero",
		UpstreamCountry:  "7",
		UpstreamService:  "fb",
		Country:          "7",
		Service:          "fb",
		UpstreamCost:     .0399,
		PriceFen:         100,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	recharge := store.Recharge{
		ID:        "PPAIDRUN",
		UserID:    u.ID,
		AmountFen: 100,
		Provider:  "epay",
		PayType:   "2",
		Token:     "token",
		Reference: order.ID,
		CreatedAt: order.CreatedAt,
	}
	if err = db.CreateSMSPayment(u, order, recharge); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE sms_orders SET status='paid' WHERE id=?", order.ID); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getNumberV2":
			_ = json.NewEncoder(w).Encode(map[string]any{"activationId": "hero-paid-id", "phoneNumber": "60111111111", "activationCost": .0399})
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	s := New(config.Config{HeroKey: "key", HeroURL: upstream.URL, HeroCurrency: "840"}, db)
	s.runPaidOrderBatch(t.Context())
	got, err := db.GetSMS(order.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "waiting" || got.UpstreamID != "hero-paid-id" || got.Phone != "60111111111" {
		t.Fatalf("unexpected paid activation: %+v", got)
	}
}

func TestPublicCatalogPrefersHigherStockBeforeLowerPrice(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "catalog-stock-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"},"9":{"id":9,"eng":"Thailand","chn":"泰国"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"fb","name":"Facebook"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"7":{"fb":{"cost":0.02,"count":12,"physicalCount":12}},"9":{"fb":{"cost":0.01,"count":3,"physicalCount":3}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := New(config.Config{HeroKey: "hero", HeroURL: upstream.URL, HeroCurrency: "840", USDCNY: 7.2, Markup: 1}, db)
	handler := server.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Offers []struct {
			Service  string `json:"service"`
			Country  string `json:"country"`
			Count    int    `json:"count"`
			PriceFen int64  `json:"priceFen"`
		} `json:"offers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Offers) != 2 {
		t.Fatalf("offers=%+v, want both country offers in public catalog", payload.Offers)
	}
	bestCountry := ""
	bestCount := -1
	for _, offer := range payload.Offers {
		if offer.Count > bestCount {
			bestCount = offer.Count
			bestCountry = offer.Country
		}
	}
	if bestCountry != "7" || bestCount != 12 {
		t.Fatalf("offers=%+v, want Malaysia to be the highest-stock route", payload.Offers)
	}
}

func TestPurchaseAllowsFastRouteModeOverride(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "fast-override.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"},"9":{"id":9,"eng":"Thailand","chn":"泰国"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"fb","name":"Facebook"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"7":{"fb":{"cost":0.02,"count":12,"physicalCount":12}},"9":{"fb":{"cost":0.01,"count":3,"physicalCount":3}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := New(config.Config{HeroKey: "hero", HeroURL: upstream.URL, HeroCurrency: "840", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", AllowLiveSMSInSandbox: true}, db)
	handler := server.Routes()
	_, session, err := db.Register("fast@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"","Service":"fb","payType":2,"routeMode":"fast"}`))
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
	if err != nil {
		t.Fatal(err)
	}
	if order.Country != "7" || order.UpstreamCountry != "7" {
		t.Fatalf("order=%+v, want fast route country 7", order)
	}
	if checkout.PriceFen != 115 {
		t.Fatalf("price=%d, want fast route price 115 with stock-priority route", checkout.PriceFen)
	}
}

func TestCountryCatalogKeepsMultipleProviderOffersForRouteChoice(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "country-offers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	heroUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"fb","name":"Facebook"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"7":{"fb":{"cost":0.35,"count":12,"physicalCount":12}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer heroUpstream.Close()
	smsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/countries":
			_, _ = w.Write([]byte(`{"1":{"id":1,"name":"Malaysia"}}`))
		case "/applications":
			_, _ = w.Write([]byte(`{"100":{"id":100,"name":"Facebook"}}`))
		case "/get-prices":
			_, _ = w.Write([]byte(`{"100":{"price":0.20,"count":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer smsUpstream.Close()

	server := New(config.Config{
		HeroKey:               "hero",
		HeroURL:               heroUpstream.URL,
		HeroCurrency:          "840",
		SMSManToken:           "sms",
		SMSManURL:             smsUpstream.URL,
		USDCNY:                7.2,
		SMSManCNYRate:         7.2,
		Markup:                1,
		PayProvider:           "sandbox",
		AllowLiveSMSInSandbox: true,
	}, db)
	handler := server.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/catalog?country=7", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Offers []struct {
			Service  string `json:"service"`
			Country  string `json:"country"`
			Count    int    `json:"count"`
			PriceFen int64  `json:"priceFen"`
		} `json:"offers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	var fbOffers []int64
	for _, offer := range payload.Offers {
		if offer.Service == "fb" && offer.Country == "7" {
			fbOffers = append(fbOffers, offer.PriceFen)
		}
	}
	if len(fbOffers) != 2 {
		t.Fatalf("offers=%+v, want both hero and smsman routes for country-specific catalog", payload.Offers)
	}
}

func TestBlockedProvidersExcludeRoutesFromCatalog(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "blocked-providers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpdateSettings(map[string]string{"blockedProviders": "smsman"}); err != nil {
		t.Fatal(err)
	}
	heroUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getBalance":
			_, _ = w.Write([]byte("5"))
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"fb","name":"Facebook"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"7":{"fb":{"cost":0.35,"count":12,"physicalCount":12}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer heroUpstream.Close()
	smsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get-balance":
			_, _ = w.Write([]byte(`{"balance":10}`))
		case "/countries":
			_, _ = w.Write([]byte(`{"1":{"id":1,"name":"Malaysia"}}`))
		case "/applications":
			_, _ = w.Write([]byte(`{"100":{"id":100,"name":"Facebook"}}`))
		case "/get-prices":
			_, _ = w.Write([]byte(`{"100":{"price":0.20,"count":30}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer smsUpstream.Close()
	server := New(config.Config{
		HeroKey:               "hero",
		HeroURL:               heroUpstream.URL,
		HeroCurrency:          "840",
		SMSManToken:           "sms",
		SMSManURL:             smsUpstream.URL,
		USDCNY:                7.2,
		SMSManCNYRate:         7.2,
		Markup:                1,
		PayProvider:           "sandbox",
		AllowLiveSMSInSandbox: true,
	}, db)
	req := httptest.NewRequest(http.MethodGet, "/api/catalog?country=7", nil)
	res := httptest.NewRecorder()
	server.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Offers []struct {
			Service  string `json:"service"`
			Country  string `json:"country"`
			PriceFen int64  `json:"priceFen"`
		} `json:"offers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Offers) != 1 || payload.Offers[0].PriceFen != 352 {
		t.Fatalf("offers=%+v, want only hero route after blocking smsman", payload.Offers)
	}
}

func TestZeroSMSManBalanceExcludesCatalogInventory(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "zero-smsman-balance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	smsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get-balance":
			_, _ = w.Write([]byte(`{"balance":0}`))
		case "/countries":
			_, _ = w.Write([]byte(`{"3":{"id":3,"name":"China"}}`))
		case "/applications":
			_, _ = w.Write([]byte(`{"149":{"id":149,"name":"QQ"}}`))
		case "/get-prices":
			_, _ = w.Write([]byte(`{"149":{"price":1.50,"count":99}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer smsUpstream.Close()
	server := New(config.Config{
		SMSManToken:           "sms",
		SMSManURL:             smsUpstream.URL,
		SMSManCNYRate:         0.09,
		Markup:                1,
		PayProvider:           "sandbox",
		AllowLiveSMSInSandbox: true,
	}, db)
	req := httptest.NewRequest(http.MethodGet, "/api/catalog?country=3", nil)
	res := httptest.NewRecorder()
	server.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Countries []any `json:"countries"`
		Services  []any `json:"services"`
		Offers    []any `json:"offers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Countries) != 0 || len(payload.Services) != 0 || len(payload.Offers) != 0 {
		t.Fatalf("payload=%+v, want no sellable inventory when smsman balance is zero", payload)
	}
}

func TestPurchaseAllowsStableRouteModeOverride(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "stable-override.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	heroUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"7":{"id":7,"eng":"Malaysia","chn":"马来西亚"}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"services":[{"code":"fb","name":"Facebook"}]}`))
		case "getPrices":
			_, _ = w.Write([]byte(`{"7":{"fb":{"cost":0.35,"count":12,"physicalCount":12}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer heroUpstream.Close()
	smsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/countries":
			_, _ = w.Write([]byte(`{"1":{"id":1,"name":"Malaysia"}}`))
		case "/applications":
			_, _ = w.Write([]byte(`{"100":{"id":100,"name":"Facebook"}}`))
		case "/get-prices":
			_, _ = w.Write([]byte(`{"100":{"price":0.20,"count":30}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer smsUpstream.Close()
	server := New(config.Config{
		HeroKey:               "hero",
		HeroURL:               heroUpstream.URL,
		HeroCurrency:          "840",
		SMSManToken:           "sms",
		SMSManURL:             smsUpstream.URL,
		USDCNY:                7.2,
		SMSManCNYRate:         7.2,
		Markup:                1,
		PayProvider:           "sandbox",
		AllowLiveSMSInSandbox: true,
	}, db)
	handler := server.Routes()
	_, session, err := db.Register("stable@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"Country":"7","Service":"fb","payType":2,"routeMode":"stable"}`))
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
	if err != nil {
		t.Fatal(err)
	}
	if order.UpstreamProvider != "hero" || order.UpstreamCountry != "7" {
		t.Fatalf("order=%+v, want stable route to choose higher-priced hero quote", order)
	}
	if checkout.PriceFen != 352 {
		t.Fatalf("price=%d, want stable route price 352 from higher-priced hero quote", checkout.PriceFen)
	}
}

func testRSAKeyPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(crand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	return string(publicPEM), string(privatePEM)
}

func testRefundSignature(t *testing.T, privatePEM string, payload map[string]any) string {
	t.Helper()
	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		t.Fatal("missing private key block")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("private key is not RSA")
	}
	values := url.Values{}
	for keyName, value := range payload {
		values.Set(keyName, fmt.Sprint(value))
	}
	keys := make([]string, 0, len(values))
	for keyName := range values {
		if keyName != "sign" && keyName != "sign_type" && values.Get(keyName) != "" {
			keys = append(keys, keyName)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, keyName := range keys {
		parts = append(parts, keyName+"="+values.Get(keyName))
	}
	message := strings.Join(parts, "&")
	sum := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(crand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}
