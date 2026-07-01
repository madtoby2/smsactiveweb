package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/http"
	netmail "net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"sms-platform/internal/mailer"
	"sms-platform/internal/store"
)

var settingKeys = map[string]bool{
	"contactTitle":              true,
	"contactValue":              true,
	"contactURL":                true,
	"supportHours":              true,
	"markupCNY":                 true,
	"usdCnyRate":                true,
	"smsmanCnyRate":             true,
	"blockedCountries":          true,
	"blockedServices":           true,
	"refundWindowMinutes":       true,
	"refundMaxCount":            true,
	"mailProvider":              true,
	"smtpHost":                  true,
	"smtpPort":                  true,
	"smtpUser":                  true,
	"smtpPassword":              true,
	"smtpFrom":                  true,
	"resendApiKey":              true,
	"resendFrom":                true,
	"emailVerificationRequired": true,
}

type livePricing struct{ Markup, USDCNY, SMSManCNY float64 }
type refundPolicy struct{ WindowMinutes, MaxCount int }

type emailVerificationSettings struct {
	Enabled          bool
	Provider         string
	SMTPHost         string
	SMTPPort         int
	SMTPUser         string
	SMTPPassword     string
	SMTPFrom         string
	ResendAPIKey     string
	ResendFrom       string
	SMTPOverridden   bool
	ResendOverridden bool
}

func (s *Server) effectiveEmailVerification() emailVerificationSettings {
	settings := emailVerificationSettings{
		Enabled:      s.C.EmailVerificationRequired,
		Provider:     "smtp",
		SMTPHost:     s.C.SMTPHost,
		SMTPPort:     s.C.SMTPPort,
		SMTPUser:     s.C.SMTPUser,
		SMTPPassword: s.C.SMTPPassword,
		SMTPFrom:     s.C.SMTPFrom,
		ResendAPIKey: s.C.ResendAPIKey,
		ResendFrom:   s.C.ResendFrom,
	}
	if settings.ResendAPIKey != "" && settings.ResendFrom != "" {
		settings.Provider = "resend"
	}
	if settings.SMTPPort <= 0 {
		settings.SMTPPort = 587
	}
	values, err := s.Store.Settings()
	if err != nil {
		return settings
	}
	if value, ok := values["emailVerificationRequired"]; ok {
		settings.Enabled = value == "true"
	}
	if value, ok := values["mailProvider"]; ok && (value == "smtp" || value == "resend") {
		settings.Provider = value
	}
	for key, target := range map[string]*string{"smtpHost": &settings.SMTPHost, "smtpUser": &settings.SMTPUser, "smtpPassword": &settings.SMTPPassword, "smtpFrom": &settings.SMTPFrom} {
		if value, ok := values[key]; ok {
			*target = value
			settings.SMTPOverridden = true
		}
	}
	for key, target := range map[string]*string{"resendApiKey": &settings.ResendAPIKey, "resendFrom": &settings.ResendFrom} {
		if value, ok := values[key]; ok {
			*target = value
			settings.ResendOverridden = true
		}
	}
	if value, ok := values["smtpPort"]; ok {
		if port, parseErr := strconv.Atoi(value); parseErr == nil {
			settings.SMTPPort = port
		}
		settings.SMTPOverridden = true
	}
	return settings
}

