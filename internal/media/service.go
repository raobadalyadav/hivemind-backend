// Package media accepts photo/video uploads, stores them in object storage
// and serves them back. Callers attach an upload to a story/post/message/…
// by id (never by URL), and only the uploader may attach it.
package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgmedia "github.com/hivemind/backend/pkg/media"
)

var (
	ErrTooLarge    = errors.New("media: file is too large")
	ErrUnsupported = errors.New("media: only JPEG/PNG photos and MP4/MOV/WebM videos are supported")
	ErrRateLimited = errors.New("media: too many uploads, try again later")
)

type Config struct {
	PublicBaseURL string // e.g. http://192.168.1.40:8080 — where GET /media/... is reachable from phones
	MaxImageBytes int64
	MaxVideoBytes int64
	HourlyLimit   int
	GCAge         time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxImageBytes <= 0 {
		c.MaxImageBytes = 10 << 20
	}
	if c.MaxVideoBytes <= 0 {
		c.MaxVideoBytes = 60 << 20
	}
	if c.HourlyLimit <= 0 {
		c.HourlyLimit = 100
	}
	if c.GCAge <= 0 {
		c.GCAge = 24 * time.Hour
	}
	c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	return c
}

type Service struct {
	pool  *pgxpool.Pool
	store Store
	cfg   Config
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool, store Store, cfg Config) *Service {
	return &Service{pool: pool, store: store, cfg: cfg.withDefaults(), now: time.Now}
}

func (s *Service) url(key string) string {
	if key == "" {
		return ""
	}
	return s.cfg.PublicBaseURL + "/media/" + key
}

// Input is one upload. Poster is an optional still for a video.
type Input struct {
	File       io.ReadSeeker
	Size       int64
	Poster     []byte
	Width      int
	Height     int
	DurationMS int
}

// sniff decides the kind from the bytes, never from the client's Content-Type.
func sniff(head []byte) (kind, contentType, ext string) {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if string(head[8:12]) == "qt  " {
			return "video", "video/quicktime", "mov"
		}
		return "video", "video/mp4", "mp4"
	}
	switch ct := strings.SplitN(http.DetectContentType(head), ";", 2)[0]; ct {
	case "image/jpeg":
		return "image", ct, "jpg"
	case "image/png":
		return "image", ct, "jpg" // re-encoded as JPEG
	case "video/webm":
		return "video", ct, "webm"
	}
	return "", "", ""
}

