// Package config loads backend configuration from environment variables.
// A dozen settings don't justify a config library; plain os.Getenv with
// defaults keeps this readable and dependency-free.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Production reports whether APP_ENV says this is a production deployment.
// Anything else (unset, dev, staging…) is treated as non-production.
func (c Config) Production() bool { return c.AppEnv == "production" || c.AppEnv == "prod" }

const (
	devJWTSecret   = "dev-secret-change-in-production"
	devMinIOSecret = "hivemind123"
)

// loadErrors collects env values that were set but unparsable, so Validate can
// refuse to start instead of silently using the default.
var loadErrors []string

type Config struct {
	AppEnv string // APP_ENV: production | staging | dev (default dev)

	// TLSCertFile/TLSKeyFile: serve gRPC over TLS directly. Leave empty when a load balancer terminates TLS.
	TLSCertFile string
	TLSKeyFile  string
	// RateLimitPerMinute caps each caller's gRPC calls per minute (0 disables); AuthRateLimitPerMinute is the
	// stricter cap for sign-in / refresh / recovery, keyed by client IP.
	RateLimitPerMinute     int
	AuthRateLimitPerMinute int
	// TrustProxyHeaders: read the client IP from X-Forwarded-For (only behind a proxy that sets it).
	TrustProxyHeaders bool
	// PassSecretSet: PASS_SECRET was provided explicitly (not derived from JWT_SECRET).
	PassSecretSet bool

	GRPCPort       string
	DatabaseURL    string
	RedisAddr      string
	NATSURL        string
	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
	MinIOUseSSL    bool
	JWTSecret      string
	JWTTTL         time.Duration
	// GoogleClientID/AppleBundleID: empty disables that sign-in provider
	// (SignInWithGoogle/SignInWithApple return Unimplemented) rather than
	// failing server startup — matches how the payments gateway is stubbed:
	// optional external config, not a hard dependency for local dev.
	GoogleClientID string
	AppleBundleID  string
	// ResendAPIKey/FirebaseCredentialsPath: same optional-external-config
	// pattern — empty disables that delivery channel (email/push skipped
	// and logged, not an error) instead of failing server startup.
	ResendAPIKey            string
	EmailFromAddress        string
	FirebaseCredentialsPath string
	// CashfreeClientID/Secret: same optional-external-config pattern —
	// empty disables real payment-session creation (order still recorded,
	// no Cashfree call). WebhookPort is a separate plain net/http listener
	// (Cashfree webhooks are HTTP POSTs, not gRPC).
	CashfreeClientID     string
	CashfreeClientSecret string
	CashfreeSandbox      bool
	WebhookPort          string
	PassSecret           string
	MediaPublicBaseURL   string // where phones reach GET /media/... (this API's HTTP port)
	PublicWebBaseURL     string // https origin used in shareable links (posts, plans); the app opens them via app links
	MediaMaxImageMB      int
	MediaMaxVideoMB      int
	EmergencyNumber      string // shown in the Safety Center and SOS response
}

