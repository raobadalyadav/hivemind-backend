// cmd/api boots the gRPC API server: loads config, connects Postgres/Redis/
// NATS, wires the shared interceptor chain, registers every domain service,
// and shuts down gracefully on SIGINT/SIGTERM — the pattern from the Go
// backend skill's "Production Patterns > Graceful Shutdown".
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/hivemind/backend/config"
	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/internal/admin"
	"github.com/hivemind/backend/internal/auth"
	"github.com/hivemind/backend/internal/bookings"
	"github.com/hivemind/backend/internal/chat"
	"github.com/hivemind/backend/internal/communities"
	"github.com/hivemind/backend/internal/connections"
	"github.com/hivemind/backend/internal/discovery"
	"github.com/hivemind/backend/internal/moderation"
	"github.com/hivemind/backend/internal/notifications"
	"github.com/hivemind/backend/internal/payments"
	"github.com/hivemind/backend/internal/plans"
	"github.com/hivemind/backend/internal/profiles"
	"github.com/hivemind/backend/internal/recommendation"
	"github.com/hivemind/backend/internal/search"
	"github.com/hivemind/backend/internal/social"
	"github.com/hivemind/backend/internal/subscriptions"
	"github.com/hivemind/backend/internal/users"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
	"github.com/hivemind/backend/pkg/idempotency"
	"github.com/hivemind/backend/pkg/oauth"
	"github.com/hivemind/backend/pkg/observability"
	"github.com/hivemind/backend/pkg/security"
)

func main() {
	logger := observability.NewLogger()
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	// NATS is only used by cmd/worker (outbox draining + event consumers);
	// the API server writes outbox rows via the same DB transaction as its
	// state changes and never touches NATS directly.

	issuer := security.NewTokenIssuer(cfg.JWTSecret, cfg.JWTTTL)
	guard := idempotency.NewGuard(rdb)

	interceptors := grpc.ChainUnaryInterceptor(
		grpcmiddleware.RecoveryUnaryInterceptor(logger),
		grpcmiddleware.LoggingUnaryInterceptor(logger),
		grpcmiddleware.AuthUnaryInterceptor(issuer),
	)
	srv := grpc.NewServer(interceptors)

	// Construction order matters: bookingsSvc and moderationSvc are built
	// first so they can be injected into plans/chat via the
	// BookingCreator/BookingCanceller/ReportSubmitter interfaces those
	// packages declare (see internal/plans/service.go, internal/chat/service.go).
	bookingsSvc := bookings.NewService(bookings.NewRepository(pool), guard)
	moderationSvc := moderation.NewService(moderation.NewRepository(pool))

	var googleVerifier, appleVerifier *oauth.Verifier
	if cfg.GoogleClientID != "" {
		v, err := oauth.NewGoogleVerifier(ctx, cfg.GoogleClientID)
		if err != nil {
			logger.Error("google oauth verifier setup failed, Google sign-in disabled", "error", err)
		} else {
			googleVerifier = v
		}
	}
	if cfg.AppleBundleID != "" {
		v, err := oauth.NewAppleVerifier(ctx, cfg.AppleBundleID)
		if err != nil {
			logger.Error("apple oauth verifier setup failed, Apple sign-in disabled", "error", err)
		} else {
			appleVerifier = v
		}
	}

	socialv1.RegisterAuthServiceServer(srv, auth.NewHandler(auth.NewService(auth.NewRepository(pool), issuer, googleVerifier, appleVerifier)))
	socialv1.RegisterUserServiceServer(srv, users.NewHandler(users.NewService(users.NewRepository(pool))))
	socialv1.RegisterProfileServiceServer(srv, profiles.NewHandler(profiles.NewService(profiles.NewRepository(pool))))
	socialv1.RegisterDiscoveryServiceServer(srv, discovery.NewHandler(discovery.NewService(discovery.NewRepository(pool))))
	socialv1.RegisterPlanServiceServer(srv, plans.NewHandler(plans.NewService(plans.NewRepository(pool), bookingsSvc, bookingsSvc)))
	socialv1.RegisterBookingServiceServer(srv, bookings.NewHandler(bookingsSvc))
	socialv1.RegisterPaymentServiceServer(srv, payments.NewHandler(payments.NewService(payments.NewRepository(pool))))
	socialv1.RegisterSubscriptionServiceServer(srv, subscriptions.NewHandler(subscriptions.NewService(subscriptions.NewRepository(pool))))
	socialv1.RegisterCommunityServiceServer(srv, communities.NewHandler(communities.NewService(communities.NewRepository(pool))))
	socialv1.RegisterConnectionServiceServer(srv, connections.NewHandler(connections.NewService(connections.NewRepository(pool))))
	socialv1.RegisterChatServiceServer(srv, chat.NewHandler(chat.NewService(chat.NewRepository(pool), moderationSvc)))
	socialv1.RegisterSocialServiceServer(srv, social.NewHandler(social.NewService(social.NewRepository(pool))))
	socialv1.RegisterModerationServiceServer(srv, moderation.NewHandler(moderationSvc))
	socialv1.RegisterNotificationServiceServer(srv, notifications.NewHandler(notifications.NewService(notifications.NewRepository(pool))))
	socialv1.RegisterSearchServiceServer(srv, search.NewHandler(search.NewService(search.NewRepository(pool))))
	socialv1.RegisterRecommendationServiceServer(srv, recommendation.NewHandler(recommendation.NewService(recommendation.NewRepository(pool))))
	socialv1.RegisterAdminServiceServer(srv, admin.NewHandler(admin.NewService(admin.NewRepository(pool))))

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	// Reflection makes local debugging with grpcurl/grpcui possible without
	// shipping .proto files alongside the binary. Harmless to leave on in
	// this scaffold; revisit before a production deploy if that's a concern.
	reflection.Register(srv)

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logger.Error("listen", "error", err)
		os.Exit(1)
	}

	go func() {
		logger.Info("grpc server starting", "port", cfg.GRPCPort)
		if err := srv.Serve(lis); err != nil {
			logger.Error("grpc serve", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		srv.Stop()
	}
	logger.Info("server exited")
}
