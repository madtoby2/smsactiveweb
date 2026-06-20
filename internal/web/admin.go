package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sms-platform/internal/store"
)

var settingKeys = map[string]bool{
	"contactTitle": true,
	"contactValue": true,
	"contactURL":   true,
	"supportHours": true,
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
	for key, value := range values {
		defaults[key] = value
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
	s.publicSettings(w, r)
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
		clean[key] = value
	}
	if err := s.Store.UpdateSettings(clean); err != nil {
		fail(w, 500, err)
		return
	}
	s.adminSettings(w, r)
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
	jsonOut(w, 201, message)
}
