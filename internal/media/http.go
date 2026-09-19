package media

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/hivemind/backend/pkg/security"
)

// keyPattern is the only shape of object key the public GET will look up:
// u/<owner-uuid>/<upload-uuid>[_t].<ext> — no path traversal, no listing.
var keyPattern = regexp.MustCompile(`^u/[0-9a-f-]{36}/[0-9a-f-]{36}(_t)?\.(jpg|mp4|mov|webm)$`)

var contentTypes = map[string]string{"jpg": "image/jpeg", "mp4": "video/mp4", "mov": "video/quicktime", "webm": "video/webm"}

type Handler struct {
	svc    *Service
	issuer *security.TokenIssuer
	log    *slog.Logger
}

func NewHandler(svc *Service, issuer *security.TokenIssuer, log *slog.Logger) *Handler {
	return &Handler{svc: svc, issuer: issuer, log: log}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/media", h.upload)
	mux.HandleFunc("GET /media/", h.serve)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	claims, err := h.issuer.Verify(r.Header.Get("Authorization"))
	if err != nil {
		httpErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	// Room for one video + one poster + form overhead; the per-kind limit is
	// enforced precisely in Service.Upload.
	r.Body = http.MaxBytesReader(w, r.Body, h.svc.cfg.MaxVideoBytes+h.svc.cfg.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpErr(w, http.StatusRequestEntityTooLarge, ErrTooLarge.Error())
			return
		}
		httpErr(w, http.StatusBadRequest, "expected multipart form with a 'file' part")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpErr(w, http.StatusBadRequest, "missing 'file'")
		return
	}
	defer file.Close()

	in := Input{File: file, Size: hdr.Size, Width: atoi(r.FormValue("width")), Height: atoi(r.FormValue("height")), DurationMS: atoi(r.FormValue("duration_ms"))}
	if pf, _, err := r.FormFile("poster"); err == nil {
		defer pf.Close()
		in.Poster, _ = io.ReadAll(io.LimitReader(pf, h.svc.cfg.MaxImageBytes))
	}

	a, err := h.svc.Upload(r.Context(), claims.UserID, in)
	switch {
	case errors.Is(err, ErrTooLarge):
		httpErr(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, ErrUnsupported):
		httpErr(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, ErrRateLimited):
		httpErr(w, http.StatusTooManyRequests, err.Error())
	case err != nil:
		h.log.Error("media upload", "error", err)
		httpErr(w, http.StatusInternalServerError, "upload failed")
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": a.ID, "url": a.URL, "thumb_url": a.ThumbURL, "kind": a.Kind,
			"width": a.Width, "height": a.Height, "duration_ms": a.DurationMS,
		})
	}
}

// serve streams an object. Keys are random UUIDs (capability URLs): knowing
// the URL is the access grant, exactly like a social network's media links.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/media/")
	if !keyPattern.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	obj, err := h.svc.store.Open(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer obj.Close()
	ext := key[strings.LastIndex(key, ".")+1:]
	w.Header().Set("Content-Type", contentTypes[ext])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", obj.ModTime, obj) // handles Range → 206
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
