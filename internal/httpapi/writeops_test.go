package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"land/internal/store"
)

func mkdirRoot(t *testing.T) (string, *Server, *http.Cookie) {
	t.Helper()
	srv, cookie := adminWithSession(t)
	root := t.TempDir()
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"rw"}`
	if rec := doJSON(t, srv.Handler(), cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
		t.Fatalf("create share: %d %s", rec.Code, rec.Body)
	}
	return root, srv, cookie
}

func TestMkdirAllowedForAnonymousInWritableDir(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()

	rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/mkdir", `{"path":"","name":"公共上传"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous mkdir = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "公共上传")); err != nil {
		t.Fatalf("directory not created: %v", err)
	}

	// A read-only subtree blocks anonymous mkdir.
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"公共上传","perm":"r"}`)
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/mkdir", `{"path":"公共上传","name":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("mkdir in read-only dir = %d, want 403", rec.Code)
	}

	// Duplicate and invalid names.
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/mkdir", `{"path":"","name":"公共上传"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate mkdir = %d, want 409", rec.Code)
	}
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/mkdir", `{"path":"","name":".."}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mkdir = %d, want 400", rec.Code)
	}
}

func TestRenameAndMoveAreAdminOnlyAndMigrateNotes(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	if err := os.MkdirAll(filepath.Join(root, "归档"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "报告.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SetNote(t.Context(), 1, "报告.txt", "终版", 1); err != nil {
		t.Fatal(err)
	}

	// Anonymous cannot rename or move.
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/rename", `{"path":"报告.txt","name":"x.txt"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous rename = %d, want 403", rec.Code)
	}
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/move", `{"path":"报告.txt","to":"归档"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous move = %d, want 403", rec.Code)
	}

	// Admin renames; the note follows.
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/rename", `{"path":"报告.txt","name":"报告-终版.txt"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	note, ok, err := srv.Store.NoteFor(t.Context(), 1, "报告-终版.txt")
	if err != nil || !ok || note != "终版" {
		t.Fatalf("note after rename = (%q,%v,%v)", note, ok, err)
	}

	// Admin moves it into 归档; the note follows again.
	rec := doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/move", `{"path":"报告-终版.txt","to":"归档"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move = %d %s", rec.Code, rec.Body)
	}
	var moved struct {
		Path string `json:"path"`
	}
	json.Unmarshal(rec.Body.Bytes(), &moved)
	if moved.Path != "归档/报告-终版.txt" {
		t.Fatalf("moved path = %q", moved.Path)
	}
	if note, ok, _ := srv.Store.NoteFor(t.Context(), 1, "归档/报告-终版.txt"); !ok || note != "终版" {
		t.Fatalf("note after move = (%q,%v)", note, ok)
	}

	// Moving onto an existing name conflicts.
	if err := os.WriteFile(filepath.Join(root, "归档", "别的.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "别的.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/move", `{"path":"别的.txt","to":"归档"}`); rec.Code != http.StatusConflict {
		t.Fatalf("move onto existing = %d, want 409", rec.Code)
	}
	_ = store.PermRead
}
