package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	_ "modernc.org/sqlite"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Store struct{ DB *sql.DB }
type User struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Balance  int64  `json:"balanceFen"`
	Disabled bool   `json:"disabled,omitempty"`
}
type UserProfile struct {
	ID               int64  `json:"id"`
	Email            string `json:"email"`
	BalanceFen       int64  `json:"balanceFen"`
	CreatedAt        string `json:"createdAt"`
	OrdersTotal      int64  `json:"ordersTotal"`
	OrdersSuccessful int64  `json:"ordersSuccessful"`
	SpentFen         int64  `json:"spentFen"`
}
type SMSOrder struct {
	ID               string `json:"id"`
	UserID           int64  `json:"-"`
	UpstreamID       string `json:"-"`
	UpstreamProvider string `json:"-"`
	UpstreamCountry  string `json:"-"`
	UpstreamService  string `json:"-"`
	Country          string
	CountryName      string `json:"countryName"`
	Service          string
	Phone            string
	Status           string
	Code             string
	UpstreamCost     float64 `json:"-"`
	PriceFen         int64
	Refunded         bool `json:"refunded"`
	AutoReplace      bool
	ReplaceAttempts  int
	LastNumberAt     string
	CreatedAt        string
}
type Product struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	PriceFen       int64  `json:"priceFen"`
	Active         bool   `json:"active"`
	AvailableCount int64  `json:"availableCount"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}
type ProductInventoryItem struct {
	ID          int64  `json:"id"`
	ProductCode string `json:"productCode"`
	Credential  string `json:"credential"`
	Status      string `json:"status"`
	OrderID     string `json:"orderId"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}
type ProductOrder struct {
	ID          string `json:"id"`
	UserID      int64  `json:"-"`
	ProductCode string `json:"productCode"`
	ProductName string `json:"productName"`
	Credential  string `json:"credential"`
	Status      string `json:"status"`
	PriceFen    int64  `json:"priceFen"`
	Refunded    bool   `json:"refunded"`
	CreatedAt   string `json:"createdAt"`
	DeliveredAt string `json:"deliveredAt"`
}
type Recharge struct {
	ID                                                      string `json:"id"`
	UserID, AmountFen                                       int64
	Provider, PayType, Status, ProviderID, RefundProviderID string
	Token, Reference, RefundedAt                            string
	CreatedAt                                               string
}
type AdminStats struct {
	OrdersTotal      int64 `json:"ordersTotal"`
	OrdersToday      int64 `json:"ordersToday"`
	OrdersSuccessful int64 `json:"ordersSuccessful"`
	RevenueFen       int64 `json:"revenueFen"`
	UsersTotal       int64 `json:"usersTotal"`
	OpenChats        int64 `json:"openChats"`
}
type AdminOrder struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Country   string `json:"country"`
	Service   string `json:"service"`
	Status    string `json:"status"`
	Provider  string `json:"provider"`
	Phone     string `json:"phone"`
	CreatedAt string `json:"createdAt"`
	PriceFen  int64  `json:"priceFen"`
}
type SupportMessage struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	Sender    string `json:"sender"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}
type SupportThread struct {
	UserID      int64  `json:"userId"`
	Unread      int64  `json:"unread"`
	Email       string `json:"email"`
	LastMessage string `json:"lastMessage"`
	LastAt      string `json:"lastAt"`
}
type AdminUser struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	BalanceFen int64  `json:"balanceFen"`
	Disabled   bool   `json:"disabled"`
	Orders     int64  `json:"orders"`
	SpentFen   int64  `json:"spentFen"`
	CreatedAt  string `json:"createdAt"`
}
type AdminPayment struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Provider   string `json:"provider"`
	PayType    string `json:"payType"`
	Status     string `json:"status"`
	ProviderID string `json:"providerId"`
	OrderID    string `json:"orderId"`
	CreatedAt  string `json:"createdAt"`
	AmountFen  int64  `json:"amountFen"`
}
type AdminOrderLogEntry struct {
	Time   string `json:"time"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}
type Announcement struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Active    bool   `json:"active"`
}
type AuditEvent struct {
	ID     int64  `json:"id"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	At     string `json:"at"`
}
type EmailSendLog struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	Provider   string `json:"provider"`
	Sender     string `json:"sender"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	CreatedAt  string `json:"createdAt"`
}

const smsOrderSelect = `SELECT id,user_id,COALESCE(upstream_id,''),upstream_provider,COALESCE(upstream_country,''),COALESCE(upstream_service,''),country,COALESCE(country_name,''),service,COALESCE(phone,''),status,COALESCE(code,''),upstream_cost,price_fen,refunded,auto_replace,replace_attempts,last_number_at,created_at FROM sms_orders`

type scanner interface {
	Scan(dest ...any) error
}

func scanSMS(row scanner, order *SMSOrder) error {
	return row.Scan(&order.ID, &order.UserID, &order.UpstreamID, &order.UpstreamProvider, &order.UpstreamCountry, &order.UpstreamService, &order.Country, &order.CountryName, &order.Service, &order.Phone, &order.Status, &order.Code, &order.UpstreamCost, &order.PriceFen, &order.Refunded, &order.AutoReplace, &order.ReplaceAttempts, &order.LastNumberAt, &order.CreatedAt)
}

func providerOrHero(provider string) string {
	if provider == "" {
		return "hero"
	}
	return provider
}

const sessionLifetime = 24 * time.Hour

func Open(path string) (*Store, error) {
	if err := os.MkdirAll("data", 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY,email TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL,balance_fen INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL,expires_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id));
