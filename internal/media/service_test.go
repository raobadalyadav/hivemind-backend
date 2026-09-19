package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgmedia "github.com/hivemind/backend/pkg/media"
)

type memStore struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newMem() *memStore { return &memStore{objs: map[string][]byte{}} }

func (m *memStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	m.mu.Lock()
	m.objs[key] = b
	m.mu.Unlock()
	return err
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func (m *memStore) Open(_ context.Context, key string) (*Object, error) {
	m.mu.Lock()
	b, ok := m.objs[key]
	m.mu.Unlock()
	if !ok {
		return nil, errors.New("not found")
	}
	return &Object{ReadSeekCloser: nopCloser{bytes.NewReader(b)}, Size: int64(len(b)), ModTime: time.Unix(1, 0)}, nil
}

func (m *memStore) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	for _, k := range keys {
		delete(m.objs, k)
	}
	m.mu.Unlock()
	return nil
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: %v", err)
	}
	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"md-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// jpegWithEXIF returns a w×h JPEG (left half red, right half blue) carrying an
// APP1 EXIF segment with the given orientation and a fake GPS marker string.
func jpegWithEXIF(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// TIFF (little-endian) with one IFD entry: orientation.
	tiff := []byte{'I', 'I', 0x2A, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0, 0, 0, 0, 0}
	seg := append([]byte("Exif\x00\x00"), tiff...)
	seg = append(seg, []byte("GPSLatitude=28.61")...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	out := append([]byte{0xFF, 0xD8}, app1...)
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func newSvc(t *testing.T) (*Service, *memStore, *pgxpool.Pool) {
	pool := testPool(t)
	t.Cleanup(pool.Close)
	st := newMem()
	return NewService(pool, st, Config{PublicBaseURL: "http://host:8080", MaxImageBytes: 1 << 20, MaxVideoBytes: 2 << 20, HourlyLimit: 5}), st, pool
}

func TestUpload_ImageIsReencodedStrippedAndOriented(t *testing.T) {
	svc, st, pool := newSvc(t)
	owner := seedUser(t, pool, "img")
	src := jpegWithEXIF(t, 80, 40, 6) // stored sideways: must be rotated 90° clockwise

	a, err := svc.Upload(context.Background(), owner, Input{File: bytes.NewReader(src), Size: int64(len(src))})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if a.Kind != "image" || a.Width != 40 || a.Height != 80 {
		t.Fatalf("orientation 6 must swap dimensions to 40×80, got %+v", a)
	}
	if !strings.HasPrefix(a.URL, "http://host:8080/media/u/"+owner+"/") || a.ThumbURL == "" {
		t.Fatalf("urls: %+v", a)
	}
	var stored []byte
	for k, v := range st.objs {
		if strings.HasSuffix(k, ".jpg") && !strings.HasSuffix(k, "_t.jpg") {
			stored = v
		}
	}
	if bytes.Contains(stored, []byte("Exif")) || bytes.Contains(stored, []byte("GPSLatitude")) {
		t.Fatal("EXIF/GPS metadata must not survive re-encoding")
	}
	img, err := jpeg.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("stored file must be a valid JPEG: %v", err)
	}
	// After rotating 90° clockwise, the red (left) half ends up on TOP.
	if r, _, b, _ := img.At(20, 10).RGBA(); r>>8 < 200 || b>>8 > 60 {
		t.Fatalf("top should be red after rotation, got r=%d b=%d", r>>8, b>>8)
	}
	if r, _, b, _ := img.At(20, 70).RGBA(); b>>8 < 200 || r>>8 > 60 {
		t.Fatalf("bottom should be blue after rotation")
	}
}

func TestUpload_PNGBecomesJPEGAndLargeIsCapped(t *testing.T) {
	svc, _, pool := newSvc(t)
	owner := seedUser(t, pool, "png")
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 1000))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	svc.cfg.MaxImageBytes = 50 << 20
	a, err := svc.Upload(context.Background(), owner, Input{File: bytes.NewReader(buf.Bytes()), Size: int64(buf.Len())})
	if err != nil || a.Width != 2048 || a.Height != 682 {
		t.Fatalf("long edge is capped at 2048 keeping aspect: %+v err=%v", a, err)
	}
}

func TestUpload_RejectsDisguisedAndOversizedAndFlooding(t *testing.T) {
	svc, _, pool := newSvc(t)
	ctx := context.Background()
	owner := seedUser(t, pool, "bad")

	exe := append([]byte("MZ\x90\x00"), bytes.Repeat([]byte{0}, 600)...)
	if _, err := svc.Upload(ctx, owner, Input{File: bytes.NewReader(exe), Size: int64(len(exe))}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("an executable named .jpg must be rejected by content sniffing, got %v", err)
	}
	html := []byte("<html><script>alert(1)</script></html>" + strings.Repeat(" ", 600))
	if _, err := svc.Upload(ctx, owner, Input{File: bytes.NewReader(html), Size: int64(len(html))}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("html must be rejected, got %v", err)
	}
	// JPEG header followed by garbage passes the sniff but not the decoder.
	fake := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 900)...)
	if _, err := svc.Upload(ctx, owner, Input{File: bytes.NewReader(fake), Size: int64(len(fake))}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a corrupt JPEG must be rejected, got %v", err)
	}
	big := jpegWithEXIF(t, 40, 40, 1)
	if _, err := svc.Upload(ctx, owner, Input{File: bytes.NewReader(big), Size: 2 << 20}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("declared size over the limit: %v", err)
	}

	ok := jpegWithEXIF(t, 40, 40, 1)
	for i := 0; i < 5; i++ {
		if _, err := svc.Upload(ctx, owner, Input{File: bytes.NewReader(ok), Size: int64(len(ok))}); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	if _, err := svc.Upload(ctx, owner, Input{File: bytes.NewReader(ok), Size: int64(len(ok))}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("the 6th upload within the hour must be rate limited, got %v", err)
	}
}