func validateEmailVerificationSettings(settings emailVerificationSettings, turnstileReady bool) error {
	if settings.Provider != "smtp" && settings.Provider != "resend" {
		return fmt.Errorf("mailProvider must be smtp or resend")
	}
	if settings.Provider == "smtp" {
		if settings.SMTPPort < 1 || settings.SMTPPort > 65535 {
			return fmt.Errorf("smtpPort must be between 1 and 65535")
		}
		if settings.SMTPHost != "" && (strings.Contains(settings.SMTPHost, "://") || strings.ContainsAny(settings.SMTPHost, " /\\")) {
			return fmt.Errorf("smtpHost must be a hostname")
		}
		if settings.SMTPFrom != "" {
			if _, err := netmail.ParseAddress(settings.SMTPFrom); err != nil {
				return fmt.Errorf("smtpFrom must be a valid email address")
			}
		}
	}
	if settings.Provider == "resend" && settings.ResendFrom != "" {
		if _, err := netmail.ParseAddress(settings.ResendFrom); err != nil {
			return fmt.Errorf("resendFrom must be a valid email address")
		}
	}
	if settings.Enabled {
		if settings.Provider == "smtp" && (settings.SMTPHost == "" || settings.SMTPFrom == "") {
			return fmt.Errorf("SMTP host and sender are required before enabling email verification")
		}
		if settings.Provider == "resend" && (settings.ResendAPIKey == "" || settings.ResendFrom == "") {
			return fmt.Errorf("Resend API key and sender are required before enabling email verification")
		}
		if !turnstileReady {
			return fmt.Errorf("Cloudflare Turnstile must be configured before enabling email verification")
		}
	}
	return nil
}

func emailVerificationAvailable(settings emailVerificationSettings) bool {
	if !settings.Enabled {
		return false
	}
	if settings.Provider == "resend" {
		return settings.ResendAPIKey != "" && settings.ResendFrom != ""
	}
	return settings.SMTPHost != "" && settings.SMTPFrom != ""
}

func verificationSender(settings emailVerificationSettings) mailer.Sender {
	if settings.Provider == "resend" {
		return &mailer.Resend{APIKey: settings.ResendAPIKey, From: settings.ResendFrom}
	}
	return &mailer.SMTP{
		Host:     settings.SMTPHost,
		Port:     settings.SMTPPort,
		User:     settings.SMTPUser,
		Password: settings.SMTPPassword,
		From:     settings.SMTPFrom,
	}
}

func (s *Server) effectivePricing() livePricing {
	pricing := livePricing{Markup: s.C.Markup, USDCNY: s.C.USDCNY, SMSManCNY: s.C.SMSManCNYRate}
	values, err := s.Store.Settings()
	if err != nil {
		return pricing
	}
	for key, target := range map[string]*float64{"markupCNY": &pricing.Markup, "usdCnyRate": &pricing.USDCNY, "smsmanCnyRate": &pricing.SMSManCNY} {
		if value, parseErr := strconv.ParseFloat(values[key], 64); parseErr == nil && value > 0 {
			*target = value
		}
	}
	return pricing
}

func (s *Server) effectiveRefundPolicy() refundPolicy {
	policy := refundPolicy{WindowMinutes: 10, MaxCount: 3}
	values, err := s.Store.Settings()
	if err != nil {
		return policy
	}
	if value, err := strconv.Atoi(strings.TrimSpace(values["refundWindowMinutes"])); err == nil && value >= 1 && value <= 1440 {
		policy.WindowMinutes = value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(values["refundMaxCount"])); err == nil && value >= 0 && value <= 100 {
		policy.MaxCount = value
	}
	return policy
}

func (s *Server) admin(next adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err != nil || !s.Store.AdminSessionValid(cookie.Value) {
			fail(w, http.StatusUnauthorized, "admin login required")
			return
		}
		next(w, r)
	}
}

