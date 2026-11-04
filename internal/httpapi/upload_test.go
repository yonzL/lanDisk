package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"land/internal/upload"
)

func charlie(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return b
}

func TestUploadLifecycle(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"rw"}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
		t.Fatalf("create share: %d %s", rec.Code, rec.Body)
	}

	size := int64(upload.ChunkSize + 5)
	begin := doJSON(t, h, cookie, http.MethodPost, "/api/uploads",
		`{"share_id":1,"path":"","name":"大文件.bin","size":`+itoa(int(size))+`}`)
	if begin.Code != http.StatusCreated {
		t.Fatalf("begin = %d %s", begin.Code, begin.Body)
	}
	var session uploadResponse
	json.Unmarshal(begin.Body.Bytes(), &session)
	if session.ID == "" || session.Chunk != upload.ChunkSize || len(session.Received) != 0 {
		t.Fatalf("session = %+v", session)
	}

	// Chunk 0: exact size.
	req := httptest.NewRequest(http.MethodPut, "/api/uploads/"+session.ID+"/0", bytes.NewReader(charlie(upload.ChunkSize)))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chunk 0 = %d %s", rec.Code, rec.Body)
	}

	// Wrong size for the tail chunk is a 400, not a truncated file.
	req = httptest.NewRequest(http.MethodPut, "/api/uploads/"+session.ID+"/1", bytes.NewReader(charlie(3)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short tail = %d, want 400", rec.Code)
	}

	// Out-of-range index.
	req = httptest.NewRequest(http.MethodPut, "/api/uploads/"+session.ID+"/9", bytes.NewReader(charlie(5)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("index 9 = %d, want 400", rec.Code)
	}

	// Completing early must fail.
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/uploads/"+session.ID+"/complete", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("early complete = %d, want 400", rec.Code)
	}

	// Upload the tail and complete.
	req = httptest.NewRequest(http.MethodPut, "/api/uploads/"+session.ID+"/1", bytes.NewReader(charlie(5)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tail chunk = %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/uploads/"+session.ID+"/complete", ""); rec.Code != http.StatusOK {
		t.Fatalf("complete = %d %s", rec.Code, rec.Body)
	}

	stored, err := os.ReadFile(filepath.Join(root, "大文件.bin"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if int64(len(stored)) != size {
		t.Fatalf("stored size = %d, want %d", len(stored), size)
	}

	// Part file is gone and no reserved names remain in the directory.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".landpart-") {
			t.Fatalf("part file left behind: %s", e.Name())
		}
	}
}

func TestUploadRespectsWritePermission(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	rec := doJSON(t, h, nil, http.MethodPost, "/api/uploads",
		`{"share_id":1,"path":"","name":"x.bin","size":1}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous upload into read-only dir = %d, want 403", rec.Code)
	}
}

func TestUploadRejectsReservedNames(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"rw"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	for _, name := range []string{"x.landpart-1", ".land-trash", "../escape", ".hidden"} {
		payload := `{"share_id":1,"path":"","name":` + quoteJSON(name) + `,"size":1}`
		if rec := doJSON(t, h, cookie, http.MethodPost, "/api/uploads", payload); rec.Code != http.StatusBadRequest {
			t.Fatalf("name %q = %d, want 400", name, rec.Code)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
