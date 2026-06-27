package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sms-platform/internal/config"
	"sms-platform/internal/epay"
	"sms-platform/internal/hero"
	"sms-platform/internal/mailer"
	"sms-platform/internal/smsman"
	"sms-platform/internal/store"
	"sms-platform/internal/turnstile"
	"sms-platform/internal/yishoumi"
)

//go:embed public/* public/flags/*
var assets embed.FS

type Server struct {
	C         config.Config
	Store     *store.Store
	Hero      *hero.Client
	SMSMan    *smsman.Client
	SMSCache  *smsmanCatalogCache
	Catalog   *catalogResponseCache
	YSM       *yishoumi.Client
	EPay      *epay.Client
	Mailer    mailer.Sender
	Turnstile turnstile.Verifier
}
type handler func(http.ResponseWriter, *http.Request, store.User)
type adminHandler func(http.ResponseWriter, *http.Request)

func New(c config.Config, s *store.Store) *Server {
	if c.AutoReplaceAfter < 2*time.Minute {
		c.AutoReplaceAfter = 2 * time.Minute
	}
	if c.AutoReplaceAfter > 15*time.Minute {
		c.AutoReplaceAfter = 15 * time.Minute
	}
	if c.AutoReplaceMax < 0 {
		c.AutoReplaceMax = 0
	}
	if c.AutoReplaceMax == 0 || c.AutoReplaceMax > 20 {
		c.AutoReplaceMax = 20
	}
	if c.AutoReplaceScan < time.Second {
		c.AutoReplaceScan = 10 * time.Second
	}
	if c.PaymentOrderTTL < time.Minute {
		c.PaymentOrderTTL = 20 * time.Minute
	}
	var sender mailer.Sender = &mailer.SMTP{Host: c.SMTPHost, Port: c.SMTPPort, User: c.SMTPUser, Password: c.SMTPPassword, From: c.SMTPFrom}
	if c.ResendAPIKey != "" && c.ResendFrom != "" {
		sender = &mailer.Resend{APIKey: c.ResendAPIKey, From: c.ResendFrom}
	}
	return &Server{
		C:         c,
		Store:     s,
		Hero:      hero.New(c.HeroKey, c.HeroURL, c.HeroCurrency),
		SMSMan:    smsman.New(c.SMSManToken, c.SMSManURL),
		SMSCache:  newSMSManCatalogCache(),
		Catalog:   newCatalogResponseCache(2 * time.Minute),
		YSM:       yishoumi.New(c.YSMAppID, c.YSMSecret, c.YSMURL),
		EPay:      epay.New(c.EPayPID, c.EPayKey, c.EPayURL, epay.WithRefundKeys(c.EPayPlatformPublicKey, c.EPayMerchantPrivateKey)),
		Mailer:    sender,
		Turnstile: turnstile.New(c.TurnstileSecret),
	}
}
func (s *Server) Routes() http.Handler {
	m := http.NewServeMux()
	sub, _ := fs.Sub(assets, "public")
	m.HandleFunc("GET /healthz", s.health)
	m.Handle("/", http.FileServer(http.FS(sub)))
	m.HandleFunc("POST /api/auth/register", s.register)
	m.HandleFunc("GET /api/auth/config", s.authConfig)
	m.HandleFunc("POST /api/auth/email-code", s.sendEmailCode)
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/auth/logout", s.logout)
	m.HandleFunc("GET /api/me", s.auth(s.me))
	m.HandleFunc("GET /api/profile", s.auth(s.profile))
	m.HandleFunc("GET /api/catalog", s.publicCatalog)
	m.HandleFunc("GET /api/orders", s.auth(s.orders))
	m.HandleFunc("POST /api/orders", s.auth(s.purchase))
	m.HandleFunc("GET /api/orders/{id}", s.auth(s.orderStatus))
	m.HandleFunc("GET /api/orders/{id}/checkout", s.auth(s.orderCheckout))
	m.HandleFunc("POST /api/orders/{id}/finish", s.auth(s.finishOrder))
	m.HandleFunc("POST /api/orders/{id}/replace", s.auth(s.manualReplace))
	m.HandleFunc("POST /api/orders/{id}/cancel", s.auth(s.cancel))
	m.HandleFunc("GET /api/settings", s.publicSettings)
	m.HandleFunc("GET /api/announcements", s.publicAnnouncements)
	m.HandleFunc("GET /api/support", s.auth(s.supportMessages))
	m.HandleFunc("POST /api/support", s.auth(s.sendSupportMessage))
	m.HandleFunc("POST /api/admin/login", s.adminLogin)
	m.HandleFunc("POST /api/admin/logout", s.admin(s.adminLogout))
	m.HandleFunc("GET /api/admin/overview", s.admin(s.adminOverview))
	m.HandleFunc("GET /api/admin/settings", s.admin(s.adminSettings))
	m.HandleFunc("PUT /api/admin/settings", s.admin(s.updateAdminSettings))
	m.HandleFunc("GET /api/admin/chats", s.admin(s.adminChats))
	m.HandleFunc("GET /api/admin/chats/{userID}", s.admin(s.adminChatMessages))
	m.HandleFunc("POST /api/admin/chats/{userID}", s.admin(s.adminSendMessage))
	m.HandleFunc("GET /api/admin/users", s.admin(s.adminUsers))
	m.HandleFunc("PATCH /api/admin/users/{userID}", s.admin(s.adminUpdateUser))
	m.HandleFunc("GET /api/admin/orders", s.admin(s.adminOrders))
	m.HandleFunc("POST /api/admin/orders/{id}/close", s.admin(s.adminCloseOrder))
	m.HandleFunc("GET /api/admin/payments", s.admin(s.adminPayments))
	m.HandleFunc("GET /api/admin/email-logs", s.admin(s.adminEmailLogs))
	m.HandleFunc("GET /api/admin/announcements", s.admin(s.adminAnnouncements))
	m.HandleFunc("POST /api/admin/announcements", s.admin(s.adminSaveAnnouncement))
	m.HandleFunc("PUT /api/admin/announcements/{id}", s.admin(s.adminSaveAnnouncement))
	m.HandleFunc("DELETE /api/admin/announcements/{id}", s.admin(s.adminDeleteAnnouncement))
	m.HandleFunc("GET /api/admin/audit", s.admin(s.adminAudit))
	m.HandleFunc("GET /sandbox/pay/{id}", s.sandboxPay)
	m.HandleFunc("POST /sandbox/pay/{id}", s.sandboxComplete)
	m.HandleFunc("POST /api/payments/yishoumi/notify", s.ysmNotify)
	m.HandleFunc("GET /api/payments/epay/notify", s.epayNotify)
	m.HandleFunc("GET /api/payments/epay/return", s.epayReturn)
	return security(m)
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DB.PingContext(r.Context()); err != nil {
		jsonOut(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
		return
	}
	jsonOut(w, http.StatusOK, map[string]string{"status": "ok"})
}
func security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; img-src 'self' data:; connect-src 'self' https://challenges.cloudflare.com")
		next.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e any) {
	jsonOut(w, status, map[string]any{"error": fmt.Sprint(e)})
}
func decode(r *http.Request, v any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

func (s *Server) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("session")
		if e != nil {
			fail(w, 401, "请先登录")
			return
		}
		u, e := s.Store.UserByToken(c.Value)
		if e != nil {
			fail(w, 401, "登录已过期")
			return
		}
		_ = s.Store.TouchSession(c.Value)
		setSession(w, c.Value, strings.HasPrefix(s.C.BaseURL, "https://"))
		next(w, r, u)
	}
}
func setSession(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure, MaxAge: 30 * 86400})
}
func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	email := s.effectiveEmailVerification()
	jsonOut(w, 200, map[string]any{"emailVerificationRequired": email.Enabled, "emailVerificationAvailable": emailVerificationAvailable(email), "turnstileSiteKey": s.C.TurnstileSiteKey})
}

func normalizedEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := netmail.ParseAddress(value)
	if err != nil || strings.ToLower(parsed.Address) != value || len(value) > 254 {
		return "", fmt.Errorf("邮箱格式错误")
	}
	return value, nil
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	parsed := net.ParseIP(host)
	if parsed != nil && parsed.IsLoopback() {
		forwarded := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-For"), ",", 2)[0])
		if net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	return host
}

func verificationCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func (s *Server) sendEmailCode(w http.ResponseWriter, r *http.Request) {
	emailSettings := s.effectiveEmailVerification()
	if !emailSettings.Enabled || !emailVerificationAvailable(emailSettings) {
		fail(w, http.StatusServiceUnavailable, "邮箱验证尚未启用")
		return
	}
	var input struct {
		Email          string `json:"email"`
		TurnstileToken string `json:"turnstileToken"`
	}
	if decode(r, &input) != nil {
		fail(w, 400, "请求格式错误")
		return
	}
	email, err := normalizedEmail(input.Email)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if err = s.Turnstile.Verify(r.Context(), input.TurnstileToken, remoteIP(r)); err != nil {
		fail(w, 403, "请先完成人机验证")
		return
	}
	code, err := verificationCode()
	if err != nil {
		fail(w, 500, "验证码生成失败")
		return
	}
	if err = s.Store.SaveEmailVerification(email, code, remoteIP(r)); err != nil {
		fail(w, 429, err)
		return
	}
	sender := s.Mailer
	if emailSettings.SMTPOverridden || emailSettings.ResendOverridden {
		sender = verificationSender(emailSettings)
	}
	provider := emailSettings.Provider
	senderAddress := emailSettings.SMTPFrom
	if provider == "resend" {
		senderAddress = emailSettings.ResendFrom
	}
	if err = sender.SendVerification(r.Context(), email, code); err != nil {
		s.Store.DeleteEmailVerification(email)
		s.Store.LogEmailSend(email, provider, senderAddress, "failed", err.Error())
		log.Printf("verification email delivery failed for %s: %v", email, err)
		fail(w, 502, "验证码邮件发送失败，请稍后重试")
		return
	}
	s.Store.LogEmailSend(email, provider, senderAddress, "sent", "")
	jsonOut(w, 200, map[string]any{"ok": true, "expiresIn": 600})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Code, TurnstileToken string }
	if decode(r, &in) != nil {
		fail(w, 400, "请求格式错误")
		return
	}
	var e error
	in.Email, e = normalizedEmail(in.Email)
	if e != nil {
		fail(w, 400, e)
		return
	}
	var u store.User
	var t string
	emailSettings := s.effectiveEmailVerification()
	if emailVerificationAvailable(emailSettings) {
		if len(in.Code) != 6 {
			fail(w, 400, "请输入 6 位邮箱验证码")
			return
		}
		u, t, e = s.Store.RegisterVerified(in.Email, in.Password, in.Code)
	} else {
		if s.C.TurnstileSiteKey != "" && s.C.TurnstileSecret != "" {
			if err := s.Turnstile.Verify(r.Context(), in.TurnstileToken, remoteIP(r)); err != nil {
				fail(w, 403, "请先完成人机验证")
				return
			}
		}
		u, t, e = s.Store.Register(in.Email, in.Password)
	}
	if e != nil {
		fail(w, 400, e)
		return
	}
	setSession(w, t, strings.HasPrefix(s.C.BaseURL, "https://"))
	jsonOut(w, 201, u)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if decode(r, &in) != nil {
		fail(w, 400, "请求格式错误")
		return
	}
	u, t, e := s.Store.Login(strings.ToLower(strings.TrimSpace(in.Email)), in.Password)
	if e != nil {
		fail(w, 401, e)
		return
	}
	setSession(w, t, strings.HasPrefix(s.C.BaseURL, "https://"))
	jsonOut(w, 200, u)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("session"); e == nil {
		_ = s.Store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1, HttpOnly: true})
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (s *Server) me(w http.ResponseWriter, r *http.Request, u store.User) {
	liveSMSPurchaseEnabled := s.C.PayProvider != "sandbox" || s.C.AllowLiveSMSInSandbox
	pricing := s.effectivePricing()
	jsonOut(w, 200, map[string]any{"user": u, "pricing": map[string]any{"markupCNY": pricing.Markup, "usdCnyRate": pricing.USDCNY}, "paymentProvider": s.C.PayProvider, "liveSmsPurchaseEnabled": liveSMSPurchaseEnabled, "autoReplaceMax": s.C.AutoReplaceMax})
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request, u store.User) {
	profile, err := s.Store.UserProfile(u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	orders, err := s.Store.ListSMS(u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"profile": profile, "orders": orders})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request, u store.User) {
	s.serveCatalog(w, r)
}

