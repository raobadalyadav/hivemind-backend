// Package config loads backend configuration from environment variables.
// A dozen settings don't justify a config library; plain os.Getenv with
// defaults keeps this readable and dependency-free.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
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
}

func Load() Config {
	return Config{
		GRPCPort:       getEnv("GRPC_PORT", "50051"),
		DatabaseURL:    getEnv("DATABASE_URL", "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"),
		RedisAddr:      getEnv("REDIS_ADDR", "localhost:6379"),
		NATSURL:        getEnv("NATS_URL", "nats://localhost:4222"),
		MinIOEndpoint:  getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinIOAccessKey: getEnv("MINIO_ACCESS_KEY", "hivemind"),
		MinIOSecretKey: getEnv("MINIO_SECRET_KEY", "hivemind123"),
		MinIOBucket:    getEnv("MINIO_BUCKET", "hivemind-media"),
		MinIOUseSSL:    getEnvBool("MINIO_USE_SSL", false),
		JWTSecret:      getEnv("JWT_SECRET", "dev-secret-change-in-production"),
		JWTTTL:         getEnvDuration("JWT_TTL", time.Hour),
		GoogleClientID: getEnv("GOOGLE_CLIENT_ID", ""),
		AppleBundleID:  getEnv("APPLE_BUNDLE_ID", ""),

		ResendAPIKey:            getEnv("RESEND_API_KEY", ""),
		EmailFromAddress:        getEnv("EMAIL_FROM_ADDRESS", "noreply@hivemind.app"),
		FirebaseCredentialsPath: getEnv("FIREBASE_CREDENTIALS_PATH", ""),

		CashfreeClientID:     getEnv("CASHFREE_CLIENT_ID", ""),
		CashfreeClientSecret: getEnv("CASHFREE_CLIENT_SECRET", ""),
		CashfreeSandbox:      getEnvBool("CASHFREE_SANDBOX", true),
		WebhookPort:          getEnv("WEBHOOK_PORT", "8080"),
		// Domain-separated from JWT_SECRET so a leaked pass can never be
		// confused with (or used to forge) a session token.
		PassSecret: getEnv("PASS_SECRET", getEnv("JWT_SECRET", "dev-secret-change-in-production")+":pass"),
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
		return fallback
	}
	return d
}
