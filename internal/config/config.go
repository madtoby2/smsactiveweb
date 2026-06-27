package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port, BaseURL, HeroKey, HeroURL, HeroCurrency      string
	SMSManToken, SMSManURL                             string
	AdminEmail, AdminPassword                          string
	SMTPHost, SMTPUser, SMTPPassword, SMTPFrom         string
	ResendAPIKey, ResendFrom                           string
	TurnstileSiteKey, TurnstileSecret                  string
	TelegramBotToken                                   string
	SMTPPort                                           int
	EmailVerificationRequired                          bool
	USDCNY, SMSManCNYRate, Markup                      float64
	PayProvider, YSMAppID, YSMSecret, YSMURL           string
	EPayPID, EPayKey, EPayURL                          string
	EPayPlatformPublicKey, EPayMerchantPrivateKey      string
	AllowLiveSMSInSandbox                              bool
	AutoReplaceAfter, AutoReplaceScan, PaymentOrderTTL time.Duration
	AutoReplaceMax                                     int
}

func (c Config) Validate() error {
	if c.EmailVerificationRequired {
		hasSMTP := c.SMTPHost != "" && c.SMTPPort > 0 && c.SMTPFrom != ""
		hasResend := c.ResendAPIKey != "" && c.ResendFrom != ""
		if !hasSMTP && !hasResend {
			return fmt.Errorf("configure SMTP_* or RESEND_API_KEY/RESEND_FROM before enabling email verification")
		}
		if c.TurnstileSiteKey == "" || c.TurnstileSecret == "" {
			return fmt.Errorf("TURNSTILE_SITE_KEY and TURNSTILE_SECRET are required when email verification is enabled")
		}
	}
	if c.HeroKey == "" && c.SMSManToken == "" {
		return fmt.Errorf("at least one SMS provider is required")
	}
	if c.SMSManToken != "" {
		u, err := url.Parse(c.SMSManURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("SMSMAN_BASE_URL must be HTTPS")
		}
		if c.SMSManCNYRate <= 0 {
			return fmt.Errorf("SMSMAN_PRICE_CNY_RATE must be greater than zero")
		}
	}
	if c.USDCNY <= 0 {
		return fmt.Errorf("USD_CNY_RATE must be greater than zero")
	}
	if c.Markup < 0 {
		return fmt.Errorf("PRICE_MARKUP_CNY cannot be negative")
	}
	switch c.PayProvider {
	case "sandbox":
		return nil
	case "yishoumi":
		if c.YSMAppID == "" || c.YSMSecret == "" {
			return fmt.Errorf("YSM_APP_ID and YSM_SECRET are required for yishoumi")
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("APP_BASE_URL must be a public HTTPS URL for yishoumi callbacks")
		}
		return nil
	case "epay", "50pay":
		if c.EPayPID == "" || c.EPayKey == "" {
			return fmt.Errorf("EPAY_PID and EPAY_KEY are required for 50Pay")
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("APP_BASE_URL must be a public HTTPS URL for 50Pay callbacks")
		}
		endpoint, err := url.Parse(c.EPayURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
			return fmt.Errorf("EPAY_BASE_URL must be HTTPS")
		}
		return nil
	default:
		return fmt.Errorf("unsupported PAY_PROVIDER %q", c.PayProvider)
	}
}

func Load() Config {
	loadEnv(".env")
	return Config{
		Port: env("PORT", "3000"), BaseURL: env("APP_BASE_URL", "http://localhost:3000"),
		HeroKey: os.Getenv("HEROSMS_API_KEY"), HeroURL: env("HEROSMS_BASE_URL", "https://hero-sms.com/stubs/handler_api.php"), HeroCurrency: env("HEROSMS_CURRENCY", "840"),
		SMSManToken: os.Getenv("SMSMAN_API_TOKEN"), SMSManURL: env("SMSMAN_BASE_URL", "https://api.sms-man.com/control"),
		AdminEmail: strings.ToLower(strings.TrimSpace(env("ADMIN_EMAIL", "admin@local"))), AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		SMTPHost: os.Getenv("SMTP_HOST"), SMTPPort: envInt("SMTP_PORT", 587), SMTPUser: os.Getenv("SMTP_USER"), SMTPPassword: os.Getenv("SMTP_PASSWORD"), SMTPFrom: os.Getenv("SMTP_FROM"),
		ResendAPIKey: os.Getenv("RESEND_API_KEY"), ResendFrom: os.Getenv("RESEND_FROM"),
		TurnstileSiteKey: os.Getenv("TURNSTILE_SITE_KEY"), TurnstileSecret: os.Getenv("TURNSTILE_SECRET"), EmailVerificationRequired: env("EMAIL_VERIFICATION_REQUIRED", "false") == "true",
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		USDCNY: envFloat("USD_CNY_RATE", 7.2), SMSManCNYRate: envFloat("SMSMAN_PRICE_CNY_RATE", 0.08), Markup: envFloat("PRICE_MARKUP_CNY", 1),
		PayProvider: env("PAY_PROVIDER", "sandbox"), YSMAppID: os.Getenv("YSM_APP_ID"), YSMSecret: os.Getenv("YSM_SECRET"), YSMURL: env("YSM_BASE_URL", "https://www.yishoumi.cn"),
		EPayPID: os.Getenv("EPAY_PID"), EPayKey: os.Getenv("EPAY_KEY"), EPayURL: env("EPAY_BASE_URL", "https://50pay.xiajuan88.com"),
		EPayPlatformPublicKey: os.Getenv("EPAY_PLATFORM_PUBLIC_KEY"), EPayMerchantPrivateKey: os.Getenv("EPAY_MERCHANT_PRIVATE_KEY"),
		AllowLiveSMSInSandbox: env("ALLOW_LIVE_SMS_IN_SANDBOX", "false") == "true",
		AutoReplaceAfter:      time.Duration(envInt("SMS_AUTO_REPLACE_AFTER_SECONDS", 180)) * time.Second,
		AutoReplaceMax:        envInt("SMS_AUTO_REPLACE_MAX_ATTEMPTS", 20),
		AutoReplaceScan:       time.Duration(envInt("SMS_AUTO_REPLACE_SCAN_SECONDS", 10)) * time.Second,
		PaymentOrderTTL:       time.Duration(envInt("PAYMENT_ORDER_TTL_MINUTES", 20)) * time.Minute,
	}
}

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.SplitN(line, "=", 2)
		if len(p) == 2 {
			if _, ok := os.LookupEnv(p[0]); !ok {
				_ = os.Setenv(p[0], p[1])
			}
		}
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envFloat(k string, d float64) float64 {
	v, err := strconv.ParseFloat(env(k, ""), 64)
	if err != nil {
		return d
	}
	return v
}
func envInt(k string, d int) int {
	v, err := strconv.Atoi(env(k, ""))
	if err != nil {
		return d
	}
	return v
}
