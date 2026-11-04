package httpapi

import (
	"errors"
	"os"
	"strings"

	"net/http"

	"land/internal/files"
	"land/internal/perms"
)

type searchResponse struct {
	Path         string        `json:"path"`
	Query        string        `json:"query"`
	Entries      []files.Entry `json:"entries"`
	Truncated    bool          `json:"truncated"`
	TimedOut     bool          `json:"timed_out"`
	DepthLimited bool          `json:"depth_limited"`
	Visited      int           `json:"visited"`
}

// handleSearch finds entries by name under the current directory.
func (s *Server) handleSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		share, policy, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		relPath := r.URL.Query().Get("path")
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			writeError(w, http.StatusBadRequest, "empty_query", "请提供搜索关键词")
			return
		}
		if len(query) > 100 {
			writeError(w, http.StatusBadRequest, "query_too_long", "关键词过长")
			return
		}
		if !policy.CanRead(relPath, visitor) {
			writeError(w, http.StatusNotFound, "not_found", "路径不存在")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}

		res, err := root.Search(r.Context(), relPath, files.SearchOptions{
			Query: query,
			Filter: func(p string, isDir bool) bool {
				return policy.CanRead(p, visitor)
			},
		})
		if err != nil {
			if errors.Is(err, files.ErrInvalidPath) || errors.Is(err, files.ErrNotDir) || os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "not_found", "路径不存在")
				return
			}
			s.fail(w, "search", err)
			return
		}

		paths := make([]string, len(res.Entries))
		for i, e := range res.Entries {
			paths[i] = e.Path
		}
		notes, err := s.Store.NotesFor(r.Context(), share.ID, paths)
		if err != nil {
			s.fail(w, "load notes", err)
			return
		}
		files.AttachNotes(res.Entries, notes)

		cleanRel, _ := perms.Normalize(relPath)
		writeJSON(w, http.StatusOK, searchResponse{
			Path:         cleanRel,
			Query:        query,
			Entries:      res.Entries,
			Truncated:    res.Truncated,
			TimedOut:     res.TimedOut,
			DepthLimited: res.DepthLimited,
			Visited:      res.Visited,
		})
	}
}
