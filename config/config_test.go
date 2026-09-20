package config

import (
	"strings"
	"testing"
)

// cleanEnv blanks every setting Load reads, so a developer's real .env (or CI's DATABASE_URL) can't leak into a test.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "GRPC_PORT", "DATABASE_URL", "REDIS_ADDR", "NATS_URL", "MINIO_ENDPOINT", "MINIO_ACCESS_KEY", "MINIO_SECRET_KEY",
		"MINIO_BUCKET", "MINIO_USE_SSL", "JWT_SECRET", "JWT_TTL", "PASS_SECRET", "PUBLIC_WEB_BASE_URL", "MEDIA_PUBLIC_BASE_URL", "WEBHOOK_PORT",
		"CASHFREE_SANDBOX", "TLS_CERT_FILE", "TLS_KEY_FILE", "RATE_LIMIT_PER_MINUTE", "AUTH_RATE_LIMIT_PER_MINUTE", "TRUST_PROXY_HEADERS",
		"DB_MAX_CONNS", "MEDIA_MAX_IMAGE_MB", "MEDIA_MAX_VIDEO_MB"} {
		t.Setenv(k, "")
	}
}

func setProdEnv(t *testing.T) {
	t.Helper()
	cleanEnv(t)
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("PASS_SECRET", strings.Repeat("p", 40))
	t.Setenv("DATABASE_URL", "postgres://svc:s3cret@db.internal:5432/hivemind?sslmode=require")
	t.Setenv("MINIO_SECRET_KEY", strings.Repeat("m", 30))
	t.Setenv("PUBLIC_WEB_BASE_URL", "https://hivemind.app")
	t.Setenv("MEDIA_PUBLIC_BASE_URL", "https://media.hivemind.app")
	t.Setenv("CASHFREE_SANDBOX", "false")
}

func TestValidate_DevAcceptsDefaults(t *testing.T) {
	cleanEnv(t)
	if err := Load().Validate(); err != nil {
		t.Fatalf("dev defaults must run: %v", err)
	}
}

func TestValidate_ProductionRefusesDevDefaults(t *testing.T) {
	cleanEnv(t)
	t.Setenv("APP_ENV", "production") // everything else left at development defaults
	err := Load().Validate()
	if err == nil {
		t.Fatal("production with default secrets must refuse to start")
	}
	for _, want := range []string{"JWT_SECRET", "PASS_SECRET", "DATABASE_URL", "MINIO_SECRET_KEY", "MEDIA_PUBLIC_BASE_URL", "CASHFREE_SANDBOX"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestValidate_ProductionAcceptsAProperConfig(t *testing.T) {
	setProdEnv(t)
	if err := Load().Validate(); err != nil {
		t.Fatalf("a proper production config must pass: %v", err)
	}
}

func TestValidate_UnparsableValuesAreErrorsNotDefaults(t *testing.T) {
	cleanEnv(t)
	t.Setenv("JWT_TTL", "soon")
	t.Setenv("MEDIA_MAX_IMAGE_MB", "lots")
	t.Setenv("MINIO_USE_SSL", "maybe")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "JWT_TTL") || !strings.Contains(err.Error(), "MEDIA_MAX_IMAGE_MB") || !strings.Contains(err.Error(), "MINIO_USE_SSL") {
		t.Fatalf("bad env values must be reported: %v", err)
	}
}

func TestValidate_ProductionNeedsBothTLSFiles(t *testing.T) {
	setProdEnv(t)
	t.Setenv("TLS_CERT_FILE", "/etc/tls/cert.pem")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "TLS_CERT_FILE") {
		t.Fatalf("a cert without a key must be refused: %v", err)
	}
}

func TestLoad_TrimsWhitespaceAroundValues(t *testing.T) {
	cleanEnv(t)
	t.Setenv("RATE_LIMIT_PER_MINUTE", "600   ")
	t.Setenv("TRUST_PROXY_HEADERS", "false  ")
	t.Setenv("JWT_TTL", " 2h ")
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatalf("stray spaces must not break startup: %v", err)
	}
	if c.RateLimitPerMinute != 600 || c.TrustProxyHeaders || c.JWTTTL.Hours() != 2 {
		t.Fatalf("values parsed after trimming: %+v", c)
	}
}
