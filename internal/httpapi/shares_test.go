package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"land/internal/auth"
	"land/internal/logs"
	"land/internal/store"
)

// adminWithSession returns a server whose admin has already changed the
// bootstrap password (so requireAdmin lets it through) plus its cookie.
func adminWithSession(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "land.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	hash, err := auth.HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := st.CreateAdmin(t.Context(), hash, false); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	srv := &Server{Store: st, Version: "test"}

	login := postJSON(t, srv.Handler(), "/api/login", `{"password":"correct-horse"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	return srv, login.Result().Cookies()[0]
}

func doJSON(t *testing.T, h http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminShareLifecycle(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()

	body := `{"name":"图纸","virtual_path":"/图纸","root":` + quoteJSON(root) + `,"root_perm":"rw"}`
	rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created store.Share
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID == 0 || created.Root != root {
		t.Fatalf("created share = %+v", created)
	}

	// Duplicate virtual path is a conflict, not a 500.
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", rec.Code)
	}

	// Rename.
	rec = doJSON(t, h, cookie, http.MethodPut, "/api/admin/shares/1",
		`{"name":"图纸-旧版","virtual_path":"/图纸","sort":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/shares", "")
	var views []shareView
	json.Unmarshal(rec.Body.Bytes(), &views)
	if len(views) != 1 || views[0].Share.Name != "图纸-旧版" {
		t.Fatalf("shares after update = %+v", views)
	}
	if len(views[0].Rules) != 1 || views[0].Rules[0].Perm != store.PermWrite {
		t.Fatalf("root rule = %+v, want rw", views[0].Rules)
	}

	// Delete.
	if rec := doJSON(t, h, cookie, http.MethodDelete, "/api/admin/shares/1", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/shares", "")
	json.Unmarshal(rec.Body.Bytes(), &views)
	if len(views) != 0 {
		t.Fatalf("shares after delete = %+v", views)
	}
}

func TestCreateShareValidatesRootDirectory(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()

	missing := filepath.Join(t.TempDir(), "nope")
	body := `{"name":"x","virtual_path":"/x","root":` + quoteJSON(missing) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing root = %d, want 400", rec.Code)
	}

	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body = `{"name":"x","virtual_path":"/x","root":` + quoteJSON(file) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("file as root = %d, want 400", rec.Code)
	}

	body = `{"name":"x","virtual_path":"x","root":` + quoteJSON(t.TempDir()) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("virtual path without slash = %d, want 400", rec.Code)
	}
}

func TestPublicSharesHideNonReadableShares(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()

	for _, tc := range []struct{ name, path, perm string }{
		{"公开", "/public", "r"},
		{"隐藏", "/hidden", "none"},
	} {
		body := `{"name":"` + tc.name + `","virtual_path":"` + tc.path + `","root":` +
			quoteJSON(t.TempDir()) + `,"root_perm":"` + tc.perm + `"}`
		if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", tc.name, rec.Code, rec.Body)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/shares", nil))
	var public []publicShare
	json.Unmarshal(rec.Body.Bytes(), &public)
	if len(public) != 1 || public[0].Name != "公开" {
		t.Fatalf("public shares = %+v, want only 公开", public)
	}
}

