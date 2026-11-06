// Package httpapi wires the HTTP surface: routing, middleware, and the JSON
// encoding rules. Filesystem work belongs to files/upload/trash; this package
// only orchestrates.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"land/internal/auth"
	"land/internal/logs"
	"land/internal/store"
	webui "land/web"
)

// Server holds the dependencies every handler needs.
type Server struct {
	Store    *store.Store
	Version  string
	Logger   *slog.Logger
	Throttle *auth.Throttle
	Logs     *logs.Writer
	IPFilter *ipFilter
	Limiter  *concurrencyLimiter

	mu           sync.RWMutex
	downloadKbps int
	uploadKbps   int

	throttleOnce sync.Once
}

// Handler builds the full route table, including the embedded SPA.
func (s *Server) Handler() http.Handler {
	// Lazily construct the runtime guards so a Server built directly (tests,
	// tools) behaves like the wired app.
	if s.IPFilter == nil {
		s.IPFilter = newIPFilter()
		_ = s.IPFilter.load(context.Background(), s.Store)
	}
	if s.Limiter == nil {
		s.Limiter = newConcurrencyLimiter(64)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth())
	mux.HandleFunc("GET /api/site", s.handleSite())

	mux.HandleFunc("POST /api/login", s.handleLogin())
	mux.HandleFunc("POST /api/logout", s.handleLogout())
	mux.HandleFunc("GET /api/session", s.handleSession())
	mux.HandleFunc("POST /api/password", s.handlePassword())

	mux.HandleFunc("GET /api/shares", s.handlePublicShares())
	mux.HandleFunc("GET /api/shares/{id}/list", s.handleList())
	mux.HandleFunc("GET /api/shares/{id}/search", s.handleSearch())

	mux.HandleFunc("POST /api/shares/{id}/mkdir", s.handleMkdir())
	mux.HandleFunc("POST /api/shares/{id}/rename", s.handleRename())
	mux.HandleFunc("POST /api/shares/{id}/move", s.handleMove())
	mux.HandleFunc("POST /api/shares/{id}/delete", s.handleDelete())

	mux.HandleFunc("GET /api/admin/fs/list", s.handleFSList())
	mux.HandleFunc("POST /api/admin/fs/mkdir", s.handleFSMkdir())

	mux.HandleFunc("GET /api/admin/settings", s.handleGetSettings())
	mux.HandleFunc("PUT /api/admin/settings", s.handlePutSettings())

	mux.HandleFunc("GET /api/admin/ip-rules", s.handleListIPRules())
	mux.HandleFunc("POST /api/admin/ip-rules", s.handleSetIPRule())
	mux.HandleFunc("DELETE /api/admin/ip-rules", s.handleDeleteIPRule())

	mux.HandleFunc("GET /api/admin/logs", s.handleListLogs())
	mux.HandleFunc("GET /api/admin/logs/export", s.handleExportLogs())

	mux.HandleFunc("GET /api/admin/hosts", s.handleListHosts())
	mux.HandleFunc("POST /api/admin/hosts", s.handleSetHost())
	mux.HandleFunc("DELETE /api/admin/hosts/{ip}", s.handleDeleteHost())
	mux.HandleFunc("GET /api/admin/hosts/export", s.handleExportHosts())
	mux.HandleFunc("POST /api/admin/hosts/import", s.handleImportHosts())

	mux.HandleFunc("GET /api/admin/trash", s.handleListTrash())
	mux.HandleFunc("POST /api/admin/trash/{id}/restore", s.handleRestoreTrash())
	mux.HandleFunc("POST /api/admin/trash/clear", s.handlePurgeTrash())

	mux.HandleFunc("POST /api/uploads", s.handleBeginUpload())
	mux.HandleFunc("GET /api/uploads/{id}", s.handleUploadStatus())
	mux.HandleFunc("PUT /api/uploads/{id}/{index}", s.handleUploadChunk())
	mux.HandleFunc("POST /api/uploads/{id}/complete", s.handleUploadComplete())
	mux.HandleFunc("DELETE /api/uploads/{id}", s.handleUploadAbort())
	mux.HandleFunc("GET /api/shares/{id}/thumb", s.handleThumb())
	mux.HandleFunc("GET /api/shares/{id}/content", s.handleContent())
	mux.HandleFunc("GET /api/shares/{id}/raw", s.handleRaw())
	mux.HandleFunc("GET /api/shares/{id}/zip", s.handleZip())

	mux.HandleFunc("GET /api/admin/shares", s.handleAdminListShares())
	mux.HandleFunc("POST /api/admin/shares", s.handleAdminCreateShare())
	mux.HandleFunc("PUT /api/admin/shares/{id}", s.handleAdminUpdateShare())
	mux.HandleFunc("DELETE /api/admin/shares/{id}", s.handleAdminDeleteShare())
	mux.HandleFunc("GET /api/admin/notes", s.handleGetNote())
	mux.HandleFunc("POST /api/admin/notes", s.handleSetNote())

	mux.HandleFunc("GET /api/admin/shares/{id}/rules", s.handleAdminListRules())
	mux.HandleFunc("POST /api/admin/shares/{id}/rules", s.handleAdminSetRule())
	mux.HandleFunc("DELETE /api/admin/shares/{id}/rules", s.handleAdminDeleteRule())

	// Everything under /api that no route claimed answers as JSON, not HTML.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint: "+r.Method+" "+r.URL.Path)
	})

	mux.Handle("/", webui.Handler())

	h := s.ipFilterMiddleware(s.concurrencyMiddleware(mux))
	return recoverMiddleware(s.logger(), s.logMiddleware(h))
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Shares  int    `json:"shares"`
	Assets  int    `json:"preview_assets"`
	Time    int64  `json:"time"`
}

func (s *Server) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shares, err := s.Store.CountShares(r.Context())
		if err != nil {
			s.logger().Error("health: count shares", "error", err)
			writeError(w, http.StatusServiceUnavailable, "db_unavailable", "数据库不可用")
			return
		}
		writeJSON(w, http.StatusOK, healthResponse{
			Status:  "ok",
			Version: s.Version,
			Shares:  shares,
			Time:    time.Now().Unix(),
			Assets:  webui.AssetCount(),
		})
	}
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// recoverMiddleware turns a panicking handler into a 500 instead of a dropped
// connection, and records the panic with the request it belonged to.
func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic serving request", "method", r.Method, "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// apiError is the single error shape on the wire: {code, message}.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent; nothing left to do but log it.
		slog.Default().Error("encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Code: code, Message: message})
}

// nowFunc keeps time injectable in tests that age rows.
var nowFunc = time.Now
