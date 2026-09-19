package media

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/config"
)

// Connect builds the media service from environment config (MinIO/S3).
func Connect(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) (*Service, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store, err := NewS3Store(cctx, cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOBucket, cfg.MinIOUseSSL)
	if err != nil {
		return nil, err
	}
	return NewService(pool, store, Config{
		PublicBaseURL: cfg.MediaPublicBaseURL,
		MaxImageBytes: int64(cfg.MediaMaxImageMB) << 20,
		MaxVideoBytes: int64(cfg.MediaMaxVideoMB) << 20,
	}), nil
}
