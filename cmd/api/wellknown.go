package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hivemind/backend/config"
)

// linkPrefixes are the shared-link paths the app opens (see mobile core/links.dart).
var linkPrefixes = []string{"/posts/*", "/plans/*", "/communities/*", "/people/*"}

// registerWellKnown serves what Android App Links and iOS Universal Links need to trust this domain
// (without them, taps on hivemind.app links open a browser or a chooser instead of the app), plus the
// Terms / Privacy pages from LEGAL_DIR when configured. Serve the same paths on PUBLIC_WEB_BASE_URL's host.
func registerWellKnown(mux *http.ServeMux, cfg config.Config) {
	mux.HandleFunc("/.well-known/assetlinks.json", func(w http.ResponseWriter, _ *http.Request) {
		var prints []string
		for _, p := range strings.Split(cfg.AndroidCertSHA256, ",") {
			if p = strings.ToUpper(strings.TrimSpace(p)); p != "" {
				prints = append(prints, p)
			}
		}
		if len(prints) == 0 {
			http.NotFound(w, nil)
			return
		}
		writeJSON(w, []map[string]any{{
			"relation": []string{"delegate_permission/common.handle_all_urls"},
			"target":   map[string]any{"namespace": "android_app", "package_name": cfg.AndroidPackage, "sha256_cert_fingerprints": prints},
		}})
	})
	mux.HandleFunc("/.well-known/apple-app-site-association", func(w http.ResponseWriter, _ *http.Request) {
		if cfg.AppleTeamID == "" || cfg.AppleBundleID == "" {
			http.NotFound(w, nil)
			return
		}
		comps := make([]map[string]string, 0, len(linkPrefixes))
		for _, p := range linkPrefixes {
			comps = append(comps, map[string]string{"/": p})
		}
		writeJSON(w, map[string]any{"applinks": map[string]any{"details": []map[string]any{{
			"appIDs": []string{cfg.AppleTeamID + "." + cfg.AppleBundleID}, "components": comps,
		}}}})
	})
	for _, page := range []string{"terms", "privacy", "support"} {
		page := page
		mux.HandleFunc("/"+page, func(w http.ResponseWriter, r *http.Request) {
			if cfg.LegalDir == "" {
				http.NotFound(w, r)
				return
			}
			f := filepath.Join(cfg.LegalDir, page+".html") // fixed names only: no request-controlled path
			if _, err := os.Stat(f); err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=300")
			http.ServeFile(w, r, f)
		})
	}
}

// registerAppConfig serves the switches the app checks at launch: the oldest build still allowed and an
// optional maintenance notice. Never cached, so flipping the env value takes effect on the next launch.
func registerAppConfig(mux *http.ServeMux, cfg config.Config) {
	mux.HandleFunc("/v1/app-config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{"min_version": cfg.MinAppVersion, "maintenance": cfg.MaintenanceMessage})
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(v)
}