func Load() Config {
	loadErrors = nil
	return Config{
		AppEnv:                 strings.ToLower(getEnv("APP_ENV", "dev")),
		TLSCertFile:            getEnv("TLS_CERT_FILE", ""),
		TLSKeyFile:             getEnv("TLS_KEY_FILE", ""),
		RateLimitPerMinute:     getEnvIntAllowZero("RATE_LIMIT_PER_MINUTE", 600),
		AuthRateLimitPerMinute: getEnvIntAllowZero("AUTH_RATE_LIMIT_PER_MINUTE", 20),
		TrustProxyHeaders:      getEnvBool("TRUST_PROXY_HEADERS", false),
		PassSecretSet:          os.Getenv("PASS_SECRET") != "",
		GRPCPort:               getEnv("GRPC_PORT", "50051"),
		DatabaseURL:            getEnv("DATABASE_URL", "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"),
		RedisAddr:              getEnv("REDIS_ADDR", "localhost:6379"),
		NATSURL:                getEnv("NATS_URL", "nats://localhost:4222"),
		MinIOEndpoint:          getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinIOAccessKey:         getEnv("MINIO_ACCESS_KEY", "hivemind"),
		MinIOSecretKey:         getEnv("MINIO_SECRET_KEY", "hivemind123"),
		MinIOBucket:            getEnv("MINIO_BUCKET", "hivemind-media"),
		MinIOUseSSL:            getEnvBool("MINIO_USE_SSL", false),
		JWTSecret:              getEnv("JWT_SECRET", devJWTSecret),
		JWTTTL:                 getEnvDuration("JWT_TTL", time.Hour),
		GoogleClientID:         getEnv("GOOGLE_CLIENT_ID", ""),
		AppleBundleID:          getEnv("APPLE_BUNDLE_ID", ""),

		ResendAPIKey:            getEnv("RESEND_API_KEY", ""),
		EmailFromAddress:        getEnv("EMAIL_FROM_ADDRESS", "noreply@hivemind.app"),
		FirebaseCredentialsPath: getEnv("FIREBASE_CREDENTIALS_PATH", ""),

		CashfreeClientID:     getEnv("CASHFREE_CLIENT_ID", ""),
		CashfreeClientSecret: getEnv("CASHFREE_CLIENT_SECRET", ""),
		CashfreeSandbox:      getEnvBool("CASHFREE_SANDBOX", true),
		WebhookPort:          getEnv("WEBHOOK_PORT", "8080"),
		// Domain-separated from JWT_SECRET so a leaked pass can never be
		// confused with (or used to forge) a session token.
		MediaPublicBaseURL: getEnv("MEDIA_PUBLIC_BASE_URL", "http://localhost:"+getEnv("WEBHOOK_PORT", "8080")),
		PublicWebBaseURL:   strings.TrimRight(getEnv("PUBLIC_WEB_BASE_URL", "https://hivemind.app"), "/"),
		MediaMaxImageMB:    getEnvInt("MEDIA_MAX_IMAGE_MB", 10),
		MediaMaxVideoMB:    getEnvInt("MEDIA_MAX_VIDEO_MB", 60),
		EmergencyNumber:    getEnv("EMERGENCY_NUMBER", "112"),
		PassSecret:         getEnv("PASS_SECRET", getEnv("JWT_SECRET", devJWTSecret)+":pass"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		loadErrors = append(loadErrors, fmt.Sprintf("%s=%q is not a boolean", key, v))
		return fallback
	}
	return b
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		loadErrors = append(loadErrors, fmt.Sprintf("%s=%q is not a duration", key, v))
		return fallback
	}
	return d
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		loadErrors = append(loadErrors, fmt.Sprintf("%s=%q is not a positive integer", key, v))
	}
	return fallback
}

// getEnvIntAllowZero is getEnvInt where 0 is meaningful (0 = disabled).
func getEnvIntAllowZero(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
		loadErrors = append(loadErrors, fmt.Sprintf("%s=%q is not a non-negative integer", key, v))
	}
	return fallback
}

// Validate refuses configurations that must never run: unparsable values anywhere, and — in
// production — any secret or endpoint still at its development default. Call it right after Load.
func (c Config) Validate() error {
	var problems []string
	problems = append(problems, loadErrors...)
	if c.Production() {
		if c.JWTSecret == devJWTSecret || len(c.JWTSecret) < 32 {
			problems = append(problems, "JWT_SECRET must be set to a random value of at least 32 characters")
		}
		if !c.PassSecretSet || c.PassSecret == c.JWTSecret+":pass" || len(c.PassSecret) < 32 {
			problems = append(problems, "PASS_SECRET must be set explicitly (32+ characters, different from JWT_SECRET)")
		}
		if strings.Contains(c.DatabaseURL, "hivemind:hivemind@") || strings.Contains(c.DatabaseURL, "sslmode=disable") {
			problems = append(problems, "DATABASE_URL still uses the development credentials or sslmode=disable")
		}
		if c.MinIOSecretKey == devMinIOSecret {
			problems = append(problems, "MINIO_SECRET_KEY is still the development default")
		}
		if !strings.HasPrefix(c.PublicWebBaseURL, "https://") {
			problems = append(problems, "PUBLIC_WEB_BASE_URL must be https")
		}
		if !strings.HasPrefix(c.MediaPublicBaseURL, "https://") {
			problems = append(problems, "MEDIA_PUBLIC_BASE_URL must be https")
		}
		if c.CashfreeSandbox {
			problems = append(problems, "CASHFREE_SANDBOX must be false in production")
		}
		if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
			problems = append(problems, "TLS_CERT_FILE and TLS_KEY_FILE must be set together")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New("invalid configuration:\n  - " + strings.Join(problems, "\n  - "))
}
