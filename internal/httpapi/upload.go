package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"land/internal/files"
	"land/internal/perms"
	"land/internal/store"
	"land/internal/upload"
)

type beginUploadRequest struct {
	ShareID int64  `json:"share_id"`
	Path    string `json:"path"` // destination directory, relative to the share
	Name    string `json:"name"` // final file name
	Size    int64  `json:"size"`
}

type uploadResponse struct {
	ID       string  `json:"id"`
	Chunk    int64   `json:"chunk_size"`
	Size     int64   `json:"size"`
	Received []int64 `json:"received"`
	Name     string  `json:"name"`
}

func (s *Server) handleBeginUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req beginUploadRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, policy, visitor, ok := s.shareContext(w, r, req.ShareID)
		if !ok {
			return
		}
		dirRel, cleanOK := perms.Normalize(req.Path)
		if !cleanOK {
			writeError(w, http.StatusBadRequest, "invalid_path", "路径不合法")
			return
		}
		if !policy.CanWrite(dirRel, visitor) {
			writeError(w, http.StatusForbidden, "read_only", "该目录不允许上传")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}

		sess, err := upload.Begin(s.Store, root, share.ID, dirRel, req.Name, req.Size)
		if err != nil {
			switch {
			case errors.Is(err, upload.ErrBadTarget):
				writeError(w, http.StatusBadRequest, "invalid_name", "文件名不合法")
			case errors.Is(err, files.ErrInvalidPath), errors.Is(err, files.ErrNotDir), os.IsNotExist(err):
				writeError(w, http.StatusNotFound, "not_found", "目标目录不存在")
			default:
				s.fail(w, "begin upload", err)
			}
			return
		}
		writeJSON(w, http.StatusCreated, uploadResponse{
			ID:       sess.ID,
			Chunk:    upload.ChunkSize,
			Size:     sess.Row.Size,
			Received: sess.Received(),
			Name:     sess.Name,
		})
	}
}

// loadSession resolves an upload session and re-checks that the caller may still
// write to its destination.
func (s *Server) loadSession(w http.ResponseWriter, r *http.Request) (*upload.Session, bool) {
	id := r.PathValue("id")
	row, err := s.Store.UploadByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "上传会话不存在或已过期")
		return nil, false
	}
	if err != nil {
		s.fail(w, "read upload", err)
		return nil, false
	}
	share, policy, visitor, ok := s.shareContext(w, r, row.ShareID)
	if !ok {
		return nil, false
	}
	dirRel := ""
	if idx := lastSlash(row.RelPath); idx >= 0 {
		dirRel = row.RelPath[:idx]
	}
	if !policy.CanWrite(dirRel, visitor) {
		writeError(w, http.StatusForbidden, "read_only", "该目录不允许上传")
		return nil, false
	}
	root, err := files.RootFor(share)
	if err != nil {
		s.fail(w, "resolve share root", err)
		return nil, false
	}
	dirAbs, _, err := root.Resolve(dirRel)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "目标目录不存在")
		return nil, false
	}
	return &upload.Session{
		ID:      row.ID,
		Store:   s.Store,
		Row:     row,
		Dir:     dirAbs,
		Name:    baseName(row.RelPath),
		PartAbs: row.PartPath,
	}, true
}

func lastSlash(p string) int {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return i
		}
	}
	return -1
}

func baseName(p string) string {
	if i := lastSlash(p); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (s *Server) handleUploadStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.loadSession(w, r)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, uploadResponse{
			ID:       sess.ID,
			Chunk:    sess.Row.Chunk,
			Size:     sess.Row.Size,
			Received: sess.Received(),
			Name:     sess.Name,
		})
	}
}

func (s *Server) handleUploadChunk() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.loadSession(w, r)
		if !ok {
			return
		}
		index, err := strconv.ParseInt(r.PathValue("index"), 10, 64)
		if err != nil || index < 0 {
			writeError(w, http.StatusBadRequest, "bad_index", "分块序号不合法")
			return
		}
		maxChunks := (sess.Row.Size + sess.Row.Chunk - 1) / sess.Row.Chunk
		if index >= maxChunks {
			writeError(w, http.StatusBadRequest, "bad_index", "分块序号超出范围")
			return
		}
		expected := sess.Row.Chunk
		if remaining := sess.Row.Size - index*sess.Row.Chunk; remaining < expected {
			expected = remaining
		}
		if expected < 0 {
			expected = 0
		}

		body := http.MaxBytesReader(w, newThrottledBody(r.Body, s.rateFor(false)), expected)
		data, err := io.ReadAll(body)
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "chunk_too_large", "分块过大")
			return
		}
		if int64(len(data)) != expected {
			writeError(w, http.StatusBadRequest, "chunk_size_mismatch", "分块大小与该序号不符")
			return
		}
		if err := sess.WriteChunk(index, data); err != nil {
			s.fail(w, "write chunk", err)
			return
		}
		if err := sess.MarkChunk(index); err != nil {
			s.fail(w, "record chunk", err)
			return
		}
		writeJSON(w, http.StatusOK, uploadResponse{
			ID:       sess.ID,
			Chunk:    sess.Row.Chunk,
			Size:     sess.Row.Size,
			Received: sess.Received(),
			Name:     sess.Name,
		})
	}
}

func (s *Server) handleUploadComplete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.loadSession(w, r)
		if !ok {
			return
		}
		if err := sess.Complete(); err != nil {
			var incomplete upload.ErrIncomplete
			if errors.As(err, &incomplete) {
				writeError(w, http.StatusBadRequest, "incomplete", incomplete.Error())
				return
			}
			// Anything else (fsync, rename, a locked destination) would leak
			// server paths: log it and answer generically.
			s.fail(w, "complete upload", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": sess.Row.RelPath, "name": sess.Name})
	}
}

func (s *Server) handleUploadAbort() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.loadSession(w, r)
		if !ok {
			return
		}
		if err := sess.Abort(); err != nil {
			s.fail(w, "abort upload", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "aborted"})
	}
}

// shareContext resolves a share by id (from the body or path) together with its
// policy and visitor. It answers the error itself.
func (s *Server) shareContext(w http.ResponseWriter, r *http.Request, shareID int64) (store.Share, perms.Policy, perms.Visitor, bool) {
	if shareID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "共享 ID 不合法")
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	share, err := s.Store.ShareByID(r.Context(), shareID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "共享不存在")
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	if err != nil {
		s.fail(w, "read share", err)
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	policy, err := s.policy(r.Context(), shareID)
	if err != nil {
		s.fail(w, "load rules", err)
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	_, admin := s.session(r)
	return share, policy, perms.Visitor{Admin: admin}, true
}
