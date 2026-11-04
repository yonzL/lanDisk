package httpapi

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"land/internal/logs"
)

// logMiddleware records every API call that changes or reveals something. It
// derives the action from the matched route pattern, so no handler has to
// remember to log, and it never blocks the response (the writer's queue drops
// instead of stalling).
func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		if s.Logs == nil {
			return
		}
		action := actionFor(r.Method, r.Pattern)
		if action == "" {
			return
		}
		shareID := int64(0)
		if v := r.PathValue("id"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				shareID = n
			}
		}
		target := readableTarget(r.URL)
		result := "ok"
		detail := ""
		if rec.status >= 400 {
			result = "error"
			detail = strconv.Itoa(rec.status)
		}
		s.Logs.Record(logs.Entry{
			Action:  action,
			IP:      clientIP(r),
			ShareID: shareID,
			Path:    target,
			Result:  result,
			Detail:  detail,
		})
	})
}

// readableTarget renders a request target for humans: path plus query values
// decoded, so a log line shows 图纸/2024.dwg instead of %E5%9B%BE%E7%BA%B8…
// Keys are sorted to keep exports comparable between rows.
func readableTarget(u *url.URL) string {
	if u.RawQuery == "" {
		return u.Path
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return u.Path + "?" + u.RawQuery
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, key+"="+value)
		}
	}
	return u.Path + "?" + strings.Join(parts, "&")
}

// actionFor maps a matched route to the audit action name. Unlisted routes
// (health, static files) are not logged.
func actionFor(method, pattern string) string {
	switch pattern {
	case "POST /api/login":
		return "login"
	case "POST /api/logout":
		return "logout"
	case "POST /api/password":
		if method == http.MethodPost {
			return "password_change"
		}
	case "GET /api/shares/{id}/list":
		return "browse"
	case "GET /api/shares/{id}/search":
		return "search"
	case "GET /api/shares/{id}/raw":
		return "download"
	case "GET /api/shares/{id}/zip":
		return "download_zip"
	case "POST /api/uploads":
		return "upload_begin"
	case "PUT /api/uploads/{id}/{index}":
		return "upload_chunk"
	case "POST /api/uploads/{id}/complete":
		return "upload"
	case "DELETE /api/uploads/{id}":
		return "upload_abort"
	case "POST /api/shares/{id}/mkdir":
		return "mkdir"
	case "POST /api/shares/{id}/rename":
		return "rename"
	case "POST /api/shares/{id}/move":
		return "move"
	case "POST /api/shares/{id}/delete":
		return "delete"
	case "POST /api/admin/trash/{id}/restore":
		return "trash_restore"
	case "POST /api/admin/trash/clear":
		return "trash_clear"
	case "POST /api/admin/notes":
		return "note"
	}
	// Any other admin mutation is a configuration change worth recording.
	if len(pattern) > 0 && pattern[len(pattern)-1] != '/' {
		if method != http.MethodGet && strings.HasPrefix(pattern, "/api/admin/") {
			return "admin_change"
		}
	}
	return ""
}

// statusRecorder captures the status code without hiding the underlying writer.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}
