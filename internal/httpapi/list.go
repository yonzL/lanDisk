package httpapi

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"land/internal/files"
	"land/internal/perms"
	"land/internal/store"
)

type listResponse struct {
	Share    publicShare   `json:"share"`
	Path     string        `json:"path"`
	Entries  []files.Entry `json:"entries"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Writable bool          `json:"writable"`
}

func (s *Server) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		share, err := s.Store.ShareByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "共享不存在")
			return
		}
		if err != nil {
			s.fail(w, "read share", err)
			return
		}

		policy, err := s.policy(r.Context(), id)
		if err != nil {
			s.fail(w, "load rules", err)
			return
		}
		_, admin := s.session(r)
		visitor := perms.Visitor{Admin: admin}

		relPath := r.URL.Query().Get("path")
		if !policy.CanRead(relPath, visitor) {
			// 404 rather than 403: a hidden directory must not announce itself.
			writeError(w, http.StatusNotFound, "not_found", "路径不存在")
			return
		}

		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}

		// Clamp here and echo the effective values: a client that asks for 1000
		// rows must not be told it got 1000 when it got 500.
		opts := files.ListOptions{
			Page:     clampInt(atoiDefault(r.URL.Query().Get("page"), 1), 1, 1_000_000),
			PageSize: clampInt(atoiDefault(r.URL.Query().Get("page_size"), 100), 1, files.MaxPageSize),
			Sort:     files.SortKey(r.URL.Query().Get("sort")),
			Desc:     r.URL.Query().Get("order") == "desc",
			Filter: func(p string, isDir bool) bool {
				return policy.CanRead(p, visitor)
			},
		}

		cleanRel, _ := perms.Normalize(relPath)

		// Sorting by note needs the notes of the whole directory before the
		// order (and therefore the page) is known.
		if opts.Sort == files.SortNote {
			all, err := root.AllEntries(cleanRel, opts.Filter)
			if err != nil {
				writeListError(w, s, err)
				return
			}
			paths := make([]string, len(all))
			for i, e := range all {
				paths[i] = e.Path
			}
			notes, err := s.Store.NotesFor(r.Context(), id, paths)
			if err != nil {
				s.fail(w, "load notes", err)
				return
			}
			opts.Notes = func(p string) string { return notes[strings.ToLower(p)] }
		}

		entries, total, err := root.List(relPath, opts)
		if err != nil {
			writeListError(w, s, err)
			return
		}

		paths := make([]string, len(entries))
		for i, e := range entries {
			paths[i] = e.Path
		}
		notes, err := s.Store.NotesFor(r.Context(), id, paths)
		if err != nil {
			s.fail(w, "load notes", err)
			return
		}
		files.AttachNotes(entries, notes)

		writeJSON(w, http.StatusOK, listResponse{
			Share:    publicShare{ID: share.ID, Name: share.Name, VirtualPath: share.VirtualPath},
			Path:     cleanRel,
			Entries:  entries,
			Total:    total,
			Page:     opts.Page,
			PageSize: opts.PageSize,
			Writable: policy.CanWrite(cleanRel, visitor),
		})
	}
}

// writeListError maps filesystem errors onto the API's error shape.
func writeListError(w http.ResponseWriter, s *Server, err error) {
	if errors.Is(err, files.ErrInvalidPath) || errors.Is(err, files.ErrNotDir) || os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "not_found", "路径不存在")
		return
	}
	s.fail(w, "list directory", err)
}

func atoiDefault(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