func TestUpload_VideoPassthroughWithPoster(t *testing.T) {
	svc, st, pool := newSvc(t)
	owner := seedUser(t, pool, "vid")
	mp4 := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}, bytes.Repeat([]byte{7}, 4096)...)
	poster := jpegWithEXIF(t, 90, 160, 1)

	a, err := svc.Upload(context.Background(), owner, Input{File: bytes.NewReader(mp4), Size: int64(len(mp4)), Poster: poster, Width: 1080, Height: 1920, DurationMS: 12345})
	if err != nil {
		t.Fatalf("video upload: %v", err)
	}
	if a.Kind != "video" || a.DurationMS != 12345 || a.Width != 1080 || !strings.HasSuffix(a.URL, ".mp4") || a.ThumbURL == "" {
		t.Fatalf("video asset: %+v", a)
	}
	var raw []byte
	for k, v := range st.objs {
		if strings.HasSuffix(k, ".mp4") {
			raw = v
		}
	}
	if !bytes.Equal(raw, mp4) {
		t.Fatal("video bytes are stored untouched")
	}
}

func TestClaim_OnlyTheOwnerCanUseAnUpload(t *testing.T) {
	svc, _, pool := newSvc(t)
	ctx := context.Background()
	alice, mallory := seedUser(t, pool, "alice"), seedUser(t, pool, "mallory")
	src := jpegWithEXIF(t, 40, 40, 1)
	a1, _ := svc.Upload(ctx, alice, Input{File: bytes.NewReader(src), Size: int64(len(src))})
	a2, _ := svc.Upload(ctx, alice, Input{File: bytes.NewReader(src), Size: int64(len(src))})

	got, err := svc.Claim(ctx, alice, []string{a2.ID, a1.ID})
	if err != nil || len(got) != 2 || got[0].ID != a2.ID || got[1].ID != a1.ID {
		t.Fatalf("owner claims in requested order: %+v err=%v", got, err)
	}
	if _, err := svc.Claim(ctx, mallory, []string{a1.ID}); !errors.Is(err, pkgmedia.ErrNotFound) {
		t.Fatalf("someone else's upload must look nonexistent, got %v", err)
	}
	if _, err := svc.Claim(ctx, alice, []string{a1.ID, "00000000-0000-0000-0000-000000000000"}); !errors.Is(err, pkgmedia.ErrNotFound) {
		t.Fatalf("an unknown id fails the whole claim, got %v", err)
	}
	if _, err := svc.Claim(ctx, alice, []string{a1.ID, a1.ID}); !errors.Is(err, pkgmedia.ErrNotFound) {
		t.Fatalf("duplicates are rejected, got %v", err)
	}
	if _, err := svc.Claim(ctx, alice, []string{"http://evil.example/x.jpg"}); !errors.Is(err, pkgmedia.ErrNotFound) {
		t.Fatalf("a URL is not an id, got %v", err)
	}
}

func TestGC_DeletesOnlyOldUnreferencedUploads(t *testing.T) {
	svc, st, pool := newSvc(t)
	ctx := context.Background()
	owner := seedUser(t, pool, "gc")
	src := jpegWithEXIF(t, 40, 40, 1)
	orphan, _ := svc.Upload(ctx, owner, Input{File: bytes.NewReader(src), Size: int64(len(src))})
	kept, _ := svc.Upload(ctx, owner, Input{File: bytes.NewReader(src), Size: int64(len(src))})
	fresh, _ := svc.Upload(ctx, owner, Input{File: bytes.NewReader(src), Size: int64(len(src))})
	if _, err := pool.Exec(ctx, `INSERT INTO stories (author_id, media_url, audience, media_id, expires_at) VALUES ($1, 'x', 'connections', $2, now() + interval '1 day')`, owner, kept.ID); err != nil {
		t.Fatalf("seed story: %v", err)
	}
	pool.Exec(ctx, `UPDATE media_uploads SET created_at = now() - interval '2 days' WHERE id = ANY($1::uuid[])`, []string{orphan.ID, kept.ID})

	n, err := svc.GC(ctx, time.Now())
	if err != nil || n < 1 {
		t.Fatalf("gc: n=%d err=%v", n, err)
	}
	exists := func(id string) bool {
		var ok bool
		pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_uploads WHERE id = $1)`, id).Scan(&ok)
		return ok
	}
	if exists(orphan.ID) {
		t.Fatal("an old unreferenced upload must be collected")
	}
	if !exists(kept.ID) || !exists(fresh.ID) {
		t.Fatal("a referenced upload and a fresh draft must survive")
	}
	for k := range st.objs {
		if strings.Contains(k, orphan.ID) {
			t.Fatalf("the orphan's objects must be deleted from storage: %s", k)
		}
	}
}