func (s *Server) publicCatalog(w http.ResponseWriter, r *http.Request) {
	s.serveCatalog(w, r)
}

func (s *Server) serveCatalog(w http.ResponseWriter, r *http.Request) {
	country := r.URL.Query().Get("country")
	if country != "" && s.countryBlocked(country) {
		fail(w, 403, "selected country is unavailable")
		return
	}
	cacheKey := strings.TrimSpace(country)
	snapshot, ok := s.Catalog.Get(cacheKey)
	var e error
	if !ok {
		snapshot, e = s.loadCatalog(r.Context(), country)
		if e == nil {
			s.Catalog.Set(cacheKey, snapshot)
		}
	}
	if e != nil {
		if stale, staleOK := s.Catalog.GetStale(cacheKey); staleOK {
			snapshot = stale
		} else {
			jsonOut(w, 200, map[string]any{"countries": []hero.Country{}, "services": []hero.Service{}, "offers": []any{}})
			return
		}
	}
	blocked := s.blockedCountries()
	if len(blocked) > 0 {
		filteredCountries := make([]hero.Country, 0, len(snapshot.Countries))
		for _, item := range snapshot.Countries {
			if !blocked[strconv.Itoa(item.ID)] {
				filteredCountries = append(filteredCountries, item)
			}
		}
		snapshot.Countries = filteredCountries
		for service, quote := range snapshot.Quotes {
			if blocked[strings.TrimSpace(quote.Country)] {
				delete(snapshot.Quotes, service)
			}
		}
	}
	type priced struct {
		Service  string `json:"service"`
		Country  string `json:"country"`
		Count    int    `json:"count"`
		PriceFen int64  `json:"priceFen"`
	}
	po := make([]priced, 0, len(snapshot.Quotes))
	markup := s.effectivePricing().Markup
	for _, quote := range snapshot.Quotes {
		po = append(po, priced{Service: quote.Service, Country: quote.Country, Count: quote.Count, PriceFen: quote.priceFen(markup)})
	}
	jsonOut(w, 200, map[string]any{"countries": snapshot.Countries, "services": snapshot.Services, "offers": po})
}
func (s *Server) orders(w http.ResponseWriter, r *http.Request, u store.User) {
	x, e := s.Store.ListSMS(u.ID)
	if e != nil {
		fail(w, 500, e)
		return
	}
	for i := range x {
		x[i] = s.syncOrderForUser(r.Context(), u.ID, x[i])
	}
	jsonOut(w, 200, x)
}
func (s *Server) purchase(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.C.PayProvider == "sandbox" && !s.C.AllowLiveSMSInSandbox {
		fail(w, http.StatusServiceUnavailable, "sandbox payments cannot purchase live HeroSMS numbers")
		return
	}
	var in struct {
		Country, Service string
		PayType          int `json:"payType"`
	}
	if decode(r, &in) != nil || in.Service == "" {
		fail(w, 400, "请选择服务")
		return
	}
	if in.Country != "" && s.countryBlocked(in.Country) {
		fail(w, 403, "selected country is unavailable")
		return
	}
	snapshot, e := s.loadCatalog(r.Context(), in.Country)
	if e != nil {
		fail(w, 502, e)
		return
	}
	if blocked := s.blockedCountries(); len(blocked) > 0 {
		for service, quote := range snapshot.Quotes {
			if blocked[strings.TrimSpace(quote.Country)] {
				delete(snapshot.Quotes, service)
			}
		}
	}
	quote, ok := snapshot.Quotes[in.Service]
	if !ok || quote.Count <= 0 {
		fail(w, 409, "该服务暂时无库存")
		return
	}
	if in.PayType == 0 {
		in.PayType = 2
	}
	if in.PayType != 1 && in.PayType != 2 && in.PayType != 3 && in.PayType != 11 {
		fail(w, 400, "unsupported payment method")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	selectedCountry := in.Country
	if selectedCountry == "" {
		selectedCountry = quote.Country
	}
	if s.countryBlocked(selectedCountry) {
		fail(w, 403, "selected country is unavailable")
		return
	}
	selectedCountryName := selectedCountry
	for _, country := range snapshot.Countries {
		if strconv.Itoa(country.ID) == selectedCountry {
			selectedCountryName = strings.TrimSpace(country.Chn)
			if selectedCountryName == "" {
				selectedCountryName = strings.TrimSpace(country.Eng)
			}
			if selectedCountryName == "" {
				selectedCountryName = selectedCountry
			}
			break
		}
	}
	o := store.SMSOrder{ID: store.ID("S"), UserID: u.ID, UpstreamProvider: quote.Provider, UpstreamCountry: quote.ProviderCountry, UpstreamService: quote.ProviderService, Country: selectedCountry, CountryName: selectedCountryName, Service: in.Service, UpstreamCost: quote.Cost, PriceFen: quote.priceFen(s.effectivePricing().Markup), AutoReplace: false, CreatedAt: now}
	raw, _ := store.Token()
	payment := store.Recharge{ID: store.ID("P"), UserID: u.ID, AmountFen: o.PriceFen, Provider: s.C.PayProvider, PayType: strconv.Itoa(in.PayType), Token: raw, Reference: o.ID, CreatedAt: now}
	if e = s.Store.CreateSMSPayment(u, o, payment); e != nil {
		fail(w, 500, e)
		return
	}
	if s.C.PayProvider == "sandbox" {
		jsonOut(w, 201, map[string]any{"id": o.ID, "paymentId": payment.ID, "priceFen": o.PriceFen, "checkoutUrl": fmt.Sprintf("/sandbox/pay/%s?token=%s", payment.ID, url.QueryEscape(raw))})
		return
	}
	if s.C.PayProvider == "epay" || s.C.PayProvider == "50pay" {
		checkoutURL, err := s.EPay.CheckoutURL(payment.ID, o.PriceFen, in.PayType, s.C.BaseURL+"/api/payments/epay/notify", s.C.BaseURL+"/api/payments/epay/return")
		if err != nil {
			_ = s.Store.SetRechargeStatus(payment.ID, "failed")
			fail(w, 502, err)
			return
		}
		jsonOut(w, 201, map[string]any{"id": o.ID, "paymentId": payment.ID, "priceFen": o.PriceFen, "checkoutUrl": checkoutURL})
		return
	}
	if s.C.PayProvider != "yishoumi" || s.C.YSMAppID == "" || s.C.YSMSecret == "" {
		_ = s.Store.SetRechargeStatus(payment.ID, "failed")
		fail(w, 503, "payment provider is not configured")
		return
	}
	out, e := s.YSM.Create(r.Context(), payment.ID, o.PriceFen, in.PayType, s.C.BaseURL+"/api/payments/yishoumi/notify", s.C.BaseURL+"/?order="+o.ID)
	if e != nil {
		_ = s.Store.SetRechargeStatus(payment.ID, "failed")
		fail(w, 502, e)
		return
	}
	jsonOut(w, 201, map[string]any{"id": o.ID, "paymentId": payment.ID, "priceFen": o.PriceFen, "checkoutUrl": out.URL})
}
func (s *Server) orderStatus(w http.ResponseWriter, r *http.Request, u store.User) {
	o, e := s.Store.GetSMS(r.PathValue("id"), u.ID)
	if e != nil {
		fail(w, 404, "?????")
		return
	}
	o = s.syncOrderForUser(r.Context(), u.ID, o)
	jsonOut(w, 200, o)
}

func (s *Server) syncOrderForUser(ctx context.Context, userID int64, order store.SMSOrder) store.SMSOrder {
	if order.Status == "paid" {
		s.fulfillPaidOrder(ctx, order)
		if refreshed, err := s.Store.GetSMS(order.ID, userID); err == nil {
			order = refreshed
		}
	}
	if order.UpstreamID != "" && (order.Status == "waiting" || order.Status == "replacing") {
		status, code, err := s.providerStatus(ctx, order)
		if err == nil && status != "" && (status != order.Status || (code != "" && code != order.Code)) {
			_ = s.Store.UpdateSMS(order.ID, status, code)
			if refreshed, refreshErr := s.Store.GetSMS(order.ID, userID); refreshErr == nil {
				order = refreshed
			}
		}
	}
	return order
}

func (s *Server) orderCheckout(w http.ResponseWriter, r *http.Request, u store.User) {
	o, err := s.Store.GetSMS(r.PathValue("id"), u.ID)
	if err != nil {
		fail(w, 404, "订单不存在")
		return
	}
	if o.Status != "awaiting_payment" {
		fail(w, 409, "order is not awaiting payment")
		return
	}
	recharge, err := s.Store.GetRechargeByReference(o.ID)
	if err != nil {
		fail(w, 404, "payment order not found")
		return
	}
	if recharge.Status != "pending" {
		fail(w, 409, "payment is no longer pending")
		return
	}
	if s.C.PayProvider == "sandbox" {
		jsonOut(w, 200, map[string]any{"checkoutUrl": fmt.Sprintf("/sandbox/pay/%s?token=%s", recharge.ID, url.QueryEscape(recharge.Token))})
		return
	}
	if s.C.PayProvider == "epay" || s.C.PayProvider == "50pay" {
		payType, convErr := strconv.Atoi(strings.TrimSpace(recharge.PayType))
		if convErr != nil {
			fail(w, 500, convErr)
			return
		}
		checkoutURL, checkoutErr := s.EPay.CheckoutURL(recharge.ID, recharge.AmountFen, payType, s.C.BaseURL+"/api/payments/epay/notify", s.C.BaseURL+"/api/payments/epay/return")
		if checkoutErr != nil {
			fail(w, 502, checkoutErr)
			return
		}
		jsonOut(w, 200, map[string]any{"checkoutUrl": checkoutURL})
		return
	}
	fail(w, 409, "continue payment is unavailable for this provider")
}
func parseHeroStatus(st string) (string, string) {
	p := strings.SplitN(st, ":", 2)
	switch p[0] {
	case "STATUS_OK":
		if len(p) == 2 {
			return "code_received", p[1]
		}
		return "code_received", ""
	case "STATUS_WAIT_CODE", "STATUS_WAIT_RETRY", "STATUS_WAIT_RESEND":
		return "waiting", ""
	case "STATUS_CANCEL":
		return "cancelled", ""
	}
	return strings.ToLower(strings.TrimPrefix(p[0], "STATUS_")), ""
}
func (s *Server) finishOrder(w http.ResponseWriter, r *http.Request, u store.User) {
	o, e := s.Store.GetSMS(r.PathValue("id"), u.ID)
	if e != nil {
		fail(w, 404, "?????")
		return
	}
	o = s.syncOrderForUser(r.Context(), u.ID, o)
	if o.Status != "waiting" && o.Status != "code_received" {
		fail(w, 409, "order cannot be completed in its current state")
		return
	}
	if o.UpstreamProvider == "smsman" {
		fail(w, 409, "finish is unavailable for this provider")
		return
	}
	if o.UpstreamID == "" {
		fail(w, 409, "upstream activation is missing")
		return
	}
	result, err := s.Hero.SetStatus(r.Context(), o.UpstreamID, "6")
	if err != nil {
		fail(w, 409, err)
		return
	}
	normalized := strings.ToUpper(strings.Trim(strings.TrimSpace(result), `"`))
	if normalized != "ACCESS_READY" && normalized != "ACCESS_ACTIVATION" && normalized != "STATUS_OK" && normalized != "FINISHED" && !strings.Contains(normalized, "READY") {
		fail(w, 409, "upstream did not confirm completion")
		return
	}
	_ = s.Store.EndCurrentAttemptWithProvider(o.ID, o.UpstreamProvider, o.UpstreamID, "finished")
	if err = s.Store.SetSMSOrderStatus(o.ID, "finished"); err != nil {
		fail(w, 500, err)
		return
	}
	updated, err := s.Store.GetSMS(o.ID, u.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, updated)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request, u store.User) {
	o, e := s.Store.GetSMS(r.PathValue("id"), u.ID)
	if e != nil {
		fail(w, 404, "订单不存在")
		return
	}
	if o.Code != "" {
		fail(w, 409, "已收到验证码，不能取消")
		return
	}
	if direct, err := s.Store.IsSMSPaymentOrder(o.ID); err != nil {
		fail(w, 500, err)
		return
	} else if direct {
		fail(w, 409, "按单支付订单将持续换号，暂不支持手动退款取消")
		return
	}
	if o.Status != "waiting" {
		fail(w, 409, "order is not cancellable in its current state")
		return
	}
	cancelled, e := s.cancelUpstream(r.Context(), o)
	if e != nil {
		fail(w, 409, e)
		return
	}
	if !cancelled {
		fail(w, 409, "上游未确认取消")
		return
	}
	_ = s.Store.EndCurrentAttemptWithProvider(o.ID, o.UpstreamProvider, o.UpstreamID, "cancelled")
	if e = s.Store.SetSMSOrderStatus(o.ID, "cancelled"); e != nil {
		fail(w, 500, e)
		return
	}
	recharge, rechargeErr := s.Store.GetRechargeByReference(o.ID)
	if rechargeErr == nil && (recharge.Provider == "epay" || recharge.Provider == "50pay") && recharge.Status == "paid" && recharge.RefundedAt == "" {
		policy := s.effectiveRefundPolicy()
		since := time.Now().UTC().Add(-time.Duration(policy.WindowMinutes) * time.Minute).Format(time.RFC3339)
		recentRefunds, countErr := s.Store.CountRecentRefundsByUser(o.UserID, since)
		if countErr != nil {
			fail(w, 500, countErr)
			return
		}
		if policy.MaxCount >= 0 && recentRefunds >= policy.MaxCount {
			jsonOut(w, 200, map[string]any{"cancelled": true, "refunded": false, "reason": "refund_threshold_reached"})
			return
		}
		if recharge.ProviderID == "" {
			fail(w, 409, "payment transaction id is missing")
			return
		}
		refundID := store.ID("R")
		refund, refundErr := s.EPay.Refund(recharge.ProviderID, recharge.AmountFen, refundID)
		if refundErr != nil {
			fail(w, 409, refundErr)
			return
		}
		if e = s.Store.MarkRechargeRefunded(recharge.ID, refund.OutRefundNo); e != nil {
			fail(w, 500, e)
			return
		}
		jsonOut(w, 200, map[string]any{"cancelled": true, "refunded": true, "refundId": refund.OutRefundNo})
		return
	}
	jsonOut(w, 200, map[string]any{"cancelled": true, "refunded": false})
}

func (s *Server) manualReplace(w http.ResponseWriter, r *http.Request, u store.User) {
	o, e := s.Store.GetSMS(r.PathValue("id"), u.ID)
	if e != nil {
		fail(w, 404, "订单不存在")
		return
	}
	if o.Code != "" {
		fail(w, 409, "已收到验证码，不能再换号")
		return
	}
	if o.Status != "waiting" {
		fail(w, 409, "当前状态不支持手动换号")
		return
	}
	if claimed, err := s.Store.ClaimAutoReplace(o.ID, o.UpstreamID); err != nil || !claimed {
		if err != nil {
			fail(w, 500, err)
			return
		}
		fail(w, 409, "当前订单暂时无法换号")
		return
	}
	cancelled, e := s.cancelUpstream(r.Context(), o)
	if e != nil {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		fail(w, 409, e)
		return
	}
	if !cancelled {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		fail(w, 409, "上游未确认取消，暂时不能换号")
		return
	}
	_ = s.Store.EndCurrentAttemptWithProvider(o.ID, o.UpstreamProvider, o.UpstreamID, "cancelled")
	act, err := s.acquireNumber(r.Context(), o)
	if err != nil {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		fail(w, 409, err)
		return
	}
	if err = s.Store.ReplaceActivationWithProvider(o.ID, o.UpstreamProvider, o.UpstreamID, o.UpstreamProvider, act.ID, act.Phone, act.Cost); err != nil {
		fail(w, 500, err)
		return
	}
	updated, err := s.Store.GetSMS(o.ID, u.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, updated)
}

func (s *Server) RunAutoReplace(ctx context.Context) {
	ticker := time.NewTicker(s.C.AutoReplaceScan)
	defer ticker.Stop()
	s.runExpiredPaymentCleanup()
	s.runPaidOrderBatch(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runExpiredPaymentCleanup()
			s.runPaidOrderBatch(ctx)
		}
	}
}