func TestRulesCRUDRejectsRootDeletion(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	body := `{"name":"图纸","virtual_path":"/图纸","root":` + quoteJSON(t.TempDir()) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules",
		`{"rel_path":"公开","perm":"rw"}`); rec.Code != http.StatusOK {
		t.Fatalf("set rule = %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, h, cookie, http.MethodGet, "/api/admin/shares/1/rules", "")
	var rules []store.Rule
	json.Unmarshal(rec.Body.Bytes(), &rules)
	if len(rules) != 2 || rules[1].RelPath != "公开" || rules[1].Perm != store.PermWrite {
		t.Fatalf("rules = %+v", rules)
	}

	if rec := doJSON(t, h, cookie, http.MethodDelete, "/api/admin/shares/1/rules?rel_path=", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete root rule = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, h, cookie, http.MethodDelete, "/api/admin/shares/1/rules?rel_path=公开", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete rule = %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules",
		`{"rel_path":"../etc","perm":"rw"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("escaping rule path = %d, want 400", rec.Code)
	}
}

func TestAdminShareRoutesRejectAnonymous(t *testing.T) {
	srv, _ := adminWithSession(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/shares"},
		{http.MethodPost, "/api/admin/shares"},
		{http.MethodPut, "/api/admin/shares/1"},
		{http.MethodDelete, "/api/admin/shares/1"},
		{http.MethodPost, "/api/admin/shares/1/rules"},
	} {
		if rec := doJSON(t, srv.Handler(), nil, route.method, route.path, `{}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

func quoteJSON(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestVirtualPathIsStoredCanonical(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	body := `{"name":"x","virtual_path":"/a//b/","root":` + quoteJSON(t.TempDir()) + `}`
	rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created store.Share
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.VirtualPath != "/a/b" {
		t.Fatalf("virtual_path = %q, want /a/b", created.VirtualPath)
	}

	// A logically identical address must not create a second share.
	body = `{"name":"y","virtual_path":"/a/./b","root":` + quoteJSON(t.TempDir()) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate normalized path = %d, want 409", rec.Code)
	}

	// The bare root is not a shareable virtual path.
	body = `{"name":"z","virtual_path":"/","root":` + quoteJSON(t.TempDir()) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("virtual path / = %d, want 400", rec.Code)
	}
}

func TestPublicSharesIncludesHiddenShareForAdmin(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	body := `{"name":"财务","virtual_path":"/finance","root":` + quoteJSON(t.TempDir()) + `,"root_perm":"none"}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/shares", nil))
	var anon []publicShare
	json.Unmarshal(rec.Body.Bytes(), &anon)
	if len(anon) != 0 {
		t.Fatalf("anonymous saw a hidden share: %+v", anon)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/shares", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var admin []publicShare
	json.Unmarshal(rec.Body.Bytes(), &admin)
	if len(admin) != 1 || admin[0].Name != "财务" {
		t.Fatalf("admin did not see the hidden share: %+v", admin)
	}
}

func TestRuleRoutesRejectMissingShare(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/shares/42/rules", ""},
		{http.MethodPost, "/api/admin/shares/42/rules", `{"rel_path":"a","perm":"r"}`},
		{http.MethodDelete, "/api/admin/shares/42/rules?rel_path=a", ""},
	} {
		if rec := doJSON(t, h, cookie, tc.method, tc.path, tc.body); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

func TestUpdateCanonicalisesVirtualPath(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	body := `{"name":"x","virtual_path":"/other","root":` + quoteJSON(t.TempDir()) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}

	rec := doJSON(t, h, cookie, http.MethodPut, "/api/admin/shares/1",
		`{"name":"x","virtual_path":"/renamed//","sort":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/shares", "")
	var views []shareView
	json.Unmarshal(rec.Body.Bytes(), &views)
	if views[0].Share.VirtualPath != "/renamed" {
		t.Fatalf("virtual_path after PUT = %q, want /renamed", views[0].Share.VirtualPath)
	}
	// A canonicalized PUT that lands on another share's address must conflict,
	// not silently create a second routable alias.
	body = `{"name":"y","virtual_path":"/second","root":` + quoteJSON(t.TempDir()) + `}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
		t.Fatalf("create second = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, cookie, http.MethodPut, "/api/admin/shares/1",
		`{"name":"x","virtual_path":"/second//","sort":0}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT onto existing address = %d, want 409", rec.Code)
	}
}

func TestRulePathsCaseFoldOnWrite(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	body := `{"name":"x","virtual_path":"/x","root":` + quoteJSON(t.TempDir()) + `,"root_perm":"rw"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"HR","perm":"none"}`)
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"hr","perm":"rw"}`)

	rec := doJSON(t, h, cookie, http.MethodGet, "/api/admin/shares/1/rules", "")
	var rules []store.Rule
	json.Unmarshal(rec.Body.Bytes(), &rules)
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want exactly one rule besides the root (case folded)", rules)
	}
	if rules[1].Perm != store.PermWrite {
		t.Fatalf("rule perm = %s, want the upserted rw", rules[1].Perm)
	}

	// Deleting with a different case must remove the same row.
	if rec := doJSON(t, h, cookie, http.MethodDelete, "/api/admin/shares/1/rules?rel_path=Hr", ""); rec.Code != http.StatusOK {
		t.Fatalf("case-insensitive delete = %d %s", rec.Code, rec.Body)
	}
	// Deleting again finds nothing.
	if rec := doJSON(t, h, cookie, http.MethodDelete, "/api/admin/shares/1/rules?rel_path=hr", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", rec.Code)
	}
}

func TestListEndpointRespectsPermissions(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "公开"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "财务"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".landpart-1"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"共享","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"财务","perm":"none"}`)
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"公开","perm":"rw"}`)

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var listing listResponse
	json.Unmarshal(rec.Body.Bytes(), &listing)
	names := map[string]bool{}
	for _, e := range listing.Entries {
		names[e.Name] = true
	}
	if names["财务"] || names[".landpart-1"] {
		t.Fatalf("hidden entry leaked into the listing: %v", names)
	}
	if !names["公开"] || !names["readme.txt"] {
		t.Fatalf("visible entries missing: %v", names)
	}
	if listing.Writable {
		t.Fatal("root reported writable for an anonymous visitor with r")
	}

	// Hidden subtree → 404, not 403.
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list?path=%E8%B4%A2%E5%8A%A1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("hidden dir = %d, want 404", rec.Code)
	}

	// Writable subtree is reported as such.
	rec = doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list?path=%E5%85%AC%E5%BC%80", "")
	json.Unmarshal(rec.Body.Bytes(), &listing)
	if !listing.Writable {
		t.Fatal("rw subtree not reported writable")
	}
}

func TestListClampsPageParams(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list?page_size=9999&page=0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var listing listResponse
	json.Unmarshal(rec.Body.Bytes(), &listing)
	if listing.PageSize != 500 {
		t.Fatalf("page_size echo = %d, want clamped 500", listing.PageSize)
	}
	if listing.Page != 1 {
		t.Fatalf("page echo = %d, want clamped 1", listing.Page)
	}

	// Huge page numbers must be an empty page, not a panic.
	rec = doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list?page=92233720368547760", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("huge page = %d, want 200 with empty entries", rec.Code)
	}
}

func TestListSortByNoteUsesStoredNotes(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	// Seed notes straight through the store (the note API lands in the next step).
	if err := srv.Store.SetNote(t.Context(), 1, "a.txt", "zzz", 1); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SetNote(t.Context(), 1, "b.txt", "aaa", 1); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list?sort=note", "")
	var listing listResponse
	json.Unmarshal(rec.Body.Bytes(), &listing)
	if len(listing.Entries) != 2 || listing.Entries[0].Name != "b.txt" {
		t.Fatalf("note-sorted listing = %+v, want b.txt first (note aaa)", listing.Entries)
	}
}

func TestNotesAreAdminOnlyAndShowInListings(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	// Anonymous cannot write notes.
	if rec := doJSON(t, h, nil, http.MethodPost, "/api/admin/notes",
		`{"share_id":1,"path":"a.txt","note":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous note write = %d, want 401", rec.Code)
	}

	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/notes",
		`{"share_id":1,"path":"a.txt","note":"甲方终版"}`); rec.Code != http.StatusOK {
		t.Fatalf("note write = %d %s", rec.Code, rec.Body)
	}

	// Everyone sees the note in the listing.
	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list", "")
	var listing listResponse
	json.Unmarshal(rec.Body.Bytes(), &listing)
	if len(listing.Entries) != 1 || listing.Entries[0].Note != "甲方终版" {
		t.Fatalf("listing notes = %+v", listing.Entries)
	}

	// Reading it back as admin, and clearing it.
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/notes?share_id=1&path=a.txt", "")
	var got struct {
		Note   string `json:"note"`
		Exists bool   `json:"exists"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Exists || got.Note != "甲方终版" {
		t.Fatalf("note read = %+v", got)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/notes",
		`{"share_id":1,"path":"a.txt","note":""}`); rec.Code != http.StatusOK {
		t.Fatalf("note clear = %d", rec.Code)
	}
	rec = doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list", "")
	json.Unmarshal(rec.Body.Bytes(), &listing)
	if listing.Entries[0].Note != "" {
		t.Fatalf("note survived clearing: %+v", listing.Entries)
	}

	// Escaping paths and unknown shares are refused.
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/notes",
		`{"share_id":1,"path":"../etc","note":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("escaping note path = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/notes",
		`{"share_id":9,"path":"a.txt","note":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("note on missing share = %d, want 404", rec.Code)
	}
}

func TestRawDownloadRangeAndPermissions(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	content := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(root, "data.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "机密"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "机密", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"机密","perm":"none"}`)

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/raw?path=data.bin", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "0123456789" {
		t.Fatalf("download = %d %q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("content-disposition = %q", cd)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/shares/1/raw?path=data.bin", nil)
	req.Header.Set("Range", "bytes=2-4")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" {
		t.Fatalf("range = %d %q, want 206 234", rec.Code, rec.Body.String())
	}

	// Hidden subtree and reserved names are 404, directories are not files.
	for _, path := range []string{"机密/x.txt", ".land-trash/x", ""} {
		if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/raw?path="+url.QueryEscape(path), ""); rec.Code != http.StatusNotFound {
			t.Fatalf("raw %q = %d, want 404", path, rec.Code)
		}
	}
}

