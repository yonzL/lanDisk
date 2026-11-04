package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path"

	"land/internal/files"
	"land/internal/perms"
	"land/internal/store"
)

// shareRequestContext resolves the share + visitor + permission policy shared
// by the download endpoints. It writes the error response itself.
func (s *Server) shareRequestContext(w http.ResponseWriter, r *http.Request) (store.Share, perms.Policy, perms.Visitor, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	share, err := s.Store.ShareByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "共享不存在")
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	if err != nil {
		s.fail(w, "read share", err)
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	policy, err := s.policy(r.Context(), id)
	if err != nil {
		s.fail(w, "load rules", err)
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	_, admin := s.session(r)
	return share, policy, perms.Visitor{Admin: admin}, true
}

// handleRaw serves one file with Range support.
func (s *Server) handleRaw() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		share, policy, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		relPath := r.URL.Query().Get("path")
		if !policy.CanRead(relPath, visitor) {
			writeError(w, http.StatusNotFound, "not_found", "路径不存在")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		f, info, err := root.Open(relPath)
		if err != nil {
			if errors.Is(err, files.ErrInvalidPath) || errors.Is(err, files.ErrNotDir) || os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "not_found", "路径不存在")
				return
			}
			s.fail(w, "open file", err)
			return
		}
		defer f.Close()

		// ServeContent handles Range, If-Modified-Since and Content-Length.
		w.Header().Set("Content-Disposition", files.ContentDisposition(path.Base(info.Name())))
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(newThrottleWriter(w, s.rateFor(true)), r, info.Name(), info.ModTime(), f)
	}
}

// handleZip streams a directory (or single file) as an archive.
func (s *Server) handleZip() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		share, policy, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		relPath := r.URL.Query().Get("path")
		if !policy.CanRead(relPath, visitor) {
			writeError(w, http.StatusNotFound, "not_found", "路径不存在")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		cleanRel, _ := perms.Normalize(relPath)
		zipName := path.Base(cleanRel)
		if cleanRel == "" {
			zipName = share.Name
		}
		zipName += ".zip"

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", files.ContentDisposition(zipName))
		// The archive is generated on the fly; no Content-Length is possible.
		w.WriteHeader(http.StatusOK)

		err = root.WriteZip(w, relPath, files.ZipOptions{
			Filter: func(p string, isDir bool) bool { return policy.CanRead(p, visitor) },
		})
		if err != nil {
			// Headers are gone; the client sees a truncated (broken) archive.
			// Logging is the only honest signal left.
			s.logger().Error("zip stream failed", "path", cleanRel, "error", err)
		}
	}
}
