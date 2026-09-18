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
