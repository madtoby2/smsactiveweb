package hero

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSMSActivateCompatibilityResponses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getCountries":
			w.Write([]byte(`{"6":{"id":6,"eng":"Indonesia","chn":"Indonesia"}}`))
		case "getServicesList":
			w.Write([]byte(`{"status":"success","services":[{"code":"tg","name":"Telegram"}]}`))
		case "getPrices":
			w.Write([]byte(`{"6":{"tg":{"cost":0.2,"count":3}}}`))
		case "getNumberV2":
			w.Write([]byte(`{"activationId":"123","phoneNumber":"628001","activationCost":0.2}`))
		default:
			http.Error(w, "bad action", 400)
		}
	}))
	defer ts.Close()
	c := New("key", ts.URL, "840")
	ctx := context.Background()
	countries, err := c.Countries(ctx)
	if err != nil || len(countries) != 1 || countries[0].ID != 6 {
		t.Fatalf("countries=%v err=%v", countries, err)
	}
	services, err := c.Services(ctx, "6")
	if err != nil || len(services) != 1 || services[0].Code != "tg" {
		t.Fatalf("services=%v err=%v", services, err)
	}
	offers, err := c.Offers(ctx, "6")
	if err != nil || len(offers) != 1 || offers[0].Cost != .2 || offers[0].Count != 3 {
		t.Fatalf("offers=%v err=%v", offers, err)
	}
	a, err := c.Acquire(ctx, "6", "tg", .2)
	if err != nil || a.ID != "123" || a.Phone != "628001" {
		t.Fatalf("activation=%v err=%v", a, err)
	}
}

func TestAPIErrorCodeFromJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"title": "FREE_CANCELLATION_EXPIRED"}`))
	}))
	defer ts.Close()
	_, err := New("key", ts.URL, "840").SetStatus(context.Background(), "123", "8")
	if ErrorCode(err) != "FREE_CANCELLATION_EXPIRED" {
		t.Fatalf("code=%q err=%v", ErrorCode(err), err)
	}
}

func TestCancellationSucceededRequiresExplicitConfirmation(t *testing.T) {
	for _, response := range []string{`ACCESS_CANCEL`, `"ACCESS_CANCEL"`, `{"title":"CANCELED"}`, `{"title":"REFUNDED"}`} {
		if !CancellationSucceeded(response) {
			t.Fatalf("expected cancellation response %q to succeed", response)
		}
	}
	for _, response := range []string{`ACCESS_ACTIVATION`, `ACCESS_READY`, `STATUS_CANCEL`, `{"title":"FINISHED"}`, `garbage`} {
		if CancellationSucceeded(response) {
			t.Fatalf("unexpected cancellation success for %q", response)
		}
	}
}

func TestBalanceParsesAccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "getBalance" {
			http.Error(w, "unexpected action", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("ACCESS_BALANCE:12.34"))
	}))
	defer server.Close()
	balance, err := New("key", server.URL, "840").Balance(context.Background())
	if err != nil || balance != 12.34 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
}
