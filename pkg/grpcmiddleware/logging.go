package grpcmiddleware

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
)

func LoggingUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logger.Info("grpc request",
			"method", info.FullMethod,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", errString(err),
		)
		return resp, err
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