func TestZipDownloadSkipsHiddenSubtree(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "公开"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "机密"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "公开", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "机密", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"机密","perm":"none"}`)

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/zip", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("zip = %d", rec.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "机密") {
			t.Fatalf("hidden subtree leaked into zip: %s", f.Name)
		}
	}
	if len(zr.File) < 2 {
		t.Fatalf("zip is missing visible entries: %v", zr.File)
	}
}

func TestSearchEndpointContract(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "图纸", "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "图纸", "2024", "合同.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "机密"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares/1/rules", `{"rel_path":"机密","perm":"none"}`)
	if err := srv.Store.SetNote(t.Context(), 1, "图纸/2024/合同.pdf", "甲方终版", 1); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/search?q="+url.QueryEscape("合同"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d %s", rec.Code, rec.Body)
	}
	var res searchResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Entries) != 1 || res.Entries[0].Path != "图纸/2024/合同.pdf" {
		t.Fatalf("entries = %+v", res.Entries)
	}
	if res.Entries[0].Note != "甲方终版" {
		t.Fatalf("note not attached: %+v", res.Entries[0])
	}

	// Missing query, oversized query, hidden path.
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/search?q=", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty q = %d, want 400", rec.Code)
	}
	long := strings.Repeat("a", 101)
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/search?q="+long, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("long q = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/search?path=%E6%9C%BA%E5%AF%86&q=x", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("hidden path = %d, want 404", rec.Code)
	}
}

func TestSearchReportsDepthLimit(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()
	deep := root
	for i := 0; i < 15; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "合同.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body)

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/search?q="+url.QueryEscape("合同"), "")
	var res searchResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.DepthLimited {
		t.Fatal("deep match was pruned without reporting depth_limited")
	}
}

func TestLogsRecordActionsAndExportCSV(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	h := srv.Handler()
	writer := logs.New(srv.Store, slog.Default())
	srv.Logs = writer
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A browse, a download and a delete go through the audit middleware.
	doJSON(t, h, nil, http.MethodGet, "/api/shares/1/list", "")
	doJSON(t, h, nil, http.MethodGet, "/api/shares/1/raw?path=a.txt", "")
	doJSON(t, h, cookie, http.MethodPost, "/api/shares/1/delete", `{"path":"a.txt"}`)

	// Host curation applies to entries written before it.
	rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/hosts", `{"ip":"192.0.2.1","hostname":"本机"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set host = %d %s", rec.Code, rec.Body)
	}

	// Flush the writer instead of sleeping on its one-second ticker.
	writer.Close()

	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/logs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("logs = %d %s", rec.Code, rec.Body)
	}
	var res struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Total < 3 {
		t.Fatalf("total = %d, want at least 3 (browse/download/delete)", res.Total)
	}
	actions := map[string]bool{}
	for _, e := range res.Entries {
		actions[e["action"].(string)] = true
	}
	for _, want := range []string{"browse", "download", "delete"} {
		if !actions[want] {
			t.Fatalf("action %q missing from %v", want, actions)
		}
	}

	// CSV export carries the same rows plus the curated host.
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/logs/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "主机名") || !strings.Contains(body, "本机") {
		t.Fatalf("csv missing header or curated host:\n%s", body)
	}
}

