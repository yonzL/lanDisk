package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The viewer sometimes resolves workers against the document base; the alias keeps
// such a request working instead of failing with a 404/HTML "fake worker" error.
func TestRootRelativeViewerAssetsAreAliased(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/vendor/pdf/pdf.worker.mjs", "/wasm/cad/0.8.3/dwg-worker.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d, want 200 (aliased to /file-viewer)", path, rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("%s content-type = %q, want javascript", path, rec.Header().Get("Content-Type"))
		}
	}
}

func TestAliasStill404sForMissingFiles(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/vendor/pdf/nope.mjs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing aliased asset = %d, want 404", rec.Code)
	}
}

func TestAssetCountReportsEmbeddedViewerFiles(t *testing.T) {
	if n := AssetCount(); n < 10 {
		t.Fatalf("AssetCount = %d, want the embedded preview runtime", n)
	}
}
