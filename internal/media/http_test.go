package media

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hivemind/backend/pkg/security"
)

func TestHTTP_UploadThenServeWithRange(t *testing.T) {
	svc, _, pool := newSvc(t)
	owner := seedUser(t, pool, "http")
	issuer := security.NewTokenIssuer("test-secret", time.Hour)
	tok, _, _ := issuer.Issue(owner, "user")
	mux := http.NewServeMux()
	NewHandler(svc, issuer, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	post := func(token string, field string, data []byte) *http.Response {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile(field, "x.jpg")
		fw.Write(data)
		mw.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/v1/media", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	src := jpegWithEXIF(t, 60, 60, 1)
	if r := post("", "file", src); r.StatusCode != 401 {
		t.Fatalf("no token → 401, got %d", r.StatusCode)
	}
	if r := post("garbage", "file", src); r.StatusCode != 401 {
		t.Fatalf("bad token → 401, got %d", r.StatusCode)
	}
	if r := post(tok, "wrongfield", src); r.StatusCode != 400 {
		t.Fatalf("missing file part → 400, got %d", r.StatusCode)
	}
	if r := post(tok, "file", []byte("not an image at all"+strings.Repeat("x", 600))); r.StatusCode != 415 {
		t.Fatalf("junk → 415, got %d", r.StatusCode)
	}

	resp := post(tok, "file", src)
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload → 201, got %d %s", resp.StatusCode, b)
	}
	var out struct {
		ID, URL string
		Kind    string
	}
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(raw, &m)
	out.URL, _ = m["url"].(string)
	path := strings.TrimPrefix(out.URL, "http://host:8080")

	full, err := http.Get(srv.URL + path)
	if err != nil || full.StatusCode != 200 || full.Header.Get("Content-Type") != "image/jpeg" ||
		full.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(full.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("GET: %v %+v", err, full)
	}
	body, _ := io.ReadAll(full.Body)

	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.Header.Set("Range", "bytes=0-9")
	part, _ := http.DefaultClient.Do(req)
	chunk, _ := io.ReadAll(part.Body)
	if part.StatusCode != 206 || len(chunk) != 10 || !bytes.Equal(chunk, body[:10]) {
		t.Fatalf("Range must give 206 with the first 10 bytes, got %d len=%d", part.StatusCode, len(chunk))
	}

	for _, bad := range []string{"/media/../../etc/passwd", "/media/u/x/y.jpg", "/media/u/" + owner + "/nope.jpg", "/media/"} {
		r, _ := http.Get(srv.URL + bad)
		if r.StatusCode != 404 {
			t.Fatalf("%s must 404, got %d", bad, r.StatusCode)
		}
	}
}