func secureEqual(a, b string) bool {
	aHash := sha256.Sum256([]byte(a))
	bHash := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aHash[:], bHash[:]) == 1
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var input struct{ Email, Password string }
	if decode(r, &input) != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	if s.C.AdminPassword == "" {
		fail(w, http.StatusServiceUnavailable, "admin login is not configured")
		return
	}
	if !secureEqual(strings.ToLower(strings.TrimSpace(input.Email)), s.C.AdminEmail) || !secureEqual(input.Password, s.C.AdminPassword) {
		fail(w, http.StatusUnauthorized, "invalid admin credentials")
		return
	}
	raw, _ := store.Token()
	if err := s.Store.CreateAdminSession(raw); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.Store.Audit("admin.login", s.C.AdminEmail, r.RemoteAddr)
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: strings.HasPrefix(s.C.BaseURL, "https://"), MaxAge: 12 * 60 * 60})
	jsonOut(w, http.StatusOK, map[string]string{"email": s.C.AdminEmail})
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("admin_session"); err == nil {
		_ = s.Store.DeleteAdminSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Path: "/", MaxAge: -1, HttpOnly: true})
	jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	today := time.Now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339)
	stats, orders, err := s.Store.AdminOverview(today)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	type balance struct {
		Available bool    `json:"available"`
		Amount    float64 `json:"amount"`
		Currency  string  `json:"currency"`
		Error     string  `json:"error,omitempty"`
	}
	balances := map[string]balance{}
	if s.C.HeroKey != "" {
		value, balanceErr := s.Hero.Balance(r.Context())
		item := balance{Available: balanceErr == nil, Amount: value, Currency: "USD"}
		if balanceErr != nil {
			item.Error = balanceErr.Error()
		}
		balances["hero"] = item
	}
	if s.C.SMSManToken != "" {
		value, balanceErr := s.SMSMan.Balance(r.Context())
		item := balance{Available: balanceErr == nil, Amount: value, Currency: "RUB"}
		if balanceErr != nil {
			item.Error = balanceErr.Error()
		}
		balances["smsman"] = item
	}
	jsonOut(w, http.StatusOK, map[string]any{"stats": stats, "orders": orders, "balances": balances})
}

func defaultSettings(values map[string]string) map[string]string {
	defaults := map[string]string{"contactTitle": "在线客服", "contactValue": "请通过客服聊天联系我们", "contactURL": "", "supportHours": "每日 09:00 - 23:00"}
	for key := range settingKeys {
		if value, exists := values[key]; exists && (key == "contactTitle" || key == "contactValue" || key == "contactURL" || key == "supportHours") {
			defaults[key] = value
		}
	}
	return defaults
}

func (s *Server) publicSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.Store.Settings()
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, http.StatusOK, defaultSettings(values))
}

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.Store.Settings()
	if err != nil {
		fail(w, 500, err)
		return
	}
	out := defaultSettings(values)
	pricing := s.effectivePricing()
	out["markupCNY"] = strconv.FormatFloat(pricing.Markup, 'f', -1, 64)
	out["usdCnyRate"] = strconv.FormatFloat(pricing.USDCNY, 'f', -1, 64)
	out["smsmanCnyRate"] = strconv.FormatFloat(pricing.SMSManCNY, 'f', -1, 64)
	out["blockedCountries"] = values["blockedCountries"]
	out["blockedServices"] = values["blockedServices"]
	refund := s.effectiveRefundPolicy()
	out["refundWindowMinutes"] = strconv.Itoa(refund.WindowMinutes)
	out["refundMaxCount"] = strconv.Itoa(refund.MaxCount)
	email := s.effectiveEmailVerification()
	out["mailProvider"] = email.Provider
	out["smtpHost"] = email.SMTPHost
	out["smtpPort"] = strconv.Itoa(email.SMTPPort)
	out["smtpUser"] = email.SMTPUser
	out["smtpFrom"] = email.SMTPFrom
	out["smtpPassword"] = ""
	out["resendApiKey"] = ""
	out["resendFrom"] = email.ResendFrom
	out["emailVerificationRequired"] = strconv.FormatBool(email.Enabled)
	jsonOut(w, 200, map[string]any{
		"settings":               out,
		"smtpPasswordConfigured": email.SMTPPassword != "",
		"resendApiKeyConfigured": email.ResendAPIKey != "",
		"turnstileConfigured":    s.C.TurnstileSiteKey != "" && s.C.TurnstileSecret != "",
	})
}

