package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port, BaseURL, HeroKey, HeroURL, HeroCurrency string
	USDCNY, Markup                                float64
	PayProvider, YSMAppID, YSMSecret, YSMURL      string
	AllowLiveSMSInSandbox                         bool
}

func Load() Config {
	loadEnv(".env")
	return Config{
		Port: env("PORT", "3000"), BaseURL: env("APP_BASE_URL", "http://localhost:3000"),
		HeroKey: os.Getenv("HEROSMS_API_KEY"), HeroURL: env("HEROSMS_BASE_URL", "https://hero-sms.com/stubs/handler_api.php"), HeroCurrency: env("HEROSMS_CURRENCY", "840"),
		USDCNY: envFloat("USD_CNY_RATE", 7.2), Markup: envFloat("PRICE_MARKUP_CNY", 1),
		PayProvider: env("PAY_PROVIDER", "sandbox"), YSMAppID: os.Getenv("YSM_APP_ID"), YSMSecret: os.Getenv("YSM_SECRET"), YSMURL: env("YSM_BASE_URL", "https://www.yishoumi.cn"),
		AllowLiveSMSInSandbox: env("ALLOW_LIVE_SMS_IN_SANDBOX", "false") == "true",
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
