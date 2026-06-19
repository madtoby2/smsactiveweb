package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sms-platform/internal/config"
	"sms-platform/internal/hero"
	"sms-platform/internal/pricing"
	"sms-platform/internal/store"
	"sms-platform/internal/yishoumi"
)

//go:embed public/*
var assets embed.FS

type Server struct {
	C     config.Config
	Store *store.Store
	Hero  *hero.Client
	YSM   *yishoumi.Client
}
type handler func(http.ResponseWriter, *http.Request, store.User)

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
	if c.AutoReplaceScan < time.Second {
		c.AutoReplaceScan = 10 * time.Second
	}
	return &Server{c, s, hero.New(c.HeroKey, c.HeroURL, c.HeroCurrency), yishoumi.New(c.YSMAppID, c.YSMSecret, c.YSMURL)}
}
func (s *Server) Routes() http.Handler {
	m := http.NewServeMux()
	sub, _ := fs.Sub(assets, "public")
	m.HandleFunc("GET /healthz", s.health)
	m.Handle("/", http.FileServer(http.FS(sub)))
	m.HandleFunc("POST /api/auth/register", s.register)
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/auth/logout", s.logout)
	m.HandleFunc("GET /api/me", s.auth(s.me))
	m.HandleFunc("GET /api/catalog", s.auth(s.catalog))
	m.HandleFunc("GET /api/orders", s.auth(s.orders))
	m.HandleFunc("POST /api/orders", s.auth(s.purchase))
	m.HandleFunc("GET /api/orders/{id}", s.auth(s.orderStatus))
	m.HandleFunc("POST /api/orders/{id}/cancel", s.auth(s.cancel))
	m.HandleFunc("GET /sandbox/pay/{id}", s.sandboxPay)
	m.HandleFunc("POST /sandbox/pay/{id}", s.sandboxComplete)
	m.HandleFunc("POST /api/payments/yishoumi/notify", s.ysmNotify)
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
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
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if decode(r, &in) != nil {
		fail(w, 400, "请求格式错误")
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(in.Email, "@") {
		fail(w, 400, "邮箱格式错误")
		return
	}
	u, t, e := s.Store.Register(in.Email, in.Password)
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
	jsonOut(w, 200, map[string]any{"user": u, "pricing": map[string]any{"markupCNY": s.C.Markup, "usdCnyRate": s.C.USDCNY}, "paymentProvider": s.C.PayProvider, "liveSmsPurchaseEnabled": liveSMSPurchaseEnabled, "autoReplaceMax": s.C.AutoReplaceMax})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request, u store.User) {
	country := r.URL.Query().Get("country")
	countries, e := s.Hero.Countries(r.Context())
	if e != nil {
		fail(w, 502, e)
		return
	}
	if country == "" {
		jsonOut(w, 200, map[string]any{"countries": countries})
		return
	}
	services, e := s.Hero.Services(r.Context(), country)
	if e != nil {
		fail(w, 502, e)
		return
	}
	offers, e := s.Hero.Offers(r.Context(), country)
	if e != nil {
		fail(w, 502, e)
		return
	}
	type priced struct {
		hero.Offer
		PriceFen int64 `json:"priceFen"`
	}
	po := make([]priced, 0, len(offers))
	for _, o := range offers {
		po = append(po, priced{o, pricing.SaleFen(o.Cost, s.C.USDCNY, s.C.Markup)})
	}
	jsonOut(w, 200, map[string]any{"countries": countries, "services": services, "offers": po})
}
func (s *Server) orders(w http.ResponseWriter, r *http.Request, u store.User) {
	x, e := s.Store.ListSMS(u.ID)
	if e != nil {
		fail(w, 500, e)
		return
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
	if decode(r, &in) != nil || in.Country == "" || in.Service == "" {
		fail(w, 400, "请选择国家和服务")
		return
	}
	offers, e := s.Hero.Offers(r.Context(), in.Country)
	if e != nil {
		fail(w, 502, e)
		return
	}
	var offer *hero.Offer
	for i := range offers {
		if offers[i].Service == in.Service && offers[i].Count > 0 {
			offer = &offers[i]
			break
		}
	}
	if offer == nil {
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
	o := store.SMSOrder{ID: store.ID("S"), UserID: u.ID, Country: in.Country, Service: in.Service, UpstreamCost: offer.Cost, PriceFen: pricing.SaleFen(offer.Cost, s.C.USDCNY, s.C.Markup), AutoReplace: true, CreatedAt: now}
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
		fail(w, 404, "订单不存在")
		return
	}
	if o.Status == "paid" {
		s.fulfillPaidOrder(r.Context(), o)
		o, _ = s.Store.GetSMS(o.ID, u.ID)
	}
	if o.UpstreamID != "" && o.Status == "waiting" {
		st, e := s.Hero.Status(r.Context(), o.UpstreamID)
		if e == nil {
			status, code := parseHeroStatus(st)
			_ = s.Store.UpdateSMS(o.ID, status, code)
			o, _ = s.Store.GetSMS(o.ID, u.ID)
		}
	}
	jsonOut(w, 200, o)
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
	result, e := s.Hero.SetStatus(r.Context(), o.UpstreamID, "8")
	if e != nil {
		fail(w, 409, e)
		return
	}
	if !strings.Contains(result, "CANCEL") && !strings.Contains(result, "ACCESS") {
		fail(w, 409, "上游未确认取消")
		return
	}
	_ = s.Store.EndCurrentAttempt(o.ID, o.UpstreamID, "cancelled")
	if e = s.Store.RefundSMS(o.ID, "cancelled"); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]bool{"refunded": true})
}