func (s *Server) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	if decode(r, &values) != nil {
		fail(w, 400, "invalid settings")
		return
	}
	clean := map[string]string{}
	for key, value := range values {
		if !settingKeys[key] {
			fail(w, 400, "unsupported setting: "+key)
			return
		}
		value = strings.TrimSpace(value)
		if len(value) > 500 {
			fail(w, 400, "setting is too long")
			return
		}
		if key == "contactURL" && value != "" {
			parsed, err := url.Parse(value)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				fail(w, 400, "contactURL must be an HTTP or HTTPS URL")
				return
			}
		}
		if key == "markupCNY" || key == "usdCnyRate" || key == "smsmanCnyRate" {
			number, err := strconv.ParseFloat(value, 64)
			if err != nil || number <= 0 || number > 1000 {
				fail(w, 400, key+" must be between 0 and 1000")
				return
			}
		}
		if key == "refundWindowMinutes" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 1 || number > 1440 {
				fail(w, 400, "refundWindowMinutes must be between 1 and 1440")
				return
			}
		}
		if key == "refundMaxCount" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 0 || number > 100 {
				fail(w, 400, "refundMaxCount must be between 0 and 100")
				return
			}
		}
		if key == "blockedCountries" {
			for _, item := range strings.Split(value, ",") {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				if _, err := strconv.Atoi(item); err != nil {
					fail(w, 400, "blockedCountries must be a comma-separated list of country ids")
					return
				}
			}
		}
		if key == "blockedServices" {
			for _, item := range strings.Split(value, ",") {
				item = strings.ToLower(strings.TrimSpace(item))
				if item == "" {
					continue
				}
				if len(item) > 32 || strings.ContainsAny(item, " \t\r\n/\\") {
					fail(w, 400, "blockedServices must be a comma-separated list of service codes")
					return
				}
			}
			value = strings.ToLower(value)
		}
		if key == "smtpPort" {
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				fail(w, 400, "smtpPort must be between 1 and 65535")
				return
			}
		}
		if key == "mailProvider" && value != "smtp" && value != "resend" {
			fail(w, 400, "mailProvider must be smtp or resend")
			return
		}
		if key == "emailVerificationRequired" && value != "true" && value != "false" {
			fail(w, 400, "emailVerificationRequired must be true or false")
			return
		}
		if (key == "smtpPassword" || key == "resendApiKey") && value == "" {
			continue
		}
		clean[key] = value
	}
	current := s.effectiveEmailVerification()
	if value, ok := clean["mailProvider"]; ok {
		current.Provider = value
	}
	if value, ok := clean["emailVerificationRequired"]; ok {
		current.Enabled = value == "true"
	}
	if value, ok := clean["smtpHost"]; ok {
		current.SMTPHost = value
	}
	if value, ok := clean["smtpUser"]; ok {
		current.SMTPUser = value
	}
	if value, ok := clean["smtpPassword"]; ok {
		current.SMTPPassword = value
	}
	if value, ok := clean["smtpFrom"]; ok {
		current.SMTPFrom = value
	}
	if value, ok := clean["smtpPort"]; ok {
		current.SMTPPort, _ = strconv.Atoi(value)
	}
	if value, ok := clean["resendApiKey"]; ok {
		current.ResendAPIKey = value
	}
	if value, ok := clean["resendFrom"]; ok {
		current.ResendFrom = value
	}
	if err := validateEmailVerificationSettings(current, s.C.TurnstileSiteKey != "" && s.C.TurnstileSecret != ""); err != nil {
		fail(w, 400, err)
		return
	}
	if err := s.Store.UpdateSettings(clean); err != nil {
		fail(w, 500, err)
		return
	}
	s.Store.Audit("settings.update", "settings", strings.Join(sortedKeys(clean), ","))
	s.adminSettings(w, r)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func parseBlockedCountries(value string) map[string]bool {
	blocked := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			blocked[item] = true
		}
	}
	return blocked
}

func (s *Server) blockedCountries() map[string]bool {
	values, err := s.Store.Settings()
	if err != nil {
		return map[string]bool{}
	}
	return parseBlockedCountries(values["blockedCountries"])
}

