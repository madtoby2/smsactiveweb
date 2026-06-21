package config

import "testing"

func TestValidate(t *testing.T) {
	validSandbox := Config{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"}
	if err := validSandbox.Validate(); err != nil {
		t.Fatal(err)
	}
	validSMSMan := Config{SMSManToken: "token", SMSManURL: "https://api.sms-man.example/control", SMSManCNYRate: 0.08, USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"}
	if err := validSMSMan.Validate(); err != nil {
		t.Fatal(err)
	}
	validLive := Config{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "yishoumi", YSMAppID: "app", YSMSecret: "secret", BaseURL: "https://sms.example.com"}
	if err := validLive.Validate(); err != nil {
		t.Fatal(err)
	}
	validEPay := Config{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "epay", EPayPID: "1000", EPayKey: "secret", EPayURL: "https://50pay.example", BaseURL: "https://sms.example.com"}
	if err := validEPay.Validate(); err != nil {
		t.Fatal(err)
	}
	validVerification := Config{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", EmailVerificationRequired: true, SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPFrom: "Cloud SMS <noreply@example.com>", TurnstileSiteKey: "site", TurnstileSecret: "secret"}
	if err := validVerification.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []Config{
		{USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 0, Markup: 1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 7.2, Markup: -1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "unknown"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "yishoumi", BaseURL: "https://sms.example.com"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "yishoumi", YSMAppID: "app", YSMSecret: "secret", BaseURL: "http://sms.example.com"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "epay", EPayURL: "https://50pay.example", BaseURL: "https://sms.example.com"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "epay", EPayPID: "1000", EPayKey: "secret", EPayURL: "http://50pay.example", BaseURL: "https://sms.example.com"},
		{HeroKey: "key", SMSManToken: "smsman", SMSManURL: "http://api.sms-man.example/control", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"},
		{SMSManToken: "smsman", SMSManURL: "https://api.sms-man.example/control", SMSManCNYRate: 0, USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", EmailVerificationRequired: true},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox", EmailVerificationRequired: true, SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPFrom: "noreply@example.com"},
	}
	for i, cfg := range tests {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d unexpectedly valid: %+v", i, cfg)
		}
	}
}
