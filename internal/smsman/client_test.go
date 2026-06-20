package smsman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientProtocol(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "secret" {
			t.Fatal("missing token")
		}
		switch r.URL.Path {
		case "/countries":
			_, _ = w.Write([]byte(`{"2":{"id":2,"title":"Kazakhstan"},"1":{"id":1,"name":"Russia"}}`))
		case "/applications":
			_, _ = w.Write([]byte(`{"1":{"id":1,"title":"Telegram"}}`))
		case "/limits":
			if r.URL.Query().Get("country_id") != "2" {
				t.Fatal("missing limits filters")
			}
			_, _ = w.Write([]byte(`{"1":{"application_id":1,"numbers":3}}`))
		case "/get-prices":
			_, _ = w.Write([]byte(`{"1":{"count":3,"cost":12.5,"application_id":1}}`))
		case "/get-number":
			_, _ = w.Write([]byte(`{"request_id":1234,"number":"77001234567"}`))
		case "/get-sms":
			_, _ = w.Write([]byte(`{"sms_code":"9081"}`))
		case "/set-status":
			if r.URL.Query().Get("status") != "reject" || r.URL.Query().Get("request_id") != "1234" {
				t.Fatal("invalid reject request")
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New("secret", server.URL)
	countries, err := client.Countries(context.Background())
	if err != nil || len(countries) != 2 || countries[1].Name != "Kazakhstan" {
		t.Fatalf("countries=%v err=%v", countries, err)
	}
	applications, err := client.Applications(context.Background())
	if err != nil || len(applications) != 1 || applications[0].Name != "Telegram" {
		t.Fatalf("applications=%v err=%v", applications, err)
	}
	limits, err := client.Limits(context.Background(), 2, 1)
	if err != nil || !json.Valid(limits) {
		t.Fatalf("limits=%s err=%v", limits, err)
	}
	quotes, err := client.Quotes(context.Background(), 2)
	if err != nil || quotes[1].Price != 12.5 || quotes[1].Count != 3 {
		t.Fatalf("quotes=%v err=%v", quotes, err)
	}
	activation, err := client.Acquire(context.Background(), 2, 1)
	if err != nil || activation.ID != "1234" || activation.Phone != "77001234567" {
		t.Fatalf("activation=%+v err=%v", activation, err)
	}
	code, err := client.SMS(context.Background(), activation.ID)
	if err != nil || code != "9081" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if err = client.Reject(context.Background(), activation.ID); err != nil {
		t.Fatal(err)
	}
}

func TestQuotesParsesNestedLimitPayloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"2":{"1":{"price":"12.5","count":"3"},"7":{"cost":8,"count":2}}}`))
	}))
	defer server.Close()
	quotes, err := New("secret", server.URL).Quotes(context.Background(), 2)
	if err != nil || len(quotes) != 2 || quotes[1].Price != 12.5 || quotes[7].Price != 8 {
		t.Fatalf("quotes=%v err=%v", quotes, err)
	}
}

func TestRejectRequiresExplicitSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	defer server.Close()
	if err := New("secret", server.URL).Reject(context.Background(), "123"); err == nil {
		t.Fatal("reject succeeded without provider confirmation")
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":"no_numbers","error_msg":"No numbers"}`))
	}))
	defer server.Close()

	_, err := New("secret", server.URL).Acquire(context.Background(), 2, 1)
	if ErrorCode(err) != "no_numbers" {
		t.Fatalf("err=%v", err)
	}
}
