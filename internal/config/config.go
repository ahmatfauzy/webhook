package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv               string
	HTTPAddr             string
	DatabaseURL          string
	RedisURL             string
	WorkerConcurrency    int
	WebhookTimeout       time.Duration
	MaxRetryAttempts     int
	RetryMaxDelay        time.Duration
	RetryJitter          float64
	RetrySchedule        []time.Duration
	MaxEventPayloadSize  int64
	EncryptionKey        []byte
	AdminToken           string
	CORSAllowedOrigins   []string
	RateLimitRPS         int
	EventRetentionDays   int
	AttemptRetentionDays int
	PayloadRetentionDays int
	AllowPrivateIPs      bool
	AllowlistCIDRs       []string
}

func Load() (*Config, error) {
	c := &Config{
		AppEnv:               envOr("APP_ENV", "development"),
		HTTPAddr:             envOr("HTTP_ADDR", ":8080"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		RedisURL:             envOr("REDIS_URL", "redis://localhost:6379/0"),
		WorkerConcurrency:    envInt("WORKER_CONCURRENCY", 20),
		WebhookTimeout:       envDuration("WEBHOOK_TIMEOUT", 10*time.Second),
		MaxRetryAttempts:     envInt("MAX_RETRY_ATTEMPTS", 5),
		RetryMaxDelay:        envDuration("RETRY_MAX_DELAY", 6*time.Hour),
		RetryJitter:          envFloat("RETRY_JITTER", 0.2),
		MaxEventPayloadSize:  int64(envInt("MAX_EVENT_PAYLOAD_SIZE", 1*1024*1024)),
		AdminToken:           os.Getenv("ADMIN_TOKEN"),
		RateLimitRPS:         envInt("RATE_LIMIT_RPS", 100),
		EventRetentionDays:   envInt("EVENT_RETENTION_DAYS", 30),
		AttemptRetentionDays: envInt("ATTEMPT_RETENTION_DAYS", 30),
		PayloadRetentionDays: envInt("PAYLOAD_RETENTION_DAYS", 7),
		AllowPrivateIPs:      envBool("WEBHOOK_ALLOW_PRIVATE_IPS", false),
	}

	// DATABASE_URL required in production
	if c.AppEnv == "production" && c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL required in production")
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = "postgres://webhooker:webhooker@localhost:5432/webhooker?sslmode=disable"
	}

	// RETRY_SCHEDULE
	schedStr := envOr("RETRY_SCHEDULE", "30s,2m,10m,30m")
	sched, err := parseSchedule(schedStr)
	if err != nil {
		return nil, fmt.Errorf("invalid RETRY_SCHEDULE: %w", err)
	}
	c.RetrySchedule = sched

	// ENCRYPTION_KEY — 32-byte base64
	encKeyStr := os.Getenv("ENCRYPTION_KEY")
	if encKeyStr != "" {
		key, err := base64.StdEncoding.DecodeString(encKeyStr)
		if err != nil {
			// try raw string if not base64
			key = []byte(encKeyStr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("ENCRYPTION_KEY must be 32 bytes (got %d), provide base64 of 32 bytes", len(key))
		}
		c.EncryptionKey = key
	} else if c.AppEnv == "production" {
		return nil, fmt.Errorf("ENCRYPTION_KEY required in production")
	} else {
		// dev default — fixed 32 bytes, NOT for production
		c.EncryptionKey = []byte("0123456789abcdef0123456789abcdef")
	}

	// CORS
	corsStr := os.Getenv("CORS_ALLOWED_ORIGINS")
	if corsStr != "" {
		parts := strings.Split(corsStr, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		c.CORSAllowedOrigins = parts
	} else {
		if c.AppEnv == "development" {
			c.CORSAllowedOrigins = []string{"http://localhost:5173", "http://localhost:3000"}
		}
	}

	// allowlist CIDRs
	if s := os.Getenv("WEBHOOK_ALLOWLIST_CIDRS"); s != "" {
		c.AllowlistCIDRs = strings.Split(s, ",")
		for i := range c.AllowlistCIDRs {
			c.AllowlistCIDRs[i] = strings.TrimSpace(c.AllowlistCIDRs[i])
		}
	}

	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func parseSchedule(s string) ([]time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]time.Duration, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		d, err := time.ParseDuration(p)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