CREATE TABLE IF NOT EXISTS sms_orders(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,upstream_id TEXT,upstream_provider TEXT NOT NULL DEFAULT 'hero',upstream_country TEXT NOT NULL DEFAULT '',upstream_service TEXT NOT NULL DEFAULT '',country TEXT NOT NULL,country_name TEXT NOT NULL DEFAULT '',service TEXT NOT NULL,phone TEXT,status TEXT NOT NULL,code TEXT,upstream_cost REAL NOT NULL,price_fen INTEGER NOT NULL,refunded INTEGER NOT NULL DEFAULT 0,auto_replace INTEGER NOT NULL DEFAULT 0,replace_attempts INTEGER NOT NULL DEFAULT 0,last_number_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id),UNIQUE(upstream_provider,upstream_id));
CREATE TABLE IF NOT EXISTS sms_attempts(id INTEGER PRIMARY KEY,order_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_provider TEXT NOT NULL DEFAULT 'hero',phone TEXT NOT NULL,status TEXT NOT NULL,upstream_cost REAL NOT NULL,started_at TEXT NOT NULL,ended_at TEXT,FOREIGN KEY(order_id) REFERENCES sms_orders(id),UNIQUE(upstream_provider,upstream_id));
CREATE TABLE IF NOT EXISTS product_catalog(code TEXT PRIMARY KEY,name TEXT NOT NULL,category TEXT NOT NULL DEFAULT 'telegram_account',description TEXT NOT NULL DEFAULT '',price_fen INTEGER NOT NULL,active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS product_inventory(id INTEGER PRIMARY KEY,product_code TEXT NOT NULL,credential TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'available',order_id TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,FOREIGN KEY(product_code) REFERENCES product_catalog(code));
CREATE INDEX IF NOT EXISTS product_inventory_product_status ON product_inventory(product_code,status);
CREATE TABLE IF NOT EXISTS product_orders(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,product_code TEXT NOT NULL,product_name TEXT NOT NULL,credential TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,price_fen INTEGER NOT NULL,refunded INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,delivered_at TEXT NOT NULL DEFAULT '',FOREIGN KEY(user_id) REFERENCES users(id),FOREIGN KEY(product_code) REFERENCES product_catalog(code));
CREATE TABLE IF NOT EXISTS recharges(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,amount_fen INTEGER NOT NULL,provider TEXT NOT NULL,pay_type TEXT NOT NULL,status TEXT NOT NULL,provider_id TEXT,refund_provider_id TEXT NOT NULL DEFAULT '',refunded_at TEXT NOT NULL DEFAULT '',token TEXT NOT NULL,reference TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id));
CREATE TABLE IF NOT EXISTS ledger(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,amount_fen INTEGER NOT NULL,kind TEXT NOT NULL,reference TEXT NOT NULL UNIQUE,created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, migration := range []string{
		"ALTER TABLE sms_orders ADD COLUMN auto_replace INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE sms_orders ADD COLUMN replace_attempts INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE sms_orders ADD COLUMN last_number_at TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE recharges ADD COLUMN reference TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE recharges ADD COLUMN refund_provider_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE recharges ADD COLUMN refunded_at TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sms_orders ADD COLUMN upstream_country TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sms_orders ADD COLUMN upstream_service TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sms_orders ADD COLUMN country_name TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0",
	} {
		if _, e := db.Exec(migration); e != nil && !strings.Contains(strings.ToLower(e.Error()), "duplicate column") {
			db.Close()
			return nil, e
		}
	}
	if err = migrateUpstreamProviders(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS sms_attempts(id INTEGER PRIMARY KEY,order_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_provider TEXT NOT NULL DEFAULT 'hero',phone TEXT NOT NULL,status TEXT NOT NULL,upstream_cost REAL NOT NULL,started_at TEXT NOT NULL,ended_at TEXT,FOREIGN KEY(order_id) REFERENCES sms_orders(id),UNIQUE(upstream_provider,upstream_id))`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS recharges_provider_tx_unique ON recharges(provider,provider_id) WHERE provider_id IS NOT NULL AND provider_id <> ''`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`
CREATE TABLE IF NOT EXISTS admin_sessions(token_hash TEXT PRIMARY KEY,expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS support_messages(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,sender TEXT NOT NULL CHECK(sender IN ('user','admin')),body TEXT NOT NULL,created_at TEXT NOT NULL,read_at TEXT,FOREIGN KEY(user_id) REFERENCES users(id));
CREATE INDEX IF NOT EXISTS support_messages_user_id ON support_messages(user_id,id);
CREATE TABLE IF NOT EXISTS announcements(id INTEGER PRIMARY KEY,title TEXT NOT NULL,body TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS admin_audit(id INTEGER PRIMARY KEY,action TEXT NOT NULL,target TEXT NOT NULL DEFAULT '',detail TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS email_verifications(email TEXT PRIMARY KEY,code_hash TEXT NOT NULL,expires_at TEXT NOT NULL,sent_at TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS email_verification_sends(id INTEGER PRIMARY KEY,email TEXT NOT NULL,ip TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS email_verification_sends_email_time ON email_verification_sends(email,created_at);
CREATE INDEX IF NOT EXISTS email_verification_sends_ip_time ON email_verification_sends(ip,created_at);
CREATE TABLE IF NOT EXISTS email_send_logs(id INTEGER PRIMARY KEY,email TEXT NOT NULL,provider TEXT NOT NULL,sender TEXT NOT NULL,status TEXT NOT NULL,error_message TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS email_send_logs_created_at ON email_send_logs(created_at DESC);`); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() { s.DB.Close() }

func migrateUpstreamProviders(db *sql.DB) error {
	hasProvider, err := hasColumn(db, "sms_orders", "upstream_provider")
	if err != nil || hasProvider {
		return err
	}
	if _, err = db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer db.Exec("PRAGMA foreign_keys=ON")
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE sms_orders_new(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,upstream_id TEXT,upstream_provider TEXT NOT NULL DEFAULT 'hero',upstream_country TEXT NOT NULL DEFAULT '',upstream_service TEXT NOT NULL DEFAULT '',country TEXT NOT NULL,country_name TEXT NOT NULL DEFAULT '',service TEXT NOT NULL,phone TEXT,status TEXT NOT NULL,code TEXT,upstream_cost REAL NOT NULL,price_fen INTEGER NOT NULL,refunded INTEGER NOT NULL DEFAULT 0,auto_replace INTEGER NOT NULL DEFAULT 0,replace_attempts INTEGER NOT NULL DEFAULT 0,last_number_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id),UNIQUE(upstream_provider,upstream_id))`,
		`INSERT INTO sms_orders_new(id,user_id,upstream_id,upstream_provider,upstream_country,upstream_service,country,country_name,service,phone,status,code,upstream_cost,price_fen,refunded,auto_replace,replace_attempts,last_number_at,created_at) SELECT id,user_id,upstream_id,'hero',upstream_country,upstream_service,country,'',service,phone,status,code,upstream_cost,price_fen,refunded,auto_replace,replace_attempts,last_number_at,created_at FROM sms_orders`,
		`CREATE TABLE sms_attempts_new(id INTEGER PRIMARY KEY,order_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_provider TEXT NOT NULL DEFAULT 'hero',phone TEXT NOT NULL,status TEXT NOT NULL,upstream_cost REAL NOT NULL,started_at TEXT NOT NULL,ended_at TEXT,FOREIGN KEY(order_id) REFERENCES sms_orders_new(id),UNIQUE(upstream_provider,upstream_id))`,
		`INSERT INTO sms_attempts_new(id,order_id,upstream_id,upstream_provider,phone,status,upstream_cost,started_at,ended_at) SELECT id,order_id,upstream_id,'hero',phone,status,upstream_cost,started_at,ended_at FROM sms_attempts`,
		`DROP TABLE sms_attempts`,
		`DROP TABLE sms_orders`,
		`ALTER TABLE sms_orders_new RENAME TO sms_orders`,
		`ALTER TABLE sms_attempts_new RENAME TO sms_attempts`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func Password(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("密码至少 8 位")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	sum := derive(password, salt)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(sum), nil
}
func VerifyPassword(encoded, password string) bool {
	p := strings.SplitN(encoded, ":", 2)
	if len(p) != 2 {
		return false
	}
	salt, e := hex.DecodeString(p[0])
	if e != nil {
		return false
	}
	return hex.EncodeToString(derive(password, salt)) == p[1]
}
func derive(password string, salt []byte) []byte {
	v := append(append([]byte{}, salt...), []byte(password)...)
	for i := 0; i < 120000; i++ {
		x := sha256.Sum256(v)
		v = x[:]
	}
	return v
}
func Token() (string, string) {
	b := make([]byte, 32)
	rand.Read(b)
	raw := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(h[:])
}
func ID(prefix string) string {
	b := make([]byte, 10)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Store) Register(email, password string) (User, string, error) {
	hash, e := Password(password)
	if e != nil {
		return User{}, "", e
	}
	r, e := s.DB.Exec("INSERT INTO users(email,password_hash,created_at)VALUES(?,?,?)", email, hash, time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return User{}, "", errors.New("邮箱已注册")
	}
	id, _ := r.LastInsertId()
	token, th := Token()
	_, e = s.DB.Exec("INSERT INTO sessions VALUES(?,?,?)", th, id, time.Now().Add(sessionLifetime).UTC().Format(time.RFC3339))
	return User{ID: id, Email: email}, token, e
}

func (s *Store) SaveEmailVerification(email, code, ip string) error {
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE email=?)", email).Scan(&exists); err != nil {
		return err
	}
	if exists == 1 {
		return errors.New("邮箱已注册")
	}
	var lastSent string
	_ = tx.QueryRow("SELECT COALESCE(MAX(created_at),'') FROM email_verification_sends WHERE email=?", email).Scan(&lastSent)
	if lastSent != "" {
		last, parseErr := time.Parse(time.RFC3339, lastSent)
		if parseErr == nil && now.Sub(last) < time.Minute {
			return errors.New("验证码发送过于频繁，请稍后再试")
		}
	}
	var emailCount, ipCount int
	if err = tx.QueryRow("SELECT COUNT(*) FROM email_verification_sends WHERE email=? AND created_at>=?", email, now.Add(-time.Hour).Format(time.RFC3339)).Scan(&emailCount); err != nil {
		return err
	}
	if err = tx.QueryRow("SELECT COUNT(*) FROM email_verification_sends WHERE ip=? AND created_at>=?", ip, now.Add(-time.Hour).Format(time.RFC3339)).Scan(&ipCount); err != nil {
		return err
	}
	if emailCount >= 5 || ipCount >= 20 {
		return errors.New("验证码发送次数过多，请一小时后再试")
	}
	codeHash, err := Password("otp:" + code)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO email_verifications(email,code_hash,expires_at,sent_at,attempts) VALUES(?,?,?,?,0)
		ON CONFLICT(email) DO UPDATE SET code_hash=excluded.code_hash,expires_at=excluded.expires_at,sent_at=excluded.sent_at,attempts=0`, email, codeHash, now.Add(10*time.Minute).Format(time.RFC3339), nowText); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO email_verification_sends(email,ip,created_at) VALUES(?,?,?)", email, ip, nowText); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteEmailVerification(email string) {
	_, _ = s.DB.Exec("DELETE FROM email_verifications WHERE email=?", email)
}

