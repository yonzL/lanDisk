package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ieUA = "Mozilla/5.0 (Windows NT 6.1; Trident/7.0; rv:11.0) like Gecko"

// legacyRequest performs a request that looks like IE11.
func legacyRequest(h http.Handler, method, target, body string, contentType string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req.Header.Set("User-Agent", ieUA)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLegacyServesHTMLInsteadOfSPA(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	if err := os.MkdirAll(filepath.Join(root, "图纸"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "说明.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"产品资料","virtual_path":"/p","root":` + quoteJSON(root) + `,"root_perm":"rw"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	// Root: shares as links.
	rec := legacyRequest(h, http.MethodGet, "/", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy root = %d", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, "产品资料") || !strings.Contains(html, "/lite/s/1") {
		t.Fatalf("legacy root missing share link:\n%s", html)
	}
	if strings.Contains(html, "<div id=\"app\">") {
		t.Fatal("IE got the SPA shell instead of the legacy page")
	}

	// Listing: files, note column, download link, upload form.
	rec = legacyRequest(h, http.MethodGet, "/lite/s/1", "", "")
	html = rec.Body.String()
	if !strings.Contains(html, "说明.txt") || !strings.Contains(html, "/api/shares/1/raw?path=") {
		t.Fatalf("legacy listing missing file row:\n%s", html)
	}
	if !strings.Contains(html, "/lite/s/1/upload") || !strings.Contains(html, "/lite/s/1/mkdir") {
		t.Fatalf("writable dir should offer upload + mkdir forms:\n%s", html)
	}

	// The SPA route shape works too (a bookmark from a modern browser).
	if rec := legacyRequest(h, http.MethodGet, "/s/1", "", ""); !strings.Contains(rec.Body.String(), "说明.txt") {
		t.Fatalf("legacy /s/1 listing wrong: %d", rec.Code)
	}

	// Modern browsers still get the SPA.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), "兼容模式") {
		t.Fatal("modern browser got the legacy page")
	}

	// ?modern=1 forces the SPA from IE as an escape hatch.
	if rec := legacyRequest(h, http.MethodGet, "/?modern=1", "", ""); strings.Contains(rec.Body.String(), "兼容模式") {
		t.Fatal("?modern=1 did not bypass the legacy page")
	}
}

func TestLegacyUploadAndMkdir(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"rw"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	const boundary = "BOUNDARY"
	payload := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="path"` + "\r\n\r\n" + "\r\n" +
		"--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="报告.txt"` + "\r\n" +
		"Content-Type: text/plain\r\n\r\n" + "hello upload\r\n" +
		"--" + boundary + "--\r\n"
	rec := legacyRequest(h, http.MethodPost, "/lite/s/1/upload", payload, "multipart/form-data; boundary="+boundary)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("legacy upload = %d %s", rec.Code, rec.Body)
	}
	if data, err := os.ReadFile(filepath.Join(root, "报告.txt")); err != nil || string(data) != "hello upload" {
		t.Fatalf("uploaded file = %q (%v)", data, err)
	}

	rec = legacyRequest(h, http.MethodPost, "/lite/s/1/mkdir", "path=&name=新建目录", "application/x-www-form-urlencoded")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("legacy mkdir = %d %s", rec.Code, rec.Body)
	}
	if info, err := os.Stat(filepath.Join(root, "新建目录")); err != nil || !info.IsDir() {
		t.Fatalf("mkdir result: %v", err)
	}
}

func TestLegacyUploadRespectsReadOnlyShare(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	body := `{"name":"s","virtual_path":"/ro","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	const boundary = "B"
	payload := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="x.txt"` + "\r\n\r\n" + "x\r\n" +
		"--" + boundary + "--\r\n"
	rec := legacyRequest(h, http.MethodPost, "/lite/s/2/upload", payload, "multipart/form-data; boundary="+boundary)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload into read-only share = %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("file was written despite the read-only share")
	}
}
