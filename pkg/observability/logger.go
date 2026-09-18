// Package observability provides structured logging setup. Trace-ID
// propagation across gRPC/DB/NATS (PRD §17) is a TODO(phase4) — add
// OpenTelemetry once cross-service tracing is actually needed.
package observability

import (
	"log/slog"
	"os"
)

func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
