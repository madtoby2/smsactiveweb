package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestProviderScopedActivationIDs(t *testing.T) {
	s := testStore(t)
	u, _, err := s.Register("providers@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		order, payment, provider string
	}{{"SHERO", "PHERO", "hero"}, {"SSMSMAN", "PSMSMAN", "smsman"}} {
		order := SMSOrder{ID: tc.order, UserID: u.ID, UpstreamProvider: tc.provider, Country: "2", Service: "tg", UpstreamCost: 1, PriceFen: 820, CreatedAt: created}
		payment := Recharge{ID: tc.payment, UserID: u.ID, AmountFen: 820, Provider: "epay", PayType: "2", Token: tc.payment, CreatedAt: created}
		if err = s.CreateSMSPayment(u, order, payment); err != nil {
			t.Fatal(err)
		}
		if err = s.ActivateSMSWithProvider(order.ID, tc.provider, "12345", "+77000000000", 1); err != nil {
			t.Fatalf("activate %s: %v", tc.provider, err)
		}
		got, getErr := s.GetSMS(order.ID, u.ID)
		if getErr != nil || got.UpstreamProvider != tc.provider || got.UpstreamID != "12345" {
			t.Fatalf("order=%+v err=%v", got, getErr)
		}
		encoded, _ := json.Marshal(got)
		if strings.Contains(string(encoded), "Upstream") || strings.Contains(string(encoded), "12345") {
			t.Fatalf("upstream internals leaked in JSON: %s", encoded)
		}
	}
}

func TestLegacyOrdersMigrateToHeroProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE users(id INTEGER PRIMARY KEY,email TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL,balance_fen INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL);
CREATE TABLE sms_orders(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,upstream_id TEXT UNIQUE,country TEXT NOT NULL,service TEXT NOT NULL,phone TEXT,status TEXT NOT NULL,code TEXT,upstream_cost REAL NOT NULL,price_fen INTEGER NOT NULL,refunded INTEGER NOT NULL DEFAULT 0,auto_replace INTEGER NOT NULL DEFAULT 0,replace_attempts INTEGER NOT NULL DEFAULT 0,last_number_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);
CREATE TABLE sms_attempts(id INTEGER PRIMARY KEY,order_id TEXT NOT NULL,upstream_id TEXT NOT NULL UNIQUE,phone TEXT NOT NULL,status TEXT NOT NULL,upstream_cost REAL NOT NULL,started_at TEXT NOT NULL,ended_at TEXT);
INSERT INTO users(id,email,password_hash,created_at) VALUES(1,'legacy@example.com','unused','2026-01-01T00:00:00Z');
INSERT INTO sms_orders(id,user_id,upstream_id,country,service,phone,status,upstream_cost,price_fen,created_at) VALUES('SLEGACY',1,'777','2','tg','7700','waiting',1,820,'2026-01-01T00:00:00Z');
INSERT INTO sms_attempts(order_id,upstream_id,phone,status,upstream_cost,started_at) VALUES('SLEGACY','777','7700','waiting',1,'2026-01-01T00:00:00Z');`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	legacy, err := s.GetSMSByID("SLEGACY")
	if err != nil || legacy.UpstreamProvider != "hero" {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	foreignKeys, err := s.DB.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if foreignKeys.Next() {
		foreignKeys.Close()
		t.Fatal("provider migration left an invalid foreign key")
	}
	foreignKeys.Close()

	u, _, err := s.Register("smsman@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339)
	order := SMSOrder{ID: "SNEW", UserID: u.ID, UpstreamProvider: "smsman", Country: "2", Service: "tg", UpstreamCost: 1, PriceFen: 820, CreatedAt: created}
	payment := Recharge{ID: "PNEW", UserID: u.ID, AmountFen: 820, Provider: "epay", PayType: "2", Token: "PNEW", CreatedAt: created}
	if err = s.CreateSMSPayment(u, order, payment); err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateSMSWithProvider(order.ID, "smsman", "777", "7701", 1); err != nil {
		t.Fatalf("provider-scoped ID should not conflict after migration: %v", err)
	}
}

func TestSMSRefundIsIdempotent(t *testing.T) {
	s := testStore(t)
	u, _, err := s.Register("refund@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE users SET balance_fen=1000 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	o := SMSOrder{ID: "S1", UserID: u.ID, Country: "6", Service: "tg", UpstreamCost: .5, PriceFen: 460, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err = s.CreateSMS(u, o); err != nil {
		t.Fatal(err)
	}
	if err = s.RefundSMS(o.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err = s.RefundSMS(o.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	var balance int64
	if err = s.DB.QueryRow("SELECT balance_fen FROM users WHERE id=?", u.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 1000 {
		t.Fatalf("balance=%d, want 1000", balance)
	}
}

func TestRechargeCreditIsIdempotentAndTransactionUnique(t *testing.T) {
	s := testStore(t)
	u, _, err := s.Register("pay@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{"R1", "R2"} {
		if err = s.CreateRecharge(Recharge{ID: id, UserID: u.ID, AmountFen: 1000, Provider: "yishoumi", PayType: "2", Token: id, CreatedAt: created}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.CreditRecharge("R1", "TX-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreditRecharge("R1", "TX-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreditRecharge("R2", "TX-1"); err == nil {
		t.Fatal("same provider transaction credited twice")
	}
	var balance int64
	if err = s.DB.QueryRow("SELECT balance_fen FROM users WHERE id=?", u.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 1000 {
		t.Fatalf("balance=%d, want 1000", balance)
	}
}

func TestDeleteSessionInvalidatesToken(t *testing.T) {
	s := testStore(t)
	_, token, err := s.Register("session@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UserByToken(token); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSession(token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UserByToken(token); err == nil {
		t.Fatal("deleted session still accepted")
	}
}