func (s *Server) countryBlocked(country string) bool {
	return s.blockedCountries()[strings.TrimSpace(country)]
}

func parseBlockedServices(value string) map[string]bool {
	blocked := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			blocked[item] = true
		}
	}
	return blocked
}

func (s *Server) blockedServices() map[string]bool {
	values, err := s.Store.Settings()
	if err != nil {
		return map[string]bool{}
	}
	return parseBlockedServices(values["blockedServices"])
}

func (s *Server) serviceBlocked(service string) bool {
	return s.blockedServices()[strings.ToLower(strings.TrimSpace(service))]
}

func messageBody(r *http.Request) (string, error) {
	var input struct {
		Body string `json:"body"`
	}
	if err := decode(r, &input); err != nil {
		return "", err
	}
	input.Body = strings.TrimSpace(input.Body)
	if input.Body == "" || len([]rune(input.Body)) > 1000 {
		return "", http.ErrBodyNotAllowed
	}
	return input.Body, nil
}

func (s *Server) supportMessages(w http.ResponseWriter, r *http.Request, user store.User) {
	messages, err := s.Store.SupportMessages(user.ID, "user", 200)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, messages)
}

func (s *Server) sendSupportMessage(w http.ResponseWriter, r *http.Request, user store.User) {
	body, err := messageBody(r)
	if err != nil {
		fail(w, 400, "message must be 1-1000 characters")
		return
	}
	message, err := s.Store.AddSupportMessage(user.ID, "user", body)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 201, message)
}

func (s *Server) adminChats(w http.ResponseWriter, r *http.Request) {
	threads, err := s.Store.SupportThreads()
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, threads)
}

func (s *Server) chatUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || !s.Store.UserExists(userID) {
		fail(w, 404, "user not found")
		return 0, false
	}
	return userID, true
}

func (s *Server) adminChatMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.chatUserID(w, r)
	if !ok {
		return
	}
	messages, err := s.Store.SupportMessages(userID, "admin", 500)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, messages)
}

func (s *Server) adminSendMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.chatUserID(w, r)
	if !ok {
		return
	}
	body, err := messageBody(r)
	if err != nil {
		fail(w, 400, "message must be 1-1000 characters")
		return
	}
	message, err := s.Store.AddSupportMessage(userID, "admin", body)
	if err != nil {
		fail(w, 500, err)
		return
	}
	s.Store.Audit("support.reply", strconv.FormatInt(userID, 10), "")
	jsonOut(w, 201, message)
}

func (s *Server) publicAnnouncements(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.Announcements(true)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, items)
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.AdminUsers(r.URL.Query().Get("q"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, users)
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.chatUserID(w, r)
	if !ok {
		return
	}
	var input struct {
		Disabled bool `json:"disabled"`
	}
	if decode(r, &input) != nil {
		fail(w, 400, "invalid user update")
		return
	}
	if err := s.Store.SetUserDisabled(userID, input.Disabled); err != nil {
		fail(w, 500, err)
		return
	}
	s.Store.Audit("user.status", strconv.FormatInt(userID, 10), strconv.FormatBool(input.Disabled))
	jsonOut(w, 200, map[string]any{"id": userID, "disabled": input.Disabled})
}

func (s *Server) adminOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.Store.AdminOrders(r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, orders)
}

func (s *Server) adminOrderLogs(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimSpace(r.PathValue("id"))
	if orderID == "" {
		fail(w, 400, "invalid order id")
		return
	}
	logs, err := s.Store.AdminOrderLogs(orderID)
	if err != nil {
		if err == sql.ErrNoRows {
			fail(w, 404, "order not found")
			return
		}
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, logs)
}