func (s *Server) runExpiredPaymentCleanup() {
	before := time.Now().UTC().Add(-s.C.PaymentOrderTTL).Format(time.RFC3339)
	deleted, err := s.Store.DeleteExpiredUnpaidSMS(before, 100)
	if err != nil {
		log.Printf("expired payment cleanup failed: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("deleted %d expired unpaid SMS orders", deleted)
	}
}

func (s *Server) runPaidOrderBatch(ctx context.Context) {
	orders, err := s.Store.ListPaidSMS(20)
	if err != nil {
		log.Printf("paid SMS order scan failed: %v", err)
		return
	}
	for _, o := range orders {
		s.fulfillPaidOrder(ctx, o)
	}
}

func (s *Server) fulfillPaidOrder(ctx context.Context, o store.SMSOrder) {
	claimed, err := s.Store.ClaimPaidSMS(o.ID)
	if err != nil || !claimed {
		return
	}
	act, err := s.acquireNumber(ctx, o)
	if err != nil {
		_ = s.Store.ReleasePaidSMS(o.ID)
		log.Printf("paid SMS order %s is waiting for inventory: %v", o.ID, err)
		return
	}
	if err = s.Store.ActivateSMSWithProvider(o.ID, o.UpstreamProvider, act.ID, act.Phone, act.Cost); err != nil {
		_ = s.Store.ReleasePaidSMS(o.ID)
		log.Printf("paid SMS order %s activation persistence failed: %v", o.ID, err)
	}
}

func (s *Server) runReplacingBatch(ctx context.Context) {
	orders, err := s.Store.ListReplacing(20)
	if err != nil {
		log.Printf("replacing SMS order scan failed: %v", err)
		return
	}
	for _, o := range orders {
		lastAttempt, err := time.Parse(time.RFC3339, o.LastNumberAt)
		if err == nil && time.Since(lastAttempt) < 30*time.Second {
			continue
		}
		s.replaceNumber(ctx, o)
	}
}

func (s *Server) runAutoReplaceBatch(ctx context.Context) {
	before := time.Now().UTC().Add(-s.C.AutoReplaceAfter).Format(time.RFC3339)
	orders, err := s.Store.ListDueAutoReplace(before, s.C.AutoReplaceMax, 20)
	if err != nil {
		log.Printf("auto replace scan failed: %v", err)
		return
	}
	for _, o := range orders {
		claimed, err := s.Store.ClaimAutoReplace(o.ID, o.UpstreamID)
		if err != nil || !claimed {
			continue
		}
		s.replaceNumber(ctx, o)
	}
}

func (s *Server) replaceNumber(ctx context.Context, o store.SMSOrder) {
	status, code, err := s.providerStatus(ctx, o)
	if err != nil {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		return
	}
	if status == "cancelled" {
		s.acquireReplacement(ctx, o)
		return
	}
	if code != "" || status != "waiting" {
		_ = s.Store.UpdateSMS(o.ID, status, code)
		return
	}
	cancelled, err := s.cancelUpstream(ctx, o)
	if err != nil {
		switch hero.ErrorCode(err) {
		case "OTP_RECEIVED", "NEW_OTP_RECEIVED":
			if latest, e := s.Hero.Status(ctx, o.UpstreamID); e == nil {
				status, code = parseHeroStatus(latest)
				_ = s.Store.UpdateSMS(o.ID, status, code)
				return
			}
		case "FREE_CANCELLATION_EXPIRED":
			_ = s.Store.ReleaseAutoReplace(o.ID, true)
			return
		}
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		return
	}
	if !cancelled {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		return
	}
	s.acquireReplacement(ctx, o)
}

func (s *Server) acquireReplacement(ctx context.Context, o store.SMSOrder) {
	_ = s.Store.EndCurrentAttemptWithProvider(o.ID, o.UpstreamProvider, o.UpstreamID, "cancelled")
	act, err := s.acquireNumber(ctx, o)
	if err != nil {
		_ = s.Store.TouchReplacing(o.ID)
		log.Printf("auto replace waiting for inventory for %s: %v", o.ID, err)
		return
	}
	if err = s.Store.ReplaceActivationWithProvider(o.ID, o.UpstreamProvider, o.UpstreamID, o.UpstreamProvider, act.ID, act.Phone, act.Cost); err != nil {
		log.Printf("auto replace persistence failed for %s: %v", o.ID, err)
	}
}

func (s *Server) sandboxPay(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.GetRecharge(r.PathValue("id"))
	if e != nil || x.Provider != "sandbox" || x.Reference == "" || x.Token != r.URL.Query().Get("token") {
		http.Error(w, "invalid order", 404)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>沙箱付款</title><style>body{font:16px system-ui;display:grid;place-items:center;height:100vh;background:#f4f7fb}.box{background:white;padding:36px;border-radius:18px;box-shadow:0 20px 60px #2342}button{padding:12px 24px;background:#2563eb;color:white;border:0;border-radius:9px}</style><form class=box method=post><h2>沙箱付款</h2><p>订单 %s</p><p>应付 ¥%.2f</p><input type=hidden name=token value="%s"><button>确认付款</button></form>`, x.ID, float64(x.AmountFen)/100, x.Token)
}
func (s *Server) sandboxComplete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	x, e := s.Store.GetRecharge(r.PathValue("id"))
	if e != nil || x.Provider != "sandbox" || x.Reference == "" || x.Token != r.Form.Get("token") {
		http.Error(w, "invalid order", 400)
		return
	}
	orderID, err := s.Store.CompleteSMSPayment(x.ID, "")
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if order, err := s.Store.GetSMSByID(orderID); err == nil {
		s.fulfillPaidOrder(r.Context(), order)
	}
	http.Redirect(w, r, "/?order="+orderID, http.StatusSeeOther)
}
func (s *Server) ysmNotify(w http.ResponseWriter, r *http.Request) {
	if s.C.PayProvider != "yishoumi" || s.C.YSMAppID == "" || s.C.YSMSecret == "" {
		http.Error(w, "payment provider is not configured", http.StatusServiceUnavailable)
		return
	}
	if e := r.ParseForm(); e != nil || !yishoumi.Verify(r.PostForm, s.C.YSMSecret) {
		http.Error(w, "bad sign", 400)
		return
	}
	if r.PostForm.Get("appid") != s.C.YSMAppID || r.PostForm.Get("state") != "SUCCESS" {
		http.Error(w, "bad state", 400)
		return
	}
	x, e := s.Store.GetRecharge(r.PostForm.Get("mch_orderid"))
	if e != nil || x.Provider != "yishoumi" || x.Reference == "" {
		http.Error(w, "unknown order", 404)
		return
	}
	amount, e := strconv.ParseInt(r.PostForm.Get("total_fee"), 10, 64)
	if e != nil || amount != x.AmountFen {
		http.Error(w, "amount mismatch", 400)
		return
	}
	providerID := r.PostForm.Get("ysm_orderid")
	if providerID == "" {
		http.Error(w, "missing provider order", 400)
		return
	}
	orderID, err := s.Store.CompleteSMSPayment(x.ID, providerID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if order, err := s.Store.GetSMSByID(orderID); err == nil {
		s.fulfillPaidOrder(r.Context(), order)
	}
	w.Write([]byte("success"))
}

func (s *Server) epayNotify(w http.ResponseWriter, r *http.Request) {
	orderID, err := s.completeEpayPayment(r)
	if err != nil {
		log.Printf("50Pay notify rejected: %v", err)
		http.Error(w, "fail", http.StatusBadRequest)
		return
	}
	if order, err := s.Store.GetSMSByID(orderID); err == nil {
		s.fulfillPaidOrder(r.Context(), order)
	}
	w.Write([]byte("success"))
}

func (s *Server) epayReturn(w http.ResponseWriter, r *http.Request) {
	orderID, err := s.completeEpayPayment(r)
	if err != nil {
		http.Error(w, "payment verification failed", http.StatusBadRequest)
		return
	}
	if order, err := s.Store.GetSMSByID(orderID); err == nil {
		s.fulfillPaidOrder(r.Context(), order)
	}
	http.Redirect(w, r, "/?order="+url.QueryEscape(orderID), http.StatusSeeOther)
}

func (s *Server) completeEpayPayment(r *http.Request) (string, error) {
	if s.C.PayProvider != "epay" && s.C.PayProvider != "50pay" {
		return "", fmt.Errorf("50Pay is not enabled")
	}
	values := r.URL.Query()
	if values.Get("pid") != s.C.EPayPID || !strings.EqualFold(values.Get("sign_type"), "MD5") || !epay.Verify(values, s.C.EPayKey) {
		return "", fmt.Errorf("invalid 50Pay signature")
	}
	if values.Get("trade_status") != "TRADE_SUCCESS" {
		return "", fmt.Errorf("unexpected 50Pay status")
	}
	payment, err := s.Store.GetRecharge(values.Get("out_trade_no"))
	if err != nil || payment.Reference == "" || payment.Provider != s.C.PayProvider {
		return "", fmt.Errorf("unknown 50Pay order")
	}
	amountFen, err := epay.ParseMoneyFen(values.Get("money"))
	if err != nil || amountFen != payment.AmountFen {
		return "", fmt.Errorf("50Pay amount mismatch")
	}
	tradeNo := values.Get("trade_no")
	if tradeNo == "" {
		return "", fmt.Errorf("missing 50Pay transaction")
	}
	return s.Store.CompleteSMSPayment(payment.ID, tradeNo)
}
