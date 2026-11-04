package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"land/internal/perms"
	"land/internal/store"
)

type noteRequest struct {
	ShareID int64  `json:"share_id"`
	Path    string `json:"path"`
	Note    string `json:"note"`
}

// handleSetNote writes or clears one note. Only an admin may write notes; every
// visitor sees them in listings (goal-and-requirements "文件备注").
func (s *Server) handleSetNote() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var req noteRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !s.shareExists(w, r, req.ShareID) {
			return
		}
		rel, ok := perms.Normalize(req.Path)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_path", "路径不合法")
			return
		}
		if len(req.Note) > 2000 {
			writeError(w, http.StatusBadRequest, "note_too_long", "备注最多 2000 字")
			return
		}
		if err := s.Store.SetNote(r.Context(), req.ShareID, rel, strings.TrimSpace(req.Note), time.Now().Unix()); err != nil {
			s.fail(w, "write note", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": rel, "note": strings.TrimSpace(req.Note)})
	})
}

// handleGetNote returns one path's note (admin editing convenience).
func (s *Server) handleGetNote() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		shareID := int64(atoiDefault(r.URL.Query().Get("share_id"), 0))
		if !s.shareExists(w, r, shareID) {
			return
		}
		rel, ok := perms.Normalize(r.URL.Query().Get("path"))
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_path", "路径不合法")
			return
		}
		note, found, err := s.Store.NoteFor(r.Context(), shareID, rel)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				note, found = "", false
			} else {
				s.fail(w, "read note", err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": rel, "note": note, "exists": found})
	})
}
