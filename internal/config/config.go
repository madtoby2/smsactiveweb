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
	Port, BaseURL, HeroKey, HeroURL, HeroCurrency string
	USDCNY, Markup                                float64
	PayProvider, YSMAppID, YSMSecret, YSMURL      string
	AllowLiveSMSInSandbox                         bool
	AutoReplaceAfter, AutoReplaceScan             time.Duration
	AutoReplaceMax                                int
}

func (c Config) Validate() error {
	if c.HeroKey == "" {
		return fmt.Errorf("HEROSMS_API_KEY is required")
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
	default:
		return fmt.Errorf("unsupported PAY_PROVIDER %q", c.PayProvider)
	}
}

func Load() Config {
	loadEnv(".env")
	return Config{
		Port: env("PORT", "3000"), BaseURL: env("APP_BASE_URL", "http://localhost:3000"),
		HeroKey: os.Getenv("HEROSMS_API_KEY"), HeroURL: env("HEROSMS_BASE_URL", "https://hero-sms.com/stubs/handler_api.php"), HeroCurrency: env("HEROSMS_CURRENCY", "840"),
		USDCNY: envFloat("USD_CNY_RATE", 7.2), Markup: envFloat("PRICE_MARKUP_CNY", 1),
		PayProvider: env("PAY_PROVIDER", "sandbox"), YSMAppID: os.Getenv("YSM_APP_ID"), YSMSecret: os.Getenv("YSM_SECRET"), YSMURL: env("YSM_BASE_URL", "https://www.yishoumi.cn"),
		AllowLiveSMSInSandbox: env("ALLOW_LIVE_SMS_IN_SANDBOX", "false") == "true",
		AutoReplaceAfter:      time.Duration(envInt("SMS_AUTO_REPLACE_AFTER_SECONDS", 180)) * time.Second,
		AutoReplaceMax:        envInt("SMS_AUTO_REPLACE_MAX_ATTEMPTS", 2),
		AutoReplaceScan:       time.Duration(envInt("SMS_AUTO_REPLACE_SCAN_SECONDS", 10)) * time.Second,
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