func (s *Service) Upload(ctx context.Context, ownerID string, in Input) (*pkgmedia.Asset, error) {
	head := make([]byte, 512)
	n, _ := io.ReadFull(in.File, head)
	head = head[:n]
	kind, ct, ext := sniff(head)
	if kind == "" {
		return nil, ErrUnsupported
	}
	if _, err := in.File.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	limit := s.cfg.MaxImageBytes
	if kind == "video" {
		limit = s.cfg.MaxVideoBytes
	}
	if in.Size <= 0 || in.Size > limit {
		return nil, ErrTooLarge
	}

	var recent int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM media_uploads WHERE owner_id = $1 AND created_at > $2::timestamptz - interval '1 hour'`,
		ownerID, s.now()).Scan(&recent); err != nil {
		return nil, err
	}
	if recent >= s.cfg.HourlyLimit {
		return nil, ErrRateLimited
	}

	id := uuid.NewString()
	base := "u/" + ownerID + "/" + id
	key, thumbKey := base+"."+ext, ""
	var w, h int
	var stored int64

	if kind == "image" {
		raw, err := io.ReadAll(in.File)
		if err != nil {
			return nil, err
		}
		full, thumb, iw, ih, err := processImage(raw)
		if err != nil {
			return nil, ErrUnsupported
		}
		thumbKey = base + "_t.jpg"
		if err := s.store.Put(ctx, key, bytes.NewReader(full), int64(len(full)), "image/jpeg"); err != nil {
			return nil, err
		}
		if err := s.store.Put(ctx, thumbKey, bytes.NewReader(thumb), int64(len(thumb)), "image/jpeg"); err != nil {
			_ = s.store.Delete(ctx, key)
			return nil, err
		}
		ct, w, h, stored = "image/jpeg", iw, ih, int64(len(full))
	} else {
		if err := s.store.Put(ctx, key, in.File, in.Size, ct); err != nil {
			return nil, err
		}
		stored = in.Size
		w, h = clamp(in.Width, 0, 8192), clamp(in.Height, 0, 8192)
		if len(in.Poster) > 0 {
			if _, pthumb, _, _, err := processImage(in.Poster); err == nil {
				thumbKey = base + "_t.jpg"
				if err := s.store.Put(ctx, thumbKey, bytes.NewReader(pthumb), int64(len(pthumb)), "image/jpeg"); err != nil {
					thumbKey = "" // a missing poster is not fatal
				}
			}
		}
	}
	dur := clamp(in.DurationMS, 0, 10*60*1000)
	if kind == "image" {
		dur = 0
	}

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO media_uploads (id, owner_id, kind, content_type, size_bytes, width, height, duration_ms, object_key, thumb_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::timestamptz)`,
		id, ownerID, kind, ct, stored, w, h, dur, key, thumbKey, s.now()); err != nil {
		_ = s.store.Delete(ctx, key, thumbKey)
		return nil, err
	}
	return &pkgmedia.Asset{ID: id, URL: s.url(key), ThumbURL: s.url(thumbKey), Kind: kind, Width: int32(w), Height: int32(h), DurationMS: int32(dur)}, nil
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }

// Claim satisfies pkgmedia.Resolver: every id must be an upload owned by ownerID.
func (s *Service) Claim(ctx context.Context, ownerID string, ids []string) ([]pkgmedia.Asset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return nil, pkgmedia.ErrNotFound
		}
		seen[id] = true
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, kind, width, height, duration_ms, object_key, thumb_key
		FROM media_uploads WHERE id = ANY($1::uuid[]) AND owner_id = $2`, ids, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]pkgmedia.Asset{}
	for rows.Next() {
		var a pkgmedia.Asset
		var key, thumb string
		if err := rows.Scan(&a.ID, &a.Kind, &a.Width, &a.Height, &a.DurationMS, &key, &thumb); err != nil {
			return nil, err
		}
		a.URL, a.ThumbURL = s.url(key), s.url(thumb)
		byID[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(byID) != len(ids) {
		return nil, pkgmedia.ErrNotFound
	}
	out := make([]pkgmedia.Asset, len(ids))
	for i, id := range ids {
		out[i] = byID[id]
	}
	return out, nil
}

// GC deletes uploads older than GCAge that nothing references any more —
// abandoned drafts, and media of expired stories or removed posts. One
// transaction picks rows with SKIP LOCKED, so several workers can run it.
func (s *Service) GC(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, object_key, thumb_key FROM media_uploads m
		WHERE m.created_at < $1::timestamptz - $2::float8 * interval '1 second'
		  AND NOT EXISTS (SELECT 1 FROM stories WHERE media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM post_media WHERE media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM message_media WHERE media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM profile_photos WHERE media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM plans WHERE cover_media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM verification_requests WHERE media_id = m.id)
		ORDER BY m.created_at LIMIT 200
		FOR UPDATE SKIP LOCKED`, now, s.cfg.GCAge.Seconds())
	if err != nil {
		return 0, err
	}
	var ids []string
	var keys []string
	for rows.Next() {
		var id, k, t string
		if err := rows.Scan(&id, &k, &t); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		keys = append(keys, k, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_uploads WHERE id = ANY($1::uuid[])`, ids); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	// Rows are gone; a failed object delete only leaks bytes, never data.
	_ = s.store.Delete(ctx, keys...)
	return len(ids), nil
}
