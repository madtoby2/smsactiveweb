package turnstile

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerify(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("secret") != "secret" || r.Form.Get("response") != "token" || r.Form.Get("remoteip") != "203.0.113.1" {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	client := New("secret")
	client.URL = server.URL
	if err := client.Verify(t.Context(), "token", "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	defer server.Close()
	client := New("secret")
	client.URL = server.URL
	if err := client.Verify(t.Context(), "bad-token", ""); err == nil {
		t.Fatal("failed Turnstile response was accepted")
	}
}
