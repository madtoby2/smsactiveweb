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
	"strings"
	"time"
)

type Store struct{ DB *sql.DB }
type User struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	Balance int64  `json:"balanceFen"`
}
type SMSOrder struct {
	ID               string `json:"id"`
	UserID           int64  `json:"-"`
	UpstreamID       string `json:"-"`
	UpstreamProvider string `json:"-"`
	UpstreamCountry  string `json:"-"`
	UpstreamService  string `json:"-"`
	Country          string
	Service          string
	Phone            string
	Status           string
	Code             string
	UpstreamCost     float64 `json:"-"`
	PriceFen         int64
	AutoReplace      bool
	ReplaceAttempts  int
	LastNumberAt     string
	CreatedAt        string
}
type Recharge struct {
	ID                                                      string `json:"id"`
	UserID, AmountFen                                       int64
	Provider, PayType, Status, ProviderID, Token, Reference string
	CreatedAt                                               string
}

const smsOrderSelect = `SELECT id,user_id,COALESCE(upstream_id,''),upstream_provider,COALESCE(upstream_country,''),COALESCE(upstream_service,''),country,service,COALESCE(phone,''),status,COALESCE(code,''),upstream_cost,price_fen,auto_replace,replace_attempts,last_number_at,created_at FROM sms_orders`

type scanner interface {
	Scan(dest ...any) error
}

func scanSMS(row scanner, order *SMSOrder) error {
	return row.Scan(&order.ID, &order.UserID, &order.UpstreamID, &order.UpstreamProvider, &order.UpstreamCountry, &order.UpstreamService, &order.Country, &order.Service, &order.Phone, &order.Status, &order.Code, &order.UpstreamCost, &order.PriceFen, &order.AutoReplace, &order.ReplaceAttempts, &order.LastNumberAt, &order.CreatedAt)
}

func providerOrHero(provider string) string {
	if provider == "" {
		return "hero"
	}
	return provider
}

