package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"land/internal/auth"
	"land/internal/store"
)

const (
	sessionCookie = "land_session"
	minPassword   = 8
)

type loginRequest struct {
	// Username defaults to "admin" when omitted, so existing clients keep
	// working; the form always sends it explicitly.
	Username string `json:"username"`
	Password string `json:"password"`
}

// sessionResponse is what the interface needs to render the header: who is
// signed in. Sessions do not expire, so there is no deadline to publish.
type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	MustChange    bool   `json:"must_change"`
	Username      string `json:"username,omitempty"`
}

// newSessionResponse echoes the username even when logged out, so the login page
// can prefill a renamed account.
func newSessionResponse(username string, authenticated, mustChange bool) sessionResponse {
	return sessionResponse{Authenticated: authenticated, MustChange: mustChange, Username: username}
}

func (s *Server) handleLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !s.throttle().Allowed(ip) {
			writeError(w, http.StatusTooManyRequests, "locked_out", "尝试次数过多，请一分钟后再试")
			return
		}

		var req loginRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求格式不正确")
			return
		}

		username := strings.TrimSpace(req.Username)
		if username == "" {
			username = store.AdminUsername
		}
		user, err := s.Store.UserByUsername(r.Context(), username)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Unknown user and wrong password answer identically: the login
				// page must not reveal which usernames exist.
				s.throttle().Fail(ip)
				writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或口令不正确")
				return
			}
			s.logger().Error("login: read user", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "内部错误")
			return
		}
		if !auth.CheckPassword(user.PassHash, req.Password) {
			s.throttle().Fail(ip)
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或口令不正确")
			return
		}
		s.throttle().Success(ip)

		token := auth.NewToken()
		now := time.Now()
		if err := s.Store.CreateSession(r.Context(), token, user.ID, now); err != nil {
			s.logger().Error("login: create session", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "内部错误")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			// Persistent cookie: closing the browser must not sign the admin out.
			MaxAge: int(store.SessionCookieMaxAge / time.Second),
		})
		writeJSON(w, http.StatusOK, newSessionResponse(user.Username, true, user.MustChange))
	}
}

func (s *Server) handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token, ok := sessionToken(r); ok {
			if err := s.Store.DeleteSession(r.Context(), token); err != nil {
				s.logger().Error("logout: delete session", "error", err)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		writeJSON(w, http.StatusOK, newSessionResponse("", false, false))
	}
}

func (s *Server) handleSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(r)
		if !ok {
			writeJSON(w, http.StatusOK, newSessionResponse("", false, false))
			return
		}
		user, err := s.Store.SingleAdmin(r.Context())
		if err != nil {
			s.logger().Error("session: read admin", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "内部错误")
			return
		}
		writeJSON(w, http.StatusOK, newSessionResponse(user.Username, true, sess.MustChange))
	}
}

type passwordRequest struct {
	Current     string `json:"current_password"`
	NewUsername string `json:"new_username"`
	New         string `json:"new_password"`
}

func (s *Server) handlePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}

		var req passwordRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求格式不正确")
			return
		}
		if len(req.New) < minPassword {
			writeError(w, http.StatusBadRequest, "weak_password", "新口令至少 8 位")
			return
		}

		user, err := s.Store.SingleAdmin(r.Context())
		if err != nil {
			s.logger().Error("password: read admin", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "内部错误")
			return
		}
		// Right after the first login (or after `reset-password`) the session was
		// just established with the temporary password, so asking for it again is
		// pure friction. Every later change still requires the current password.
		if !sess.MustChange && !auth.CheckPassword(user.PassHash, req.Current) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "当前口令不正确")
			return
		}

		username := user.Username
		if next := strings.TrimSpace(req.NewUsername); next != "" && next != username {
			if problem := validateUsername(next); problem != "" {
				writeError(w, http.StatusBadRequest, "invalid_username", problem)
				return
			}
			username = next
		}

		hash, err := auth.HashPassword(req.New)
		if err != nil {
			s.logger().Error("password: hash", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "内部错误")
			return
		}
		if err := s.Store.SetCredentials(r.Context(), sess.UserID, username, hash, false); err != nil {
			if errors.Is(err, store.ErrConflict) {
				writeError(w, http.StatusConflict, "conflict", err.Error())
				return
			}
			s.logger().Error("password: store", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "内部错误")
			return
		}
		// Sessions were dropped by SetCredentials: answer as logged out and let
		// the interface send the operator to the login page with the new name.
		writeJSON(w, http.StatusOK, newSessionResponse(username, false, false))
	}
}

// validateUsername keeps login names simple: letters, digits, dot, dash and
// underscore, 2–32 characters. Returns "" when acceptable.
func validateUsername(name string) string {
	if len([]rune(name)) < 2 || len([]rune(name)) > 32 {
		return "用户名长度需在 2–32 个字符之间"
	}
	for _, r := range name {
		ok := r == '.' || r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return "用户名只能包含字母、数字、点、横线与下划线"
		}
	}
	return ""
}

// requireAdmin wraps a handler that must run with an admin session. A session
// still holding the bootstrap password is refused: "must_change" is enforced
// here, not just suggested by the interface.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		if sess.MustChange {
			writeError(w, http.StatusForbidden, "must_change", "请先修改初始口令")
			return
		}
		next(w, r)
	}
}

// session resolves the request's session cookie, refreshing its idle timer.
func (s *Server) session(r *http.Request) (store.Session, bool) {
	token, ok := sessionToken(r)
	if !ok {
		return store.Session{}, false
	}
	sess, found, err := s.Store.TouchSession(r.Context(), token, time.Now())
	if err != nil {
		s.logger().Error("session: touch", "error", err)
		return store.Session{}, false
	}
	return sess, found
}

func sessionToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

func (s *Server) throttle() *auth.Throttle {
	s.throttleOnce.Do(func() {
		if s.Throttle == nil {
			s.Throttle = auth.NewThrottle()
		}
	})
	return s.Throttle
}

// clientIP returns the peer address. No proxy sits in front of this server, so
// forwarding headers are deliberately ignored.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return strings.TrimSpace(host)
}
