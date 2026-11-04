package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"land/internal/store"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "land.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Server{Store: st, Version: "test"}
}

func TestHealthReturnsVersion(t *testing.T) {
	srv := newServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body.Status != "ok" || body.Version != "test" {
		t.Fatalf("health = %+v, want ok/test", body)
	}
}

func TestUnknownAPIRouteAnswersJSON404(t *testing.T) {
	srv := newServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", body.Code)
	}
}

func TestClientRouteServesIndex(t *testing.T) {
	srv := newServer(t)
	for _, path := range []string{"/", "/s/1/%E5%9B%BE%E7%BA%B8"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: body is not the SPA shell: %q", path, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: cache-control = %q, want no-cache", path, cc)
		}
	}
}

func TestHealthReportsShareCount(t *testing.T) {
	srv := newServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	var body struct {
		Shares int `json:"shares"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body.Shares != 0 {
		t.Fatalf("shares = %d, want 0 on a fresh database", body.Shares)
	}
}

func TestMissingAssetIs404NotSPA(t *testing.T) {
	srv := newServer(t)
	for _, path := range []string{"/assets/stale-bundle.js", "/favicon.ico"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (must not fall back to index.html)", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s: cache-control = %q, want no-store", path, cc)
		}
	}
}

func TestPreviewDeepLinkServesShell(t *testing.T) {
	srv := newServer(t)
	for _, path := range []string{"/s/1/preview/a.pdf", "/s/1/v1.2/", "/s/1/%E5%9B%BE%E7%BA%B8/dwg"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (client route must serve the shell)", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: body is not the SPA shell", path)
		}
	}
}
