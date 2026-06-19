package store

import (
	"path/filepath"
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
