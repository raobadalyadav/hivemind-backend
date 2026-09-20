// cmd/api boots the gRPC API server: loads config, connects Postgres/Redis/
// NATS, wires the shared interceptor chain, registers every domain service,
// and shuts down gracefully on SIGINT/SIGTERM — the pattern from the Go
// backend skill's "Production Patterns > Graceful Shutdown".
package main

import (
	"context"
	"net"
	"net/http"
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
	"github.com/hivemind/backend/internal/availability"
	"github.com/hivemind/backend/internal/bookings"
	"github.com/hivemind/backend/internal/chat"
	"github.com/hivemind/backend/internal/communities"
	"github.com/hivemind/backend/internal/company"
	"github.com/hivemind/backend/internal/connections"
	"github.com/hivemind/backend/internal/discovery"
	"github.com/hivemind/backend/internal/externalevents"
	"github.com/hivemind/backend/internal/host"
	"github.com/hivemind/backend/internal/media"
	"github.com/hivemind/backend/internal/meet"
	"github.com/hivemind/backend/internal/moderation"
	"github.com/hivemind/backend/internal/notifications"
	"github.com/hivemind/backend/internal/payments"
	"github.com/hivemind/backend/internal/plans"
	"github.com/hivemind/backend/internal/profiles"
	"github.com/hivemind/backend/internal/promotions"
	"github.com/hivemind/backend/internal/recommendation"
	"github.com/hivemind/backend/internal/referral"
	"github.com/hivemind/backend/internal/reviews"
	"github.com/hivemind/backend/internal/safety"
	"github.com/hivemind/backend/internal/search"
	"github.com/hivemind/backend/internal/smartgroups"
	"github.com/hivemind/backend/internal/social"
	"github.com/hivemind/backend/internal/stories"
	"github.com/hivemind/backend/internal/subscriptions"
	"github.com/hivemind/backend/internal/users"
	"github.com/hivemind/backend/internal/venues"
	"github.com/hivemind/backend/internal/verification"
	"github.com/hivemind/backend/pkg/analytics"
	"github.com/hivemind/backend/pkg/cashfree"
	"github.com/hivemind/backend/pkg/email"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
	"github.com/hivemind/backend/pkg/idempotency"
	pkgmedia "github.com/hivemind/backend/pkg/media"
	"github.com/hivemind/backend/pkg/oauth"
	"github.com/hivemind/backend/pkg/observability"
	"github.com/hivemind/backend/pkg/push"
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

	// Object storage for uploads. If it's unreachable the API still starts;
	// anything that needs media answers Unimplemented ("not configured")
	// instead of the whole server refusing to boot. Declared as the interface
	// type so a failed connection is a true nil, not a typed-nil pointer.
	var mediaResolver pkgmedia.Resolver
	mediaSvc, err := media.Connect(ctx, cfg, pool)
	if err != nil {
		logger.Error("media storage unavailable — uploads disabled", "error", err)
	} else {
		mediaResolver = mediaSvc
	}

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
	bookingsSvc := bookings.NewService(bookings.NewRepository(pool), guard).WithPassSecret([]byte(cfg.PassSecret))
	moderationSvc := moderation.NewService(moderation.NewRepository(pool))
	contentScreener := moderation.NewScreener()

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

	// Declared as the interface type, not *email.Client/*push.Client — a nil
	// *email.Client assigned to an interface parameter would produce a
	// non-nil interface wrapping a nil pointer, breaking the `== nil`
	// graceful-degradation checks inside auth/notifications services.
	var emailSender notifications.EmailSender
	if cfg.ResendAPIKey != "" {
		emailSender = email.NewClient(cfg.ResendAPIKey, cfg.EmailFromAddress)
	}
	var pushSender notifications.PushSender
	if cfg.FirebaseCredentialsPath != "" {
		p, err := push.NewClient(ctx, cfg.FirebaseCredentialsPath)
		if err != nil {
			logger.Error("firebase push client setup failed, push notifications disabled", "error", err)
		} else {
			pushSender = p
		}
	}

	// auth.EmailSender and notifications.EmailSender are structurally
	// identical (both just wrap pkg/email.Client's Send method) but
	// declared separately per package per the plan's decoupling — emailSender
	// satisfies both without a cast.
	analyticsRec := analytics.NewRecorder(pool)

	var cashfreeClient *cashfree.Client
	if cfg.CashfreeClientID != "" {
		cashfreeClient = cashfree.NewClient(cfg.CashfreeClientID, cfg.CashfreeClientSecret, cfg.CashfreeSandbox)
	}
	// Declared as the interface type for the same nil-interface reason as
	// emailSender/pushSender above.
	var paymentGateway payments.GatewayClient
	if cashfreeClient != nil {
		paymentGateway = cashfreeClient
	}
	paymentsSvc := payments.NewService(payments.NewRepository(pool), paymentGateway, bookingsSvc, logger)

	// plansSvc and subscriptionsSvc are built as named variables (not inline
	// in their Register call below) so they can also be injected into
	// promotions/host via the PlanHostChecker/EntitlementChecker interfaces
	// those packages declare.
	subscriptionsSvc := subscriptions.NewService(subscriptions.NewRepository(pool))
	communitiesSvc := communities.NewService(communities.NewRepository(pool)).WithEntitlements(subscriptionsSvc)
	plansSvc := plans.NewService(plans.NewRepository(pool), bookingsSvc, bookingsSvc, plans.NewTemplateDraftGenerator()).WithCommunities(communitiesSvc).WithMedia(mediaResolver)
	bookingsSvc.WithEntitlements(subscriptionsSvc)

	// Declared as the interface type for the same nil-interface reason as
	// paymentGateway above.
	var promotionGateway promotions.GatewayClient
	if cashfreeClient != nil {
		promotionGateway = cashfreeClient
	}
	promotionsSvc := promotions.NewService(promotions.NewRepository(pool), plansSvc, promotionGateway, logger)
	hostSvc := host.NewService(host.NewRepository(pool), subscriptionsSvc)
	venuesSvc := venues.NewService(venues.NewRepository(pool), subscriptionsSvc)
	reviewsSvc := reviews.NewService(reviews.NewRepository(pool), bookingsSvc)

	socialv1.RegisterAuthServiceServer(srv, auth.NewHandler(auth.NewService(auth.NewRepository(pool), issuer, googleVerifier, appleVerifier, emailSender, analyticsRec, logger)))
	socialv1.RegisterUserServiceServer(srv, users.NewHandler(users.NewService(users.NewRepository(pool))))
	socialv1.RegisterProfileServiceServer(srv, profiles.NewHandler(profiles.NewService(profiles.NewRepository(pool)).WithMedia(mediaResolver)))
	socialv1.RegisterDiscoveryServiceServer(srv, discovery.NewHandler(discovery.NewService(discovery.NewRepository(pool))))
	socialv1.RegisterPlanServiceServer(srv, plans.NewHandler(plansSvc))
	socialv1.RegisterBookingServiceServer(srv, bookings.NewHandler(bookingsSvc))
	socialv1.RegisterPaymentServiceServer(srv, payments.NewHandler(paymentsSvc))
	socialv1.RegisterSubscriptionServiceServer(srv, subscriptions.NewHandler(subscriptionsSvc))
	socialv1.RegisterCommunityServiceServer(srv, communities.NewHandler(communitiesSvc))
	chatSvc := chat.NewService(chat.NewRepository(pool), moderationSvc, contentScreener, chat.NewTemplateIcebreaker()).WithMedia(mediaResolver)
	socialv1.RegisterConnectionServiceServer(srv, connections.NewHandler(connections.NewService(connections.NewRepository(pool)).WithMeetAgain(guard, chatSvc)))
	availabilitySvc := availability.NewService(availability.NewRepository(pool), chatSvc)
	socialv1.RegisterChatServiceServer(srv, chat.NewHandler(chatSvc))
	socialv1.RegisterAvailabilityServiceServer(srv, availability.NewHandler(availabilitySvc))
	socialv1.RegisterReferralServiceServer(srv, referral.NewHandler(referral.NewService(referral.NewRepository(pool))))
	socialv1.RegisterSafetyServiceServer(srv, safety.NewHandler(safety.NewService(safety.NewRepository(pool), emailSender, cfg.EmergencyNumber)))
	socialv1.RegisterExternalEventServiceServer(srv, externalevents.NewHandler(externalevents.NewService(externalevents.NewRepository(pool), chatSvc)))
	socialv1.RegisterMeetServiceServer(srv, meet.NewHandler(meet.NewService(meet.NewRepository(pool)).WithDM(chatSvc)))
	socialv1.RegisterVerificationServiceServer(srv, verification.NewHandler(verification.NewService(pool).WithMedia(mediaResolver)))
	socialv1.RegisterStoryServiceServer(srv, stories.NewHandler(stories.NewService(stories.NewRepository(pool), contentScreener).WithMedia(mediaResolver)))
	socialv1.RegisterSocialServiceServer(srv, social.NewHandler(social.NewService(social.NewRepository(pool), moderationSvc, contentScreener).WithMedia(mediaResolver).WithWebBaseURL(cfg.PublicWebBaseURL)))
	socialv1.RegisterModerationServiceServer(srv, moderation.NewHandler(moderationSvc))
	socialv1.RegisterNotificationServiceServer(srv, notifications.NewHandler(notifications.NewService(notifications.NewRepository(pool), emailSender, pushSender, logger)))
	socialv1.RegisterSearchServiceServer(srv, search.NewHandler(search.NewService(search.NewRepository(pool))))
	socialv1.RegisterRecommendationServiceServer(srv, recommendation.NewHandler(recommendation.NewService(recommendation.NewRepository(pool))))
	socialv1.RegisterAdminServiceServer(srv, admin.NewHandler(admin.NewService(admin.NewRepository(pool)).WithMediaBaseURL(cfg.MediaPublicBaseURL)))
	socialv1.RegisterVenueServiceServer(srv, venues.NewHandler(venuesSvc))
	socialv1.RegisterHostServiceServer(srv, host.NewHandler(hostSvc))
	socialv1.RegisterPromotionServiceServer(srv, promotions.NewHandler(promotionsSvc))
	socialv1.RegisterSmartGroupServiceServer(srv, smartgroups.NewHandler(smartgroups.NewService(smartgroups.NewRepository(pool), plansSvc, chatSvc)))
	socialv1.RegisterCompanyServiceServer(srv, company.NewHandler(company.NewService(company.NewRepository(pool), bookingsSvc)))
	socialv1.RegisterReviewServiceServer(srv, reviews.NewHandler(reviewsSvc))

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

	// Cashfree webhooks are plain HTTP POSTs from Cashfree's servers, not
	// gRPC — a second, separate listener, same process.
	webhookMux := http.NewServeMux()
	if mediaSvc != nil {
		media.NewHandler(mediaSvc, issuer, logger).Register(webhookMux)
	}
	webhookMux.HandleFunc("/webhooks/cashfree", cashfreeWebhookHandler(paymentsSvc, promotionsSvc, cashfreeClient, logger))
	webhookSrv := &http.Server{Addr: ":" + cfg.WebhookPort, Handler: webhookMux}
	go func() {
		logger.Info("webhook server starting", "port", cfg.WebhookPort)
		if err := webhookSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("webhook serve", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := webhookSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("webhook server shutdown", "error", err)
	}

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