func (s *Server) adminCloseOrder(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimSpace(r.PathValue("id"))
	if orderID == "" {
		fail(w, 400, "invalid order id")
		return
	}
	order, err := s.Store.GetSMSByID(orderID)
	if err != nil {
		fail(w, 404, "order not found")
		return
	}
	switch order.Status {
	case "awaiting_payment":
		if err = s.Store.AdminCloseAwaitingPayment(orderID, "admin_closed"); err != nil {
			fail(w, 409, err)
			return
		}
	case "waiting", "replacing", "paid", "purchasing":
		if order.UpstreamID != "" && (order.Status == "waiting" || order.Status == "replacing") {
			cancelled, cancelErr := s.cancelUpstream(r.Context(), order)
			if cancelErr != nil {
				fail(w, 409, cancelErr)
				return
			}
			if !cancelled {
				fail(w, 409, "upstream cancellation was not confirmed")
				return
			}
			_ = s.Store.EndCurrentAttemptWithProvider(order.ID, order.UpstreamProvider, order.UpstreamID, "cancelled")
		}
		if err = s.Store.SetSMSOrderStatus(order.ID, "admin_closed"); err != nil {
			fail(w, 500, err)
			return
		}
		recharge, rechargeErr := s.Store.GetRechargeByReference(order.ID)
		if rechargeErr == nil && (recharge.Provider == "epay" || recharge.Provider == "50pay") && recharge.Status == "paid" && recharge.RefundedAt == "" {
			policy := s.effectiveRefundPolicy()
			since := time.Now().UTC().Add(-time.Duration(policy.WindowMinutes) * time.Minute).Format(time.RFC3339)
			recentRefunds, countErr := s.Store.CountRecentRefundsByUser(order.UserID, since)
			if countErr != nil {
				fail(w, 500, countErr)
				return
			}
			if policy.MaxCount >= 0 && recentRefunds >= policy.MaxCount {
				s.Store.Audit("order.close.refund_skipped", orderID, "threshold")
				jsonOut(w, 200, map[string]any{"closed": true, "refunded": false, "reason": "refund_threshold_reached"})
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
			if err = s.Store.MarkRechargeRefunded(recharge.ID, refund.OutRefundNo); err != nil {
				fail(w, 500, err)
				return
			}
			s.Store.Audit("order.close.refunded", orderID, refund.OutRefundNo)
			jsonOut(w, 200, map[string]any{"closed": true, "refunded": true, "refundId": refund.OutRefundNo})
			return
		}
	default:
		fail(w, 409, "order cannot be closed in its current state")
		return
	}
	s.Store.Audit("order.close", orderID, order.Status)
	jsonOut(w, 200, map[string]bool{"closed": true})
}

func (s *Server) adminPayments(w http.ResponseWriter, r *http.Request) {
	payments, err := s.Store.AdminPayments(r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, payments)
}

func (s *Server) adminEmailLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.Store.AdminEmailLogs(200)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, logs)
}

func (s *Server) adminAnnouncements(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.Announcements(false)
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, items)
}

func (s *Server) adminSaveAnnouncement(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Active bool   `json:"active"`
	}
	if decode(r, &input) != nil {
		fail(w, 400, "invalid announcement")
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.Title == "" || len([]rune(input.Title)) > 100 || input.Body == "" || len([]rune(input.Body)) > 5000 {
		fail(w, 400, "announcement title or body is invalid")
		return
	}
	item := store.Announcement{Title: input.Title, Body: input.Body, Active: input.Active}
	if rawID := r.PathValue("id"); rawID != "" {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			fail(w, 400, "invalid announcement id")
			return
		}
		item.ID = id
	}
	saved, err := s.Store.SaveAnnouncement(item)
	if err != nil {
		fail(w, 500, err)
		return
	}
	s.Store.Audit("announcement.save", strconv.FormatInt(saved.ID, 10), saved.Title)
	status := 200
	if item.ID == 0 {
		status = 201
	}
	jsonOut(w, status, saved)
}

func (s *Server) adminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "invalid announcement id")
		return
	}
	if err = s.Store.DeleteAnnouncement(id); err != nil {
		fail(w, 404, "announcement not found")
		return
	}
	s.Store.Audit("announcement.delete", strconv.FormatInt(id, 10), "")
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.AuditEvents()
	if err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, events)
}
