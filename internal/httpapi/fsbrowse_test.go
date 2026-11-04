package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestFSBrowseListsOnlyDirectories(t *testing.T) {
	srv, cookie := adminWithSession(t)
	root := t.TempDir()
	sub := filepath.Join(root, "图纸")
	if err := os.MkdirAll(filepath.Join(sub, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv.Handler(), cookie, http.MethodGet,
		"/api/admin/fs/list?path="+url.QueryEscape(root), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var res fsListResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Entries) != 1 || res.Entries[0].Name != "图纸" {
		t.Fatalf("entries = %+v, want only the directory", res.Entries)
	}
	if res.Entries[0].Path != sub {
		t.Fatalf("path = %q, want %q", res.Entries[0].Path, sub)
	}
	if res.Parent != filepath.Dir(root) {
		t.Fatalf("parent = %q", res.Parent)
	}
}

func TestFSBrowseDriveListWhenPathEmpty(t *testing.T) {
	srv, cookie := adminWithSession(t)
	rec := doJSON(t, srv.Handler(), cookie, http.MethodGet, "/api/admin/fs/list", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("drives = %d %s", rec.Code, rec.Body)
	}
	var res fsListResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Path != "" || res.Parent != "" {
		t.Fatalf("drive list echoed a path: %+v", res)
	}
	if len(res.Entries) == 0 {
		t.Fatal("no drives returned")
	}
}

func TestFSMkdirFromPicker(t *testing.T) {
	srv, cookie := adminWithSession(t)
	root := t.TempDir()
	body := `{"path":` + quoteJSON(root) + `,"name":"新建目录"}`
	rec := doJSON(t, srv.Handler(), cookie, http.MethodPost, "/api/admin/fs/mkdir", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("mkdir = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "新建目录")); err != nil {
		t.Fatalf("directory not created: %v", err)
	}

	if rec := doJSON(t, srv.Handler(), cookie, http.MethodPost, "/api/admin/fs/mkdir", body); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate mkdir = %d, want 409", rec.Code)
	}
	bad := `{"path":` + quoteJSON(root) + `,"name":"../escape"}`
	if rec := doJSON(t, srv.Handler(), cookie, http.MethodPost, "/api/admin/fs/mkdir", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("escaping mkdir = %d, want 400", rec.Code)
	}
}

func TestFSBrowseRequiresAdmin(t *testing.T) {
	srv, _ := adminWithSession(t)
	if rec := doJSON(t, srv.Handler(), nil, http.MethodGet, "/api/admin/fs/list", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous fs list = %d, want 401", rec.Code)
	}
}
