package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"land/internal/auth"
	"land/internal/store"
)

func newAuthServer(t *testing.T, password string) (*Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "land.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := st.CreateAdmin(t.Context(), hash, true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return &Server{Store: st, Version: "test"}, password
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.31:51234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginSetsSessionAndReportsMustChange(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	rec := postJSON(t, srv.Handler(), "/api/login", `{"password":"`+password+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body)
	}
	var body sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Authenticated || !body.MustChange {
		t.Fatalf("session = %+v, want authenticated + must_change", body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != sessionCookie || cookies[0].Value == "" {
		t.Fatalf("no session cookie set: %v", cookies)
	}
	if !cookies[0].HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	srv, _ := newAuthServer(t, "correct-horse")
	rec := postJSON(t, srv.Handler(), "/api/login", `{"password":"nope"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("cookie issued for a failed login")
	}
}

func TestLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	for i := 0; i < auth.MaxFailures; i++ {
		postJSON(t, h, "/api/login", `{"password":"nope"}`)
	}
	rec := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d failures = %d, want 429", auth.MaxFailures, rec.Code)
	}
}

func TestSessionEndpointRequiresCookie(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	var anon sessionResponse
	json.Unmarshal(rec.Body.Bytes(), &anon)
	if anon.Authenticated {
		t.Fatal("anonymous request reported as authenticated")
	}

	login := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	cookie := login.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var authed sessionResponse
	json.Unmarshal(rec.Body.Bytes(), &authed)
	if !authed.Authenticated || !authed.MustChange {
		t.Fatalf("session = %+v, want authenticated + must_change", authed)
	}
}

func TestPasswordChangeClearsMustChange(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	login := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	cookie := login.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/api/password",
		strings.NewReader(`{"current_password":"`+password+`","new_password":"a-very-long-one"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("password change status = %d, want 200 (%s)", rec.Code, rec.Body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body sessionResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.MustChange {
		t.Fatal("must_change still set after a successful password change")
	}

	if rec := postJSON(t, h, "/api/login", `{"password":"a-very-long-one"}`); rec.Code != http.StatusOK {
		t.Fatalf("login with new password = %d, want 200", rec.Code)
	}
}

func TestPasswordChangeRejectsShortPassword(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()
	login := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	cookie := login.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/api/password",
		strings.NewReader(`{"current_password":"`+password+`","new_password":"short"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a short password", rec.Code)
	}
}

func TestRequireAdminRefusesSessionHoldingBootstrapPassword(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	login := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	cookie := login.Result().Cookies()[0]

	protected := srv.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/anything", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	protected(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 while must_change is set", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "must_change") {
		t.Fatalf("body = %s, want must_change code", rec.Body.String())
	}
}

func TestRequireAdminRejectsAnonymous(t *testing.T) {
	srv, _ := newAuthServer(t, "correct-horse")
	protected := srv.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})

	rec := httptest.NewRecorder()
	protected(rec, httptest.NewRequest(http.MethodGet, "/api/admin/anything", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestThrottleIsSharedAcrossConcurrentLogins(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	done := make(chan struct{})
	for i := 0; i < auth.MaxFailures; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"nope"}`))
			req.RemoteAddr = "192.168.1.31:51234"
			h.ServeHTTP(httptest.NewRecorder(), req)
			done <- struct{}{}
		}()
	}
	for i := 0; i < auth.MaxFailures; i++ {
		<-done
	}

	rec := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d after concurrent failures, want 429", rec.Code)
	}
}

func TestLoginCookiePersistsUntilLogout(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	login := postJSON(t, h, "/api/login", `{"username":"admin","password":"`+password+`"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body)
	}
	cookie := login.Result().Cookies()[0]
	if cookie.MaxAge <= 0 {
		t.Fatalf("session cookie MaxAge = %d, want a persistent cookie so a browser restart keeps the login", cookie.MaxAge)
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}

	// No expiry fields: the client has no deadline to enforce, the session lives
	// until an explicit logout.
	var body map[string]any
	json.Unmarshal(login.Body.Bytes(), &body)
	if _, ok := body["expires_at"]; ok {
		t.Fatalf("login response still publishes expires_at: %s", login.Body)
	}
	if body["username"] != "admin" {
		t.Fatalf("username = %v, want admin", body["username"])
	}

	// Logout answers as anonymous and clears the cookie.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	logoutReq.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, logoutReq)
	var out sessionResponse
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Authenticated {
		t.Fatalf("logout response = %+v, want anonymous", out)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) == 0 || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout did not expire the cookie: %+v", cleared)
	}
}

func TestRenameAdminThroughPasswordEndpoint(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()

	login := postJSON(t, h, "/api/login", `{"username":"admin","password":"`+password+`"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body)
	}
	cookie := login.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/api/password",
		strings.NewReader(`{"current_password":"`+password+`","new_username":"ops","new_password":"a-very-long-one"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	var body sessionResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Authenticated {
		t.Fatal("credential change should answer logged out (sessions are dropped)")
	}
	if body.Username != "ops" {
		t.Fatalf("username = %q, want ops", body.Username)
	}

	// The old cookie is dead, the old name no longer logs in, the new one does.
	req = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var current sessionResponse
	json.Unmarshal(rec.Body.Bytes(), &current)
	if current.Authenticated {
		t.Fatal("session survived the rename")
	}
	if rec := postJSON(t, h, "/api/login", `{"username":"admin","password":"a-very-long-one"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old username still logs in: %d", rec.Code)
	}
	if rec := postJSON(t, h, "/api/login", `{"username":"ops","password":"a-very-long-one"}`); rec.Code != http.StatusOK {
		t.Fatalf("new username rejected: %d %s", rec.Code, rec.Body)
	}
}

func TestRenameRejectsBadUsername(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse")
	h := srv.Handler()
	login := postJSON(t, h, "/api/login", `{"password":"`+password+`"}`)
	cookie := login.Result().Cookies()[0]

	for _, name := range []string{"a", "has space", "bad/name", "管理员"} {
		payload := `{"current_password":"` + password + `","new_username":` + quoteJSON(name) + `,"new_password":"a-very-long-one"}`
		req := httptest.NewRequest(http.MethodPost, "/api/password", strings.NewReader(payload))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("username %q accepted: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestMustChangeSkipsCurrentPassword(t *testing.T) {
	srv, password := newAuthServer(t, "correct-horse") // created with must_change=1
	h := srv.Handler()

	login := postJSON(t, h, "/api/login", `{"username":"admin","password":"`+password+`"}`)
	cookie := login.Result().Cookies()[0]

	// First login: the session was just created with this password, so the new
	// password can be set without repeating it.
	req := httptest.NewRequest(http.MethodPost, "/api/password",
		strings.NewReader(`{"new_password":"a-very-long-one"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first-login password set = %d %s", rec.Code, rec.Body)
	}

	// After that, a credential change requires the current password again.
	login = postJSON(t, h, "/api/login", `{"username":"admin","password":"a-very-long-one"}`)
	cookie = login.Result().Cookies()[0]
	req = httptest.NewRequest(http.MethodPost, "/api/password",
		strings.NewReader(`{"new_password":"another-long-one"}`))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("password change without current = %d, want 401", rec.Code)
	}
}
