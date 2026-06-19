package config

import "testing"

func TestValidate(t *testing.T) {
	validSandbox := Config{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"}
	if err := validSandbox.Validate(); err != nil {
		t.Fatal(err)
	}
	validLive := Config{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "yishoumi", YSMAppID: "app", YSMSecret: "secret", BaseURL: "https://sms.example.com"}
	if err := validLive.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []Config{
		{USDCNY: 7.2, Markup: 1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 0, Markup: 1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 7.2, Markup: -1, PayProvider: "sandbox"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "unknown"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "yishoumi", BaseURL: "https://sms.example.com"},
		{HeroKey: "key", USDCNY: 7.2, Markup: 1, PayProvider: "yishoumi", YSMAppID: "app", YSMSecret: "secret", BaseURL: "http://sms.example.com"},
	}
	for i, cfg := range tests {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d unexpectedly valid: %+v", i, cfg)
		}
	}
}
