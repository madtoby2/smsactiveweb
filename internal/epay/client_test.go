package epay

import (
	"net/url"
	"testing"
)

func TestSDKCompatibleSignature(t *testing.T) {
	values := url.Values{
		"pid":          {"1000"},
		"type":         {"wxpay"},
		"notify_url":   {"https://sms.example/notify"},
		"return_url":   {"https://sms.example/return"},
		"out_trade_no": {"P1"},
		"name":         {"SMS activation order"},
		"money":        {"4.60"},
		"empty":        {""},
		"sign_type":    {"MD5"},
	}
	const want = "86dfe6b9364c050df1d1a310905c46ff"
	if got := Sign(values, "secret"); got != want {
		t.Fatalf("signature=%s, want %s", got, want)
	}
	values.Set("sign", want)
	if !Verify(values, "secret") {
		t.Fatal("valid signature rejected")
	}
	values.Set("money", "4.61")
	if Verify(values, "secret") {
		t.Fatal("modified payment accepted")
	}
}

func TestCheckoutURL(t *testing.T) {
	checkout, err := New("1000", "secret", "https://50pay.example/").CheckoutURL("P1", 460, 2, "https://sms.example/notify", "https://sms.example/return")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/submit.php" || u.Query().Get("money") != "4.60" || u.Query().Get("type") != "wxpay" || u.Query().Get("sign_type") != "MD5" {
		t.Fatalf("unexpected checkout URL: %s", checkout)
	}
	if !Verify(u.Query(), "secret") {
		t.Fatal("checkout signature is invalid")
	}
}

func TestMoneyConversion(t *testing.T) {
	for input, want := range map[string]int64{"0.01": 1, "4.6": 460, "4.60": 460, "100": 10000} {
		got, err := ParseMoneyFen(input)
		if err != nil || got != want {
			t.Fatalf("ParseMoneyFen(%q)=%d,%v want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1", "1.001", "1.", ".5", "abc"} {
		if _, err := ParseMoneyFen(input); err == nil {
			t.Fatalf("invalid money %q accepted", input)
		}
	}
}
