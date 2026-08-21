package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env  string
	Addr string

	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration

	LogLevel slog.Level

	MongoURI      string
	MongoDatabase string
	MongoReceipts string
	MongoTimeout  time.Duration

	WhatsAppToken         string
	WhatsAppAPIBase       string
	WhatsAppVerifyToken   string
	WhatsAppAppSecret     string
	WhatsAppPhoneNumberID string
	WhatsAppTimeout       time.Duration
	MediaMaxBytes         int64

	RelayNumbers []string

	TesseractBin  string
	TesseractLang string
	OCRTimeout    time.Duration

	ReceiptDayFirst bool

	ReceiptCurrency string

	MongoStores string

	MongoCounters string

	StoreOverridesOCR bool
}

func Load() (Config, error) {
	if err := loadDotenv(getString("DOTENV_PATH", defaultDotenvPath)); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Env:  getString("APP_ENV", "development"),
		Addr: listenAddr(),

		MongoURI:      getString("ATLAS", ""),
		MongoDatabase: getString("MONGO_DB", "go_ocr"),
		MongoReceipts: getString("MONGO_RECEIPTS_COLLECTION", "receipts"),
		MongoStores:   getString("MONGO_STORES_COLLECTION", "stores"),
		MongoCounters: getString("MONGO_COUNTERS_COLLECTION", "counters"),

		WhatsAppToken:       getString("WHATSAPP_TOKEN", ""),
		WhatsAppAPIBase:     getString("WHATSAPP_API_BASE", "https://graph.facebook.com/v21.0"),
		WhatsAppVerifyToken: getString("WHATSAPP_VERIFY_TOKEN", ""),
		WhatsAppAppSecret:   getString("WHATSAPP_APP_SECRET", ""),

		WhatsAppPhoneNumberID: getString("WHATSAPP_PHONE_NUMBER_ID", ""),

		RelayNumbers: getStringSlice("WHATSAPP_RELAY_NUMBERS"),

		TesseractBin:  getString("TESSERACT_BIN", "tesseract"),
		TesseractLang: getString("TESSERACT_LANG", "eng"),

		ReceiptDayFirst:   getBool("RECEIPT_DAY_FIRST", true),
		StoreOverridesOCR: getBool("STORE_OVERRIDES_OCR", false),
		ReceiptCurrency:   getString("RECEIPT_DEFAULT_CURRENCY", "USD"),
	}

	var err error

	if cfg.ReadTimeout, err = getDuration("READ_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = getDuration("WRITE_TIMEOUT", 75*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = getDuration("IDLE_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}

	if cfg.RequestTimeout, err = getDuration("REQUEST_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = getDuration("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MongoTimeout, err = getDuration("MONGO_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WhatsAppTimeout, err = getDuration("WHATSAPP_TIMEOUT", 20*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.OCRTimeout, err = getDuration("OCR_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MediaMaxBytes, err = getBytes("MEDIA_MAX_BYTES", 10<<20); err != nil {
		return Config{}, err
	}
	if cfg.LogLevel, err = getLogLevel("LOG_LEVEL", slog.LevelInfo); err != nil {
		return Config{}, err
	}

	if cfg.WriteTimeout <= cfg.RequestTimeout {
		return Config{}, fmt.Errorf(
			"config: WRITE_TIMEOUT (%s) must be greater than REQUEST_TIMEOUT (%s)",
			cfg.WriteTimeout, cfg.RequestTimeout,
		)
	}

	if cfg.MongoURI != "" &&
		!strings.HasPrefix(cfg.MongoURI, "mongodb://") && !strings.HasPrefix(cfg.MongoURI, "mongodb+srv://") {
		return Config{}, errors.New("config: ATLAS must start with mongodb:// or mongodb+srv://")
	}

	if budget := cfg.WhatsAppTimeout + cfg.OCRTimeout; budget > cfg.RequestTimeout {
		return Config{}, fmt.Errorf(
			"config: WHATSAPP_TIMEOUT (%s) + OCR_TIMEOUT (%s) = %s exceeds REQUEST_TIMEOUT (%s); raise REQUEST_TIMEOUT and WRITE_TIMEOUT",
			cfg.WhatsAppTimeout, cfg.OCRTimeout, budget, cfg.RequestTimeout,
		)
	}

	return cfg, nil
}

func (c Config) MongoURISafe() string {
	u, err := url.Parse(c.MongoURI)
	if err != nil {
		return "<unparseable mongodb uri>"
	}
	return u.Redacted()
}

// listenAddr prefers APP_ADDR, then PORT, which Cloud Run injects.
func listenAddr() string {
	if addr := getString("APP_ADDR", ""); addr != "" {
		return addr
	}
	if port := getString("PORT", ""); port != "" {
		return ":" + port
	}
	return ":8080"
}

func getString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// getStringSlice reads a comma-separated list, skipping blanks.
func getStringSlice(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: invalid %s=%q: %w", key, raw, err)
	}
	return d, nil
}

func getBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "":
		return fallback
	case "yes", "y", "on":
		return true
	case "no", "n", "off":
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func getBytes(key string, fallback int64) (int64, error) {
	raw := strings.ToUpper(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback, nil
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(raw, "KB"):
		multiplier, raw = 1<<10, strings.TrimSuffix(raw, "KB")
	case strings.HasSuffix(raw, "MB"):
		multiplier, raw = 1<<20, strings.TrimSuffix(raw, "MB")
	case strings.HasSuffix(raw, "GB"):
		multiplier, raw = 1<<30, strings.TrimSuffix(raw, "GB")
	}

	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("config: invalid %s=%q: want a positive size like 10MB", key, os.Getenv(key))
	}
	return n * multiplier, nil
}

func getLogLevel(key string, fallback slog.Level) (slog.Level, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("config: invalid %s=%q: %w", key, raw, err)
	}
	return lvl, nil
}