const sessionLifetime = 30 * 24 * time.Hour

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
CREATE TABLE IF NOT EXISTS sms_orders(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,upstream_id TEXT,upstream_provider TEXT NOT NULL DEFAULT 'hero',upstream_country TEXT NOT NULL DEFAULT '',upstream_service TEXT NOT NULL DEFAULT '',country TEXT NOT NULL,service TEXT NOT NULL,phone TEXT,status TEXT NOT NULL,code TEXT,upstream_cost REAL NOT NULL,price_fen INTEGER NOT NULL,refunded INTEGER NOT NULL DEFAULT 0,auto_replace INTEGER NOT NULL DEFAULT 0,replace_attempts INTEGER NOT NULL DEFAULT 0,last_number_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id),UNIQUE(upstream_provider,upstream_id));
CREATE TABLE IF NOT EXISTS sms_attempts(id INTEGER PRIMARY KEY,order_id TEXT NOT NULL,upstream_id TEXT NOT NULL,upstream_provider TEXT NOT NULL DEFAULT 'hero',phone TEXT NOT NULL,status TEXT NOT NULL,upstream_cost REAL NOT NULL,started_at TEXT NOT NULL,ended_at TEXT,FOREIGN KEY(order_id) REFERENCES sms_orders(id),UNIQUE(upstream_provider,upstream_id));
CREATE TABLE IF NOT EXISTS recharges(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,amount_fen INTEGER NOT NULL,provider TEXT NOT NULL,pay_type TEXT NOT NULL,status TEXT NOT NULL,provider_id TEXT,token TEXT NOT NULL,reference TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id));
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
		"ALTER TABLE sms_orders ADD COLUMN upstream_country TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sms_orders ADD COLUMN upstream_service TEXT NOT NULL DEFAULT ''",
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
		`CREATE TABLE sms_orders_new(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,upstream_id TEXT,upstream_provider TEXT NOT NULL DEFAULT 'hero',upstream_country TEXT NOT NULL DEFAULT '',upstream_service TEXT NOT NULL DEFAULT '',country TEXT NOT NULL,service TEXT NOT NULL,phone TEXT,status TEXT NOT NULL,code TEXT,upstream_cost REAL NOT NULL,price_fen INTEGER NOT NULL,refunded INTEGER NOT NULL DEFAULT 0,auto_replace INTEGER NOT NULL DEFAULT 0,replace_attempts INTEGER NOT NULL DEFAULT 0,last_number_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id),UNIQUE(upstream_provider,upstream_id))`,
		`INSERT INTO sms_orders_new(id,user_id,upstream_id,upstream_provider,upstream_country,upstream_service,country,service,phone,status,code,upstream_cost,price_fen,refunded,auto_replace,replace_attempts,last_number_at,created_at) SELECT id,user_id,upstream_id,'hero',upstream_country,upstream_service,country,service,phone,status,code,upstream_cost,price_fen,refunded,auto_replace,replace_attempts,last_number_at,created_at FROM sms_orders`,
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
	return User{id, email, 0}, token, e
}
func (s *Store) Login(email, password string) (User, string, error) {
	var u User
	var hash string
	e := s.DB.QueryRow("SELECT id,email,balance_fen,password_hash FROM users WHERE email=?", email).Scan(&u.ID, &u.Email, &u.Balance, &hash)
	if e != nil || !VerifyPassword(hash, password) {
		return User{}, "", errors.New("邮箱或密码错误")
	}
	token, th := Token()
	_, e = s.DB.Exec("INSERT INTO sessions VALUES(?,?,?)", th, u.ID, time.Now().Add(sessionLifetime).UTC().Format(time.RFC3339))
	return u, token, e
}
func (s *Store) UserByToken(raw string) (User, error) {
	h := sha256.Sum256([]byte(raw))
	var u User
	e := s.DB.QueryRow(`SELECT u.id,u.email,u.balance_fen FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, hex.EncodeToString(h[:]), time.Now().UTC().Format(time.RFC3339)).Scan(&u.ID, &u.Email, &u.Balance)
	return u, e
}
func (s *Store) TouchSession(raw string) error {
	h := sha256.Sum256([]byte(raw))
	_, e := s.DB.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=? AND expires_at>? AND expires_at<?`, time.Now().Add(sessionLifetime).UTC().Format(time.RFC3339), hex.EncodeToString(h[:]), time.Now().UTC().Format(time.RFC3339), time.Now().Add(15*24*time.Hour).UTC().Format(time.RFC3339))
	return e
}
func (s *Store) DeleteSession(raw string) error {
	h := sha256.Sum256([]byte(raw))
	_, e := s.DB.Exec("DELETE FROM sessions WHERE token_hash=?", hex.EncodeToString(h[:]))
	return e
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
	_, e = tx.Exec(`INSERT INTO sms_orders(id,user_id,upstream_provider,upstream_country,upstream_service,country,service,status,upstream_cost,price_fen,auto_replace,created_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, u.ID, providerOrHero(o.UpstreamProvider), o.UpstreamCountry, o.UpstreamService, o.Country, o.Service, "purchasing", o.UpstreamCost, o.PriceFen, o.AutoReplace, o.CreatedAt)
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
	if _, e = tx.Exec(`INSERT INTO sms_orders(id,user_id,upstream_provider,upstream_country,upstream_service,country,service,status,upstream_cost,price_fen,auto_replace,created_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, u.ID, providerOrHero(o.UpstreamProvider), o.UpstreamCountry, o.UpstreamService, o.Country, o.Service, "awaiting_payment", o.UpstreamCost, o.PriceFen, o.AutoReplace, o.CreatedAt); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO recharges(id,user_id,amount_fen,provider,pay_type,status,token,reference,created_at)VALUES(?,?,?,?,?,'pending',?,?,?)`, p.ID, u.ID, p.AmountFen, p.Provider, p.PayType, p.Token, o.ID, p.CreatedAt); e != nil {
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
	_, e := s.DB.Exec(`INSERT INTO recharges(id,user_id,amount_fen,provider,pay_type,status,token,reference,created_at)VALUES(?,?,?,?,?,'pending',?,?,?)`, r.ID, r.UserID, r.AmountFen, r.Provider, r.PayType, r.Token, r.Reference, r.CreatedAt)
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
	e := s.DB.QueryRow(`SELECT id,user_id,amount_fen,provider,pay_type,status,COALESCE(provider_id,''),token,reference,created_at FROM recharges WHERE id=?`, id).Scan(&r.ID, &r.UserID, &r.AmountFen, &r.Provider, &r.PayType, &r.Status, &r.ProviderID, &r.Token, &r.Reference, &r.CreatedAt)
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