func (s *Server) RunAutoReplace(ctx context.Context) {
	if orders, err := s.Store.ListReplacing(20); err != nil {
		log.Printf("auto replace recovery scan failed: %v", err)
	} else {
		for _, o := range orders {
			s.replaceNumber(ctx, o)
		}
	}
	ticker := time.NewTicker(s.C.AutoReplaceScan)
	defer ticker.Stop()
	s.runPaidOrderBatch(ctx)
	s.runAutoReplaceBatch(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runPaidOrderBatch(ctx)
			s.runAutoReplaceBatch(ctx)
			s.runReplacingBatch(ctx)
		}
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
	act, err := s.Hero.Acquire(ctx, o.Country, o.Service, o.UpstreamCost)
	if err != nil {
		_ = s.Store.ReleasePaidSMS(o.ID)
		log.Printf("paid SMS order %s is waiting for inventory: %v", o.ID, err)
		return
	}
	if err = s.Store.ActivateSMS(o.ID, act.ID, act.Phone, act.Cost); err != nil {
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
	st, err := s.Hero.Status(ctx, o.UpstreamID)
	if err != nil {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		return
	}
	status, code := parseHeroStatus(st)
	if status == "cancelled" {
		s.acquireReplacement(ctx, o)
		return
	}
	if code != "" || status != "waiting" {
		_ = s.Store.UpdateSMS(o.ID, status, code)
		return
	}
	result, err := s.Hero.SetStatus(ctx, o.UpstreamID, "8")
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
	if !strings.Contains(result, "CANCEL") && !strings.Contains(result, "ACCESS") {
		_ = s.Store.ReleaseAutoReplace(o.ID, false)
		return
	}
	s.acquireReplacement(ctx, o)
}

func (s *Server) acquireReplacement(ctx context.Context, o store.SMSOrder) {
	_ = s.Store.EndCurrentAttempt(o.ID, o.UpstreamID, "cancelled")
	act, err := s.Hero.Acquire(ctx, o.Country, o.Service, o.UpstreamCost)
	if err != nil {
		_ = s.Store.TouchReplacing(o.ID)
		log.Printf("auto replace waiting for inventory for %s: %v", o.ID, err)
		return
	}
	if err = s.Store.ReplaceActivation(o.ID, o.UpstreamID, act.ID, act.Phone, act.Cost); err != nil {
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
