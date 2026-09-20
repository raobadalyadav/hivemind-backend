package config

import (
	"strings"
	"testing"
)

func setProdEnv(t *testing.T) {
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
	t.Setenv("APP_ENV", "")
	if err := Load().Validate(); err != nil {
		t.Fatalf("dev defaults must run: %v", err)
	}
}

func TestValidate_ProductionRefusesDevDefaults(t *testing.T) {
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
	t.Setenv("APP_ENV", "")
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