func TestIPRulesAndSettingsEndpoints(t *testing.T) {
	_, srv, cookie := mkdirRoot(t)
	h := srv.Handler()

	// Adding a deny rule applies immediately.
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/ip-rules",
		`{"cidr":"10.9.9.9","kind":"deny","note":"scanner"}`); rec.Code != http.StatusOK {
		t.Fatalf("set rule = %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, h, cookie, http.MethodGet, "/api/admin/ip-rules", "")
	var rules []ipRuleItem
	json.Unmarshal(rec.Body.Bytes(), &rules)
	if len(rules) != 1 || rules[0].CIDR != "10.9.9.9/32" || rules[0].Kind != "deny" {
		t.Fatalf("rules = %+v", rules)
	}

	// A request from that address is refused before it reaches a handler.
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "10.9.9.9:1234"
	blocked := httptest.NewRecorder()
	h.ServeHTTP(blocked, req)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("blocked request = %d, want 403", blocked.Code)
	}
	// Loopback keeps working.
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("loopback = %d, want 200", ok.Code)
	}

	// Settings: partial update, validation, and unknown keys.
	if rec := doJSON(t, h, cookie, http.MethodPut, "/api/admin/settings",
		`{"values":{"site_title":"研发部共享","max_conns":"32"}}`); rec.Code != http.StatusOK {
		t.Fatalf("put settings = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/settings", "")
	var settings settingsResponse
	json.Unmarshal(rec.Body.Bytes(), &settings)
	if settings.Values["site_title"] != "研发部共享" || settings.Values["max_conns"] != "32" {
		t.Fatalf("settings = %+v", settings.Values)
	}
	if rec := doJSON(t, h, cookie, http.MethodPut, "/api/admin/settings",
		`{"values":{"port":"99999"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid port setting = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, h, cookie, http.MethodPut, "/api/admin/settings",
		`{"values":{"nope":"x"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown setting = %d, want 400", rec.Code)
	}
}

func TestLogTargetShowsDecodedChinesePath(t *testing.T) {
	root, srv, cookie := mkdirRoot(t)
	writer := logs.New(srv.Store, slog.Default())
	srv.Logs = writer
	h := srv.Handler()
	if err := os.MkdirAll(filepath.Join(root, "技术资料", "图纸"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "技术资料", "图纸", "平面图.dwg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	doJSON(t, h, nil, http.MethodGet, "/api/shares/1/raw?path="+url.QueryEscape("技术资料/图纸/平面图.dwg"), "")
	writer.Close()

	rec := doJSON(t, h, cookie, http.MethodGet, "/api/admin/logs?action=download", "")
	var res struct {
		Entries []map[string]any `json:"entries"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Entries) == 0 {
		t.Fatal("no download entry recorded")
	}
	path, _ := res.Entries[0]["path"].(string)
	if !strings.Contains(path, "技术资料/图纸/平面图.dwg") {
		t.Fatalf("log path = %q, want the decoded Chinese path", path)
	}
	if strings.Contains(path, "%E") {
		t.Fatalf("log path still percent-encoded: %q", path)
	}
}

func TestNoticeIsNormalisedToOneLine(t *testing.T) {
	_, srv, cookie := mkdirRoot(t)
	h := srv.Handler()

	payload := `{"values":{"site_notice":"第一行\n第二行\t<b>粗体</b>   多空格"}}`
	if rec := doJSON(t, h, cookie, http.MethodPut, "/api/admin/settings", payload); rec.Code != http.StatusOK {
		t.Fatalf("put settings = %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, h, cookie, http.MethodGet, "/api/admin/settings", "")
	var settings settingsResponse
	json.Unmarshal(rec.Body.Bytes(), &settings)
	notice := settings.Values["site_notice"]
	if strings.ContainsAny(notice, "\n\r\t") {
		t.Fatalf("notice still contains a line break: %q", notice)
	}
	if notice != "第一行 第二行 <b>粗体</b> 多空格" {
		t.Fatalf("notice = %q", notice)
	}

	// Length is capped so a huge paste cannot push the header around.
	long := strings.Repeat("长", 300)
	payload = `{"values":{"site_notice":` + quoteJSON(long) + `}}`
	doJSON(t, h, cookie, http.MethodPut, "/api/admin/settings", payload)
	rec = doJSON(t, h, cookie, http.MethodGet, "/api/admin/settings", "")
	json.Unmarshal(rec.Body.Bytes(), &settings)
	if runes := []rune(settings.Values["site_notice"]); len(runes) != 200 {
		t.Fatalf("notice length = %d, want capped at 200", len(runes))
	}
}

func TestThumbEndpoint(t *testing.T) {
	srv, cookie := adminWithSession(t)
	h := srv.Handler()
	root := t.TempDir()

	// A real PNG so decoding succeeds.
	img := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.NRGBA{R: 30, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photo.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"s","virtual_path":"/s","root":` + quoteJSON(root) + `,"root_perm":"r"}`
	if rec := doJSON(t, h, cookie, http.MethodPost, "/api/admin/shares", body); rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d %s", rec.Code, rec.Body)
	}

	rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/thumb?path=photo.png", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("thumb = %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type = %q, want image/jpeg", ct)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the thumbnail response")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode thumb: %v", err)
	}
	if cfg.Width != 96 || cfg.Height != 48 {
		t.Fatalf("thumb = %dx%d, want 96x48", cfg.Width, cfg.Height)
	}

	// Conditional request is answered without a body.
	req := httptest.NewRequest(http.MethodGet, "/api/shares/1/thumb?path=photo.png", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("conditional thumb = %d, want 304", rec2.Code)
	}

	// Non-images are refused, so the list can fall back to an icon.
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/thumb?path=notes.txt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("text thumb = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, h, nil, http.MethodGet, "/api/shares/1/thumb?path=..%2Fsecret.png", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("escaping thumb path = %d, want 404", rec.Code)
	}
}