func (s *Store) LogEmailSend(email, provider, sender, status, errorMessage string) {
	_, _ = s.DB.Exec(
		"INSERT INTO email_send_logs(email,provider,sender,status,error_message,created_at) VALUES(?,?,?,?,?,?)",
		email,
		provider,
		sender,
		status,
		errorMessage,
		time.Now().UTC().Format(time.RFC3339),
	)
}

func (s *Store) AdminEmailLogs(limit int) ([]EmailSendLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query("SELECT id,email,provider,sender,status,error_message,created_at FROM email_send_logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EmailSendLog{}
	for rows.Next() {
		var item EmailSendLog
		if err = rows.Scan(&item.ID, &item.Email, &item.Provider, &item.Sender, &item.Status, &item.Error, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RegisterVerified(email, password, code string) (User, string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback()
	var codeHash, expiresAt string
	var attempts int
	if err = tx.QueryRow("SELECT code_hash,expires_at,attempts FROM email_verifications WHERE email=?", email).Scan(&codeHash, &expiresAt, &attempts); err != nil {
		return User{}, "", errors.New("请先获取邮箱验证码")
	}
	if attempts >= 5 || expiresAt <= time.Now().UTC().Format(time.RFC3339) {
		return User{}, "", errors.New("验证码已过期，请重新获取")
	}
	if !VerifyPassword(codeHash, "otp:"+code) {
		_, _ = tx.Exec("UPDATE email_verifications SET attempts=attempts+1 WHERE email=?", email)
		_ = tx.Commit()
		return User{}, "", errors.New("邮箱验证码错误")
	}
	passwordHash, err := Password(password)
	if err != nil {
		return User{}, "", err
	}
	result, err := tx.Exec("INSERT INTO users(email,password_hash,created_at)VALUES(?,?,?)", email, passwordHash, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return User{}, "", errors.New("邮箱已注册")
	}
	userID, _ := result.LastInsertId()
	raw, tokenHash := Token()
	if _, err = tx.Exec("INSERT INTO sessions VALUES(?,?,?)", tokenHash, userID, time.Now().Add(sessionLifetime).UTC().Format(time.RFC3339)); err != nil {
		return User{}, "", err
	}
	if _, err = tx.Exec("DELETE FROM email_verifications WHERE email=?", email); err != nil {
		return User{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return User{}, "", err
	}
	return User{ID: userID, Email: email}, raw, nil
}
func (s *Store) Login(email, password string) (User, string, error) {
	var u User
	var hash string
	e := s.DB.QueryRow("SELECT id,email,balance_fen,password_hash,disabled FROM users WHERE email=?", email).Scan(&u.ID, &u.Email, &u.Balance, &hash, &u.Disabled)
	if e != nil || u.Disabled || !VerifyPassword(hash, password) {
		return User{}, "", errors.New("邮箱或密码错误")
	}
	token, th := Token()
	_, e = s.DB.Exec("INSERT INTO sessions VALUES(?,?,?)", th, u.ID, time.Now().Add(sessionLifetime).UTC().Format(time.RFC3339))
	return u, token, e
}
func (s *Store) UserByToken(raw string) (User, error) {
	h := sha256.Sum256([]byte(raw))
	var u User
	e := s.DB.QueryRow(`SELECT u.id,u.email,u.balance_fen,u.disabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>? AND u.disabled=0`, hex.EncodeToString(h[:]), time.Now().UTC().Format(time.RFC3339)).Scan(&u.ID, &u.Email, &u.Balance, &u.Disabled)
	return u, e
}
func (s *Store) TouchSession(raw string) error {
	h := sha256.Sum256([]byte(raw))
	_, e := s.DB.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=? AND expires_at>? AND expires_at<?`, time.Now().Add(sessionLifetime).UTC().Format(time.RFC3339), hex.EncodeToString(h[:]), time.Now().UTC().Format(time.RFC3339), time.Now().Add(12*time.Hour).UTC().Format(time.RFC3339))
	return e
}
func (s *Store) DeleteSession(raw string) error {
	h := sha256.Sum256([]byte(raw))
	_, e := s.DB.Exec("DELETE FROM sessions WHERE token_hash=?", hex.EncodeToString(h[:]))
	return e
}

func (s *Store) CreateAdminSession(raw string) error {
	h := sha256.Sum256([]byte(raw))
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM admin_sessions WHERE expires_at<=?", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO admin_sessions(token_hash,expires_at) VALUES(?,?)", hex.EncodeToString(h[:]), time.Now().Add(12*time.Hour).UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdminSessionValid(raw string) bool {
	h := sha256.Sum256([]byte(raw))
	var valid int
	err := s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM admin_sessions WHERE token_hash=? AND expires_at>?)", hex.EncodeToString(h[:]), time.Now().UTC().Format(time.RFC3339)).Scan(&valid)
	return err == nil && valid == 1
}

func (s *Store) DeleteAdminSession(raw string) error {
	h := sha256.Sum256([]byte(raw))
	_, err := s.DB.Exec("DELETE FROM admin_sessions WHERE token_hash=?", hex.EncodeToString(h[:]))
	return err
}

func (s *Store) CreateSMS(u User, o SMSOrder) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.Exec("UPDATE users SET balance_fen=balance_fen-? WHERE id=? AND balance_fen>=?", o.PriceFen, u.ID, o.PriceFen)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("余额不足")
	}
	_, e = tx.Exec(`INSERT INTO sms_orders(id,user_id,upstream_provider,upstream_country,upstream_service,country,country_name,service,status,upstream_cost,price_fen,auto_replace,created_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, u.ID, providerOrHero(o.UpstreamProvider), o.UpstreamCountry, o.UpstreamService, o.Country, o.CountryName, o.Service, "purchasing", o.UpstreamCost, o.PriceFen, o.AutoReplace, o.CreatedAt)
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO ledger(user_id,amount_fen,kind,reference,created_at)VALUES(?,?,?,?,?)", u.ID, -o.PriceFen, "sms_purchase", o.ID, o.CreatedAt)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) CreateSMSPayment(u User, o SMSOrder, p Recharge) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT INTO sms_orders(id,user_id,upstream_provider,upstream_country,upstream_service,country,country_name,service,status,upstream_cost,price_fen,auto_replace,created_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, u.ID, providerOrHero(o.UpstreamProvider), o.UpstreamCountry, o.UpstreamService, o.Country, o.CountryName, o.Service, "awaiting_payment", o.UpstreamCost, o.PriceFen, o.AutoReplace, o.CreatedAt); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO recharges(id,user_id,amount_fen,provider,pay_type,status,provider_id,refund_provider_id,refunded_at,token,reference,created_at)VALUES(?,?,?,?,?,'pending','','','',?,?,?)`, p.ID, u.ID, p.AmountFen, p.Provider, p.PayType, p.Token, o.ID, p.CreatedAt); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ActivateSMS(id, upstream, phone string, cost float64) error {
	return s.ActivateSMSWithProvider(id, "hero", upstream, phone, cost)
}

func (s *Store) ActivateSMSWithProvider(id, provider, upstream, phone string, cost float64) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	provider = providerOrHero(provider)
	if _, e = tx.Exec("UPDATE sms_orders SET upstream_id=?,upstream_provider=?,phone=?,upstream_cost=?,status='waiting',last_number_at=? WHERE id=?", upstream, provider, phone, cost, now, id); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO sms_attempts(order_id,upstream_id,upstream_provider,phone,status,upstream_cost,started_at)VALUES(?,?,?,?,?,?,?)", id, upstream, provider, phone, "waiting", cost, now); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) RefundSMS(id, reason string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var uid, price int64
	var refunded int
	e = tx.QueryRow("SELECT user_id,price_fen,refunded FROM sms_orders WHERE id=?", id).Scan(&uid, &price, &refunded)
	if e != nil {
		return e
	}
	if refunded == 1 {
		return tx.Commit()
	}
	if _, e = tx.Exec("UPDATE sms_orders SET refunded=1,status=? WHERE id=?", reason, id); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE users SET balance_fen=balance_fen+? WHERE id=?", price, uid); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO ledger(user_id,amount_fen,kind,reference,created_at)VALUES(?,?,?,?,?)", uid, price, "sms_refund", "refund:"+id, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) GetSMS(id string, uid int64) (SMSOrder, error) {
	var o SMSOrder
	e := scanSMS(s.DB.QueryRow(smsOrderSelect+` WHERE id=? AND user_id=?`, id, uid), &o)
	return o, e
}
func (s *Store) GetSMSByID(id string) (SMSOrder, error) {
	var o SMSOrder
	e := scanSMS(s.DB.QueryRow(smsOrderSelect+` WHERE id=?`, id), &o)
	return o, e
}
func (s *Store) UpdateSMS(id, status, code string) error {
	_, e := s.DB.Exec("UPDATE sms_orders SET status=?,code=CASE WHEN ?='' THEN code ELSE ? END WHERE id=?", status, code, code, id)
	return e
}
func (s *Store) ResetSMSCode(id, status string) error {
	_, e := s.DB.Exec("UPDATE sms_orders SET status=?,code='' WHERE id=?", status, id)
	return e
}
func (s *Store) ListSMS(uid int64) ([]SMSOrder, error) {
	rows, e := s.DB.Query(smsOrderSelect+` WHERE user_id=? ORDER BY created_at DESC LIMIT 100`, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SMSOrder{}
	for rows.Next() {
		var o SMSOrder
		if e = scanSMS(rows, &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) UserProfile(uid int64) (UserProfile, error) {
	var profile UserProfile
	err := s.DB.QueryRow(`
		SELECT u.id, u.email, u.balance_fen, u.created_at,
			(SELECT COUNT(*) FROM sms_orders o WHERE o.user_id=u.id),
			(SELECT COUNT(*) FROM sms_orders o WHERE o.user_id=u.id AND o.status='code_received'),
			COALESCE((SELECT SUM(r.amount_fen) FROM recharges r WHERE r.user_id=u.id AND r.status='paid' AND COALESCE(r.reference,'')<>''), 0)
		FROM users u WHERE u.id=?`, uid).Scan(
		&profile.ID, &profile.Email, &profile.BalanceFen, &profile.CreatedAt,
		&profile.OrdersTotal, &profile.OrdersSuccessful, &profile.SpentFen,
	)
	return profile, err
}

func (s *Store) ListPaidSMS(limit int) ([]SMSOrder, error) {
	rows, e := s.DB.Query(smsOrderSelect+` WHERE status='paid' ORDER BY created_at LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []SMSOrder
	for rows.Next() {
		var o SMSOrder
		if e = scanSMS(rows, &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) ClaimPaidSMS(id string) (bool, error) {
	r, e := s.DB.Exec("UPDATE sms_orders SET status='purchasing' WHERE id=? AND status='paid'", id)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

func (s *Store) ReleasePaidSMS(id string) error {
	_, e := s.DB.Exec("UPDATE sms_orders SET status='paid' WHERE id=? AND status='purchasing'", id)
	return e
}

func (s *Store) ListDueAutoReplace(before string, max, limit int) ([]SMSOrder, error) {
	rows, e := s.DB.Query(smsOrderSelect+` WHERE auto_replace=1 AND status='waiting' AND COALESCE(code,'')='' AND (?=0 OR replace_attempts<?) AND last_number_at<>'' AND last_number_at<=? ORDER BY last_number_at LIMIT ?`, max, max, before, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []SMSOrder
	for rows.Next() {
		var o SMSOrder
		if e = scanSMS(rows, &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) ListReplacing(limit int) ([]SMSOrder, error) {
	rows, e := s.DB.Query(smsOrderSelect+` WHERE status='replacing' ORDER BY last_number_at LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []SMSOrder
	for rows.Next() {
		var o SMSOrder
		if e = scanSMS(rows, &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) ClaimAutoReplace(id, upstream string) (bool, error) {
	r, e := s.DB.Exec("UPDATE sms_orders SET status='replacing' WHERE id=? AND upstream_id=? AND status='waiting' AND COALESCE(code,'')=''", id, upstream)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

func (s *Store) ReleaseAutoReplace(id string, disable bool) error {
	_, e := s.DB.Exec("UPDATE sms_orders SET status='waiting',auto_replace=CASE WHEN ? THEN 0 ELSE auto_replace END WHERE id=? AND status='replacing'", disable, id)
	return e
}

func (s *Store) TouchReplacing(id string) error {
	_, e := s.DB.Exec("UPDATE sms_orders SET last_number_at=? WHERE id=? AND status='replacing'", time.Now().UTC().Format(time.RFC3339), id)
	return e
}

func (s *Store) ReplaceActivation(id, oldUpstream, upstream, phone string, cost float64) error {
	return s.ReplaceActivationWithProvider(id, "hero", oldUpstream, "hero", upstream, phone, cost)
}

func (s *Store) ReplaceActivationWithProvider(id, oldProvider, oldUpstream, provider, upstream, phone string, cost float64) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	oldProvider = providerOrHero(oldProvider)
	provider = providerOrHero(provider)
	if _, e = tx.Exec("UPDATE sms_attempts SET status='cancelled',ended_at=? WHERE order_id=? AND upstream_provider=? AND upstream_id=?", now, id, oldProvider, oldUpstream); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE sms_orders SET upstream_id=?,upstream_provider=?,phone=?,upstream_cost=?,status='waiting',replace_attempts=replace_attempts+1,last_number_at=? WHERE id=? AND status='replacing'", upstream, provider, phone, cost, now, id); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO sms_attempts(order_id,upstream_id,upstream_provider,phone,status,upstream_cost,started_at)VALUES(?,?,?,?,?,?,?)", id, upstream, provider, phone, "waiting", cost, now); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) EndCurrentAttempt(id, upstream, status string) error {
	return s.EndCurrentAttemptWithProvider(id, "hero", upstream, status)
}

func (s *Store) EndCurrentAttemptWithProvider(id, provider, upstream, status string) error {
	_, e := s.DB.Exec("UPDATE sms_attempts SET status=?,ended_at=? WHERE order_id=? AND upstream_provider=? AND upstream_id=?", status, time.Now().UTC().Format(time.RFC3339), id, providerOrHero(provider), upstream)
	return e
}

func (s *Store) CreateRecharge(r Recharge) error {
	_, e := s.DB.Exec(`INSERT INTO recharges(id,user_id,amount_fen,provider,pay_type,status,provider_id,refund_provider_id,refunded_at,token,reference,created_at)VALUES(?,?,?,?,?,'pending','','','',?,?,?)`, r.ID, r.UserID, r.AmountFen, r.Provider, r.PayType, r.Token, r.Reference, r.CreatedAt)
	return e
}
func (s *Store) SetRechargeStatus(id, status string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var reference string
	if e = tx.QueryRow("SELECT reference FROM recharges WHERE id=?", id).Scan(&reference); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE recharges SET status=? WHERE id=? AND status='pending'", status, id); e != nil {
		return e
	}
	if reference != "" {
		if _, e = tx.Exec("UPDATE sms_orders SET status='payment_failed' WHERE id=? AND status='awaiting_payment'", reference); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteExpiredUnpaidSMS(before string, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT o.id,r.id FROM sms_orders o JOIN recharges r ON r.reference=o.id WHERE o.status='awaiting_payment' AND r.status='pending' AND o.created_at<=? ORDER BY o.created_at LIMIT ?`, before, limit)
	if err != nil {
		return 0, err
	}
	type expiredOrder struct{ orderID, rechargeID string }
	var expired []expiredOrder
	for rows.Next() {
		var item expiredOrder
		if err = rows.Scan(&item.orderID, &item.rechargeID); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, item)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	var deleted int64
	for _, item := range expired {
		result, deleteErr := tx.Exec(`DELETE FROM recharges WHERE id=? AND reference=? AND status='pending'`, item.rechargeID, item.orderID)
		if deleteErr != nil {
			return 0, deleteErr
		}
		removedRecharge, _ := result.RowsAffected()
		if removedRecharge != 1 {
			continue
		}
		result, deleteErr = tx.Exec(`DELETE FROM sms_orders WHERE id=? AND status='awaiting_payment'`, item.orderID)
		if deleteErr != nil {
			return 0, deleteErr
		}
		removedOrder, _ := result.RowsAffected()
		deleted += removedOrder
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}
func (s *Store) GetRecharge(id string) (Recharge, error) {
	var r Recharge
	e := s.DB.QueryRow(`SELECT id,user_id,amount_fen,provider,pay_type,status,COALESCE(provider_id,''),COALESCE(refund_provider_id,''),COALESCE(refunded_at,''),token,reference,created_at FROM recharges WHERE id=?`, id).Scan(&r.ID, &r.UserID, &r.AmountFen, &r.Provider, &r.PayType, &r.Status, &r.ProviderID, &r.RefundProviderID, &r.RefundedAt, &r.Token, &r.Reference, &r.CreatedAt)
	return r, e
}

func (s *Store) GetRechargeByReference(orderID string) (Recharge, error) {
	var r Recharge
	e := s.DB.QueryRow(`SELECT id,user_id,amount_fen,provider,pay_type,status,COALESCE(provider_id,''),COALESCE(refund_provider_id,''),COALESCE(refunded_at,''),token,reference,created_at FROM recharges WHERE reference=? ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&r.ID, &r.UserID, &r.AmountFen, &r.Provider, &r.PayType, &r.Status, &r.ProviderID, &r.RefundProviderID, &r.RefundedAt, &r.Token, &r.Reference, &r.CreatedAt)
	return r, e
}
func (s *Store) IsSMSPaymentOrder(orderID string) (bool, error) {
	var exists int
	e := s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM recharges WHERE reference=?)", orderID).Scan(&exists)
	return exists == 1, e
}
func (s *Store) CompleteSMSPayment(id, providerID string) (string, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	var reference, status, existingProviderID string
	e = tx.QueryRow("SELECT reference,status,COALESCE(provider_id,'') FROM recharges WHERE id=?", id).Scan(&reference, &status, &existingProviderID)
	if e != nil {
		return "", e
	}
	if reference == "" {
		return "", errors.New("payment is not linked to an SMS order")
	}
	if status == "paid" {
		if providerID != "" && existingProviderID != "" && providerID != existingProviderID {
			return "", errors.New("provider transaction mismatch")
		}
		return reference, tx.Commit()
	}
	if status != "pending" {
		return "", errors.New("payment is not pending")
	}
	if _, e = tx.Exec("UPDATE recharges SET status='paid',provider_id=CASE WHEN ?='' THEN provider_id ELSE ? END WHERE id=?", providerID, providerID, id); e != nil {
		return "", e
	}
	if _, e = tx.Exec("UPDATE sms_orders SET status='paid' WHERE id=? AND status='awaiting_payment'", reference); e != nil {
		return "", e
	}
	return reference, tx.Commit()
}

func (s *Store) MarkRechargeRefunded(id, refundProviderID string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	var reference string
	if err = tx.QueryRow("SELECT reference FROM recharges WHERE id=?", id).Scan(&reference); err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE recharges SET status='refunded',refund_provider_id=?,refunded_at=? WHERE id=? AND status='paid' AND COALESCE(refunded_at,'')=''", refundProviderID, now, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	if reference != "" {
		if _, err = tx.Exec("UPDATE sms_orders SET refunded=1 WHERE id=?", reference); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CountRecentRefundsByUser(userID int64, since string) (int, error) {
	var count int
	err := s.DB.QueryRow("SELECT COUNT(*) FROM recharges WHERE user_id=? AND status='refunded' AND refunded_at>=?", userID, since).Scan(&count)
	return count, err
}
func (s *Store) CreditRecharge(id, providerID string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var uid, amount int64
	var status, existingProviderID, reference string
	e = tx.QueryRow("SELECT user_id,amount_fen,status,COALESCE(provider_id,''),reference FROM recharges WHERE id=?", id).Scan(&uid, &amount, &status, &existingProviderID, &reference)
	if e != nil {
		return e
	}
	if reference != "" {
		return errors.New("SMS order payments cannot credit account balance")
	}
	if status == "paid" {
		if providerID != "" && existingProviderID != "" && providerID != existingProviderID {
			return errors.New("provider transaction mismatch")
		}
		return tx.Commit()
	}
	if status != "pending" {
		return errors.New("订单状态不可入账")
	}
	if _, e = tx.Exec("UPDATE recharges SET status='paid',provider_id=CASE WHEN ?='' THEN provider_id ELSE ? END WHERE id=?", providerID, providerID, id); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE users SET balance_fen=balance_fen+? WHERE id=?", amount, uid); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO ledger(user_id,amount_fen,kind,reference,created_at)VALUES(?,?,?,?,?)", uid, amount, "recharge", "recharge:"+id, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) AdminOverview(today string) (AdminStats, []AdminOrder, error) {
	var stats AdminStats
	err := s.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sms_orders),
		(SELECT COUNT(*) FROM sms_orders WHERE created_at>=?),
		(SELECT COUNT(*) FROM sms_orders WHERE status='code_received'),
		COALESCE((SELECT SUM(amount_fen) FROM recharges WHERE status='paid' AND reference<>''),0),
		(SELECT COUNT(*) FROM users),
		(SELECT COUNT(DISTINCT user_id) FROM support_messages WHERE sender='user' AND read_at IS NULL)`, today).
		Scan(&stats.OrdersTotal, &stats.OrdersToday, &stats.OrdersSuccessful, &stats.RevenueFen, &stats.UsersTotal, &stats.OpenChats)
	if err != nil {
		return AdminStats{}, nil, err
	}
	rows, err := s.DB.Query(`SELECT o.id,u.email,o.country,o.service,o.status,o.upstream_provider,o.price_fen,o.created_at
		FROM sms_orders o JOIN users u ON u.id=o.user_id ORDER BY o.created_at DESC LIMIT 50`)
	if err != nil {
		return AdminStats{}, nil, err
	}
	defer rows.Close()
	orders := []AdminOrder{}
	for rows.Next() {
		var order AdminOrder
		if err = rows.Scan(&order.ID, &order.Email, &order.Country, &order.Service, &order.Status, &order.Provider, &order.PriceFen, &order.CreatedAt); err != nil {
			return AdminStats{}, nil, err
		}
		orders = append(orders, order)
	}
	return stats, orders, rows.Err()
}

func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.DB.Query("SELECT key,value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) UpdateSettings(values map[string]string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for key, value := range values {
		if _, err = tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddSupportMessage(userID int64, sender, body string) (SupportMessage, error) {
	message := SupportMessage{UserID: userID, Sender: sender, Body: body, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	result, err := s.DB.Exec("INSERT INTO support_messages(user_id,sender,body,created_at) VALUES(?,?,?,?)", userID, sender, body, message.CreatedAt)
	if err != nil {
		return SupportMessage{}, err
	}
	message.ID, _ = result.LastInsertId()
	return message, nil
}

func (s *Store) SupportMessages(userID int64, reader string, limit int) ([]SupportMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(`SELECT id,user_id,sender,body,created_at FROM (
		SELECT id,user_id,sender,body,created_at FROM support_messages WHERE user_id=? ORDER BY id DESC LIMIT ?
	) ORDER BY id`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []SupportMessage{}
	for rows.Next() {
		var message SupportMessage
		if err = rows.Scan(&message.ID, &message.UserID, &message.Sender, &message.Body, &message.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	opposite := "admin"
	if reader == "admin" {
		opposite = "user"
	}
	_, err = s.DB.Exec("UPDATE support_messages SET read_at=? WHERE user_id=? AND sender=? AND read_at IS NULL", time.Now().UTC().Format(time.RFC3339), userID, opposite)
	return messages, err
}

func (s *Store) SupportThreads() ([]SupportThread, error) {
	rows, err := s.DB.Query(`SELECT u.id,u.email,m.body,m.created_at,
		(SELECT COUNT(*) FROM support_messages unread WHERE unread.user_id=u.id AND unread.sender='user' AND unread.read_at IS NULL)
		FROM users u JOIN support_messages m ON m.id=(SELECT MAX(last.id) FROM support_messages last WHERE last.user_id=u.id)
		ORDER BY m.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	threads := []SupportThread{}
	for rows.Next() {
		var thread SupportThread
		if err = rows.Scan(&thread.UserID, &thread.Email, &thread.LastMessage, &thread.LastAt, &thread.Unread); err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

func (s *Store) UserExists(userID int64) bool {
	var exists int
	return s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE id=?)", userID).Scan(&exists) == nil && exists == 1
}

func (s *Store) AdminUsers(query string) ([]AdminUser, error) {
	like := "%" + strings.TrimSpace(query) + "%"
	rows, err := s.DB.Query(`SELECT u.id,u.email,u.balance_fen,u.disabled,
		(SELECT COUNT(*) FROM sms_orders o WHERE o.user_id=u.id),
		COALESCE((SELECT SUM(r.amount_fen) FROM recharges r WHERE r.user_id=u.id AND r.status='paid' AND r.reference<>''),0),u.created_at
		FROM users u WHERE ?='' OR u.email LIKE ? ORDER BY u.created_at DESC LIMIT 200`, strings.TrimSpace(query), like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []AdminUser{}
	for rows.Next() {
		var user AdminUser
		if err = rows.Scan(&user.ID, &user.Email, &user.BalanceFen, &user.Disabled, &user.Orders, &user.SpentFen, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) SetUserDisabled(userID int64, disabled bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE users SET disabled=? WHERE id=?", disabled, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	if disabled {
		if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AdminOrders(query, status string) ([]AdminOrder, error) {
	query = strings.TrimSpace(query)
	like := "%" + query + "%"
	rows, err := s.DB.Query(`SELECT o.id,u.email,o.country,o.service,o.status,o.upstream_provider,COALESCE(o.phone,''),o.price_fen,o.created_at
		FROM sms_orders o JOIN users u ON u.id=o.user_id
		WHERE (?='' OR o.id LIKE ? OR u.email LIKE ? OR o.phone LIKE ?) AND (?='' OR o.status=?)
		ORDER BY o.created_at DESC LIMIT 200`, query, like, like, like, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := []AdminOrder{}
	for rows.Next() {
		var order AdminOrder
		if err = rows.Scan(&order.ID, &order.Email, &order.Country, &order.Service, &order.Status, &order.Provider, &order.Phone, &order.PriceFen, &order.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *Store) AdminOrderLogs(orderID string) ([]AdminOrderLogEntry, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, sql.ErrNoRows
	}
	var (
		orderCreated string
		orderStatus  string
		orderCode    string
		orderPhone   string
	)
	err := s.DB.QueryRow(`SELECT created_at,status,COALESCE(code,''),COALESCE(phone,'') FROM sms_orders WHERE id=?`, orderID).
		Scan(&orderCreated, &orderStatus, &orderCode, &orderPhone)
	if err != nil {
		return nil, err
	}
	logs := []AdminOrderLogEntry{{
		Time:   orderCreated,
		Type:   "order",
		Title:  "订单创建",
		Detail: "订单已创建，等待支付或后续处理",
	}}

	rows, err := s.DB.Query(`SELECT created_at,status,provider,pay_type,amount_fen,COALESCE(provider_id,''),COALESCE(refund_provider_id,''),COALESCE(refunded_at,'')
		FROM recharges WHERE reference=? ORDER BY created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var createdAt, status, provider, payType, providerID, refundProviderID, refundedAt string
		var amountFen int64
		if err = rows.Scan(&createdAt, &status, &provider, &payType, &amountFen, &providerID, &refundProviderID, &refundedAt); err != nil {
			return nil, err
		}
		detail := "通道：" + provider + " / 方式：" + payType + " / 金额：" + formatFen(amountFen)
		if providerID != "" {
			detail += " / 支付流水号：" + providerID
		}
		logs = append(logs, AdminOrderLogEntry{
			Time:   createdAt,
			Type:   "payment",
			Title:  "支付单创建",
			Detail: detail,
		})
		if status == "paid" || status == "refunded" {
			logs = append(logs, AdminOrderLogEntry{
				Time:   createdAt,
				Type:   "payment",
				Title:  "支付成功",
				Detail: detailOrDefault(providerID, detail),
			})
		}
		statusTitle := map[string]string{
			"pending":  "等待支付",
			"paid":     "支付成功",
			"failed":   "支付失败",
			"refunded": "已原路退款",
		}[status]
		if statusTitle == "" {
			statusTitle = "支付状态：" + status
		}
		if status != "paid" {
			logs = append(logs, AdminOrderLogEntry{
				Time:   createdAt,
				Type:   "payment",
				Title:  statusTitle,
				Detail: detail,
			})
		}
		if refundedAt != "" {
			refundDetail := "退款已完成"
			if refundProviderID != "" {
				refundDetail += " / 退款流水号：" + refundProviderID
			}
			logs = append(logs, AdminOrderLogEntry{
				Time:   refundedAt,
				Type:   "refund",
				Title:  "原路退款成功",
				Detail: refundDetail,
			})
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.DB.Query(`SELECT upstream_provider,upstream_id,phone,status,upstream_cost,started_at,COALESCE(ended_at,'')
		FROM sms_attempts WHERE order_id=? ORDER BY started_at,id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var provider, upstreamID, phone, status, startedAt, endedAt string
		var upstreamCost float64
		if err = rows.Scan(&provider, &upstreamID, &phone, &status, &upstreamCost, &startedAt, &endedAt); err != nil {
			return nil, err
		}
		logs = append(logs, AdminOrderLogEntry{
			Time:   startedAt,
			Type:   "attempt",
			Title:  "号码分配",
			Detail: "上游：" + provider + " / 上游单号：" + upstreamID + " / 号码：" + phone + " / 成本：" + strconv.FormatFloat(upstreamCost, 'f', 3, 64),
		})
		if endedAt != "" {
			statusTitle := map[string]string{
				"finished":  "本次尝试已完成",
				"cancelled": "本次尝试已取消",
				"waiting":   "本次尝试等待中",
			}[status]
			if statusTitle == "" {
				statusTitle = "本次尝试状态：" + status
			}
			logs = append(logs, AdminOrderLogEntry{
				Time:   endedAt,
				Type:   "attempt",
				Title:  statusTitle,
				Detail: "上游：" + provider + " / 上游单号：" + upstreamID,
			})
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.DB.Query(`SELECT action,COALESCE(detail,''),created_at FROM admin_audit WHERE target=? ORDER BY created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var action, detail, createdAt string
		if err = rows.Scan(&action, &detail, &createdAt); err != nil {
			return nil, err
		}
		logs = append(logs, AdminOrderLogEntry{
			Time:   createdAt,
			Type:   "audit",
			Title:  orderAuditTitle(action),
			Detail: detailOrDefault(detail, "后台记录"),
		})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	finalDetail := "当前状态：" + orderStatus
	if orderPhone != "" {
		finalDetail += " / 当前号码：" + orderPhone
	}
	if orderCode != "" {
		finalDetail += " / 验证码：" + orderCode
	}
	logs = append(logs, AdminOrderLogEntry{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Type:   "snapshot",
		Title:  "当前订单快照",
		Detail: finalDetail,
	})

	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].Time == logs[j].Time {
			return logs[i].Title < logs[j].Title
		}
		return logs[i].Time < logs[j].Time
	})
	return logs, nil
}

func (s *Store) AdminCloseAwaitingPayment(id, reason string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE recharges SET status='failed' WHERE reference=? AND status='pending'", id); err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE sms_orders SET status=?,auto_replace=0 WHERE id=? AND status='awaiting_payment'", reason, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) SetSMSOrderStatus(id, status string) error {
	_, err := s.DB.Exec("UPDATE sms_orders SET status=?,auto_replace=0 WHERE id=?", status, id)
	return err
}

func (s *Store) AdminPayments(query, status string) ([]AdminPayment, error) {
	query = strings.TrimSpace(query)
	like := "%" + query + "%"
	rows, err := s.DB.Query(`SELECT r.id,u.email,r.provider,r.pay_type,r.status,COALESCE(r.provider_id,''),r.reference,r.amount_fen,r.created_at
		FROM recharges r JOIN users u ON u.id=r.user_id
		WHERE (?='' OR r.id LIKE ? OR u.email LIKE ? OR r.provider_id LIKE ? OR r.reference LIKE ?) AND (?='' OR r.status=?)
		ORDER BY r.created_at DESC LIMIT 200`, query, like, like, like, like, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	payments := []AdminPayment{}
	for rows.Next() {
		var payment AdminPayment
		if err = rows.Scan(&payment.ID, &payment.Email, &payment.Provider, &payment.PayType, &payment.Status, &payment.ProviderID, &payment.OrderID, &payment.AmountFen, &payment.CreatedAt); err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}
	return payments, rows.Err()
}

func (s *Store) Announcements(activeOnly bool) ([]Announcement, error) {
	rows, err := s.DB.Query(`SELECT id,title,body,active,created_at,updated_at FROM announcements WHERE ?=0 OR active=1 ORDER BY id DESC LIMIT 100`, activeOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Announcement{}
	for rows.Next() {
		var item Announcement
		if err = rows.Scan(&item.ID, &item.Title, &item.Body, &item.Active, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SaveAnnouncement(item Announcement) (Announcement, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if item.ID == 0 {
		result, err := s.DB.Exec("INSERT INTO announcements(title,body,active,created_at,updated_at) VALUES(?,?,?,?,?)", item.Title, item.Body, item.Active, now, now)
		if err != nil {
			return Announcement{}, err
		}
		item.ID, _ = result.LastInsertId()
		item.CreatedAt = now
		item.UpdatedAt = now
		return item, nil
	}
	result, err := s.DB.Exec("UPDATE announcements SET title=?,body=?,active=?,updated_at=? WHERE id=?", item.Title, item.Body, item.Active, now, item.ID)
	if err != nil {
		return Announcement{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Announcement{}, sql.ErrNoRows
	}
	item.UpdatedAt = now
	return item, nil
}

func (s *Store) DeleteAnnouncement(id int64) error {
	result, err := s.DB.Exec("DELETE FROM announcements WHERE id=?", id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Audit(action, target, detail string) {
	_, _ = s.DB.Exec("INSERT INTO admin_audit(action,target,detail,created_at) VALUES(?,?,?,?)", action, target, detail, time.Now().UTC().Format(time.RFC3339))
}

func (s *Store) AuditEvents() ([]AuditEvent, error) {
	rows, err := s.DB.Query("SELECT id,action,target,detail,created_at FROM admin_audit ORDER BY id DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		if err = rows.Scan(&event.ID, &event.Action, &event.Target, &event.Detail, &event.At); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func formatFen(fen int64) string {
	return "¥" + strconv.FormatFloat(float64(fen)/100, 'f', 2, 64)
}

func orderAuditTitle(action string) string {
	switch action {
	case "order.close":
		return "管理员关闭订单"
	case "order.close.refunded":
		return "管理员关闭并退款"
	case "order.close.refund_skipped":
		return "管理员关闭，退款被风控拦截"
	default:
		return action
	}
}

func detailOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
