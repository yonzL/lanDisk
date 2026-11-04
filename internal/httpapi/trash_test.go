package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRestoreLifecycle(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	if err := os.WriteFile(filepath.Join(root, "报告.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Anonymous delete is refused.
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/shares/1/delete", `{"path":"报告.txt"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous delete = %d, want 403", rec.Code)
	}

	rec := doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/delete", `{"path":"报告.txt"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "报告.txt")); !os.IsNotExist(err) {
		t.Fatal("file still in place after delete")
	}

	// It is listed and no longer reachable through the normal API.
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/trash", "")
	var items []trashItem
	json.Unmarshal(rec.Body.Bytes(), &items)
	if len(items) != 1 || items[0].Origin != "报告.txt" {
		t.Fatalf("trash list = %+v", items)
	}
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/raw?path=%E6%8A%A5%E5%91%8A.txt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted file still downloadable: %d", rec.Code)
	}

	// Restoring works, and a second restore of the same id is a 404.
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/trash/1/restore", ""); rec.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body)
	}
	if body, err := os.ReadFile(filepath.Join(root, "报告.txt")); err != nil || string(body) != "hello" {
		t.Fatalf("restored file = %q (%v)", body, err)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/trash/1/restore", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second restore = %d, want 404", rec.Code)
	}
}

func TestRestoreConflictRequiresForce(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/delete", `{"path":"a.txt"}`)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/trash/1/restore", ""); rec.Code != http.StatusConflict {
		t.Fatalf("restore without force = %d, want 409", rec.Code)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/trash/1/restore?force=1", ""); rec.Code != http.StatusOK {
		t.Fatalf("forced restore = %d %s", rec.Code, rec.Body)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(body) != "old" {
		t.Fatalf("forced restore content = %q", body)
	}
}

func TestPurgeTrashEmptiesBin(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/delete", `{"path":"`+name+`"}`)
	}
	rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/trash/clear", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body)
	}
	var res map[string]int
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res["removed"] != 2 {
		t.Fatalf("removed = %d, want 2", res["removed"])
	}
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/trash", "")
	var items []trashItem
	json.Unmarshal(rec.Body.Bytes(), &items)
	if len(items) != 0 {
		t.Fatalf("trash not empty: %+v", items)
	}
}

func TestDeletedEntriesAreHiddenFromListings(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gone.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/delete", `{"path":"gone.txt"}`)

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list", "")
	var listing listResponse
	json.Unmarshal(rec.Body.Bytes(), &listing)
	for _, e := range listing.Entries {
		if e.Name == ".land-trash" || e.Name == "gone.txt" {
			t.Fatalf("recycle bin leaked into the listing: %+v", listing.Entries)
		}
	}
}
