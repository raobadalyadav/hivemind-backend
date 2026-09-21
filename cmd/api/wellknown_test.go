package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hivemind/backend/config"
)

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestWellKnown_ServedOnlyWhenConfigured(t *testing.T) {
	mux := http.NewServeMux()
	registerWellKnown(mux, config.Config{AndroidPackage: "app.hivemind.hivemind"})
	for _, p := range []string{"/.well-known/assetlinks.json", "/.well-known/apple-app-site-association", "/terms", "/privacy"} {
		if get(t, mux, p).Code != http.StatusNotFound {
			t.Errorf("%s must 404 until configured (a wrong file would silently break links)", p)
		}
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "terms.html"), []byte("<h1>Terms</h1>"), 0o644)
	mux = http.NewServeMux()
	registerWellKnown(mux, config.Config{AndroidPackage: "app.hivemind.hivemind", AndroidCertSHA256: " ab:cd:ef , 12:34 ", AppleTeamID: "TEAM123456", AppleBundleID: "app.hivemind.hivemind", LegalDir: dir})

	al := get(t, mux, "/.well-known/assetlinks.json")
	if al.Code != 200 || !strings.Contains(al.Body.String(), `"AB:CD:EF"`) || !strings.Contains(al.Body.String(), "app.hivemind.hivemind") || al.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("assetlinks: %d %s", al.Code, al.Body.String())
	}
	aasa := get(t, mux, "/.well-known/apple-app-site-association")
	if aasa.Code != 200 || !strings.Contains(aasa.Body.String(), "TEAM123456.app.hivemind.hivemind") || !strings.Contains(aasa.Body.String(), "/plans/*") {
		t.Fatalf("aasa: %d %s", aasa.Code, aasa.Body.String())
	}
	if terms := get(t, mux, "/terms"); terms.Code != 200 || !strings.Contains(terms.Body.String(), "Terms") {
		t.Fatalf("terms: %d", terms.Code)
	}
	if get(t, mux, "/privacy").Code != 404 {
		t.Fatal("a page that isn't there is a 404")
	}
}
