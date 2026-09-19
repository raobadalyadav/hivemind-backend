// Package mediatest is an in-memory media.Resolver for other packages' tests.
package mediatest

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/media"
)

type Fake struct {
	pool   *pgxpool.Pool
	owner  map[string]string
	assets map[string]media.Asset
}

// New returns a fake. Pass the test database pool so each upload also gets a
// real media_uploads row (attachment tables have a foreign key to it); pass
// nil when nothing persists the id.
func New(pool *pgxpool.Pool) *Fake {
	return &Fake{pool: pool, owner: map[string]string{}, assets: map[string]media.Asset{}}
}

// Add registers an upload owned by ownerID. The id is a valid UUID so it
// passes the same parsing real ids do.
func (f *Fake) Add(ownerID, kind string) media.Asset {
	id := uuid.NewString()
	a := media.Asset{ID: id, URL: "http://media.test/media/u/" + ownerID + "/" + id + ".jpg", ThumbURL: "http://media.test/media/u/" + ownerID + "/" + id + "_t.jpg", Kind: kind, Width: 1080, Height: 1350}
	if kind == "video" {
		a.DurationMS = 8000
	}
	f.owner[id], f.assets[id] = ownerID, a
	if f.pool != nil {
		if _, err := f.pool.Exec(context.Background(), `
			INSERT INTO media_uploads (id, owner_id, kind, content_type, size_bytes, width, height, duration_ms, object_key)
			VALUES ($1, $2, $3, 'image/jpeg', 1000, $4, $5, $6, $7)`,
			id, ownerID, kind, a.Width, a.Height, a.DurationMS, "u/"+ownerID+"/"+id+".jpg"); err != nil {
			panic(err) // test helper: a broken DB should fail loudly
		}
	}
	return a
}

// Claim enforces ownership exactly like the real service.
func (f *Fake) Claim(_ context.Context, ownerID string, ids []string) ([]media.Asset, error) {
	out := make([]media.Asset, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		a, ok := f.assets[id]
		if !ok || f.owner[id] != ownerID || seen[id] {
			return nil, media.ErrNotFound
		}
		seen[id] = true
		out = append(out, a)
	}
	return out, nil
}
