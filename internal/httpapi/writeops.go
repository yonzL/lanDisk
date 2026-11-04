package httpapi

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"land/internal/files"
	"land/internal/perms"
	"land/internal/store"
)

type mkdirRequest struct {
	Path string `json:"path"` // parent directory
	Name string `json:"name"`
}

type renameRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type moveRequest struct {
	Path string `json:"path"`
	To   string `json:"to"` // destination directory
}

// handleMkdir creates a directory. Anonymous visitors may do this in a directory
// they can write to (goal: 匿名在有写权限的目录里上传/新建文件夹).
func (s *Server) handleMkdir() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mkdirRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, policy, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		dirRel, okPath := perms.Normalize(req.Path)
		if !okPath {
			writeError(w, http.StatusBadRequest, "invalid_path", "路径不合法")
			return
		}
		if !policy.CanWrite(dirRel, visitor) {
			writeError(w, http.StatusForbidden, "read_only", "该目录不允许写入")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		created, err := root.Mkdir(dirRel, req.Name)
		if err != nil {
			writeOpError(w, s, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": created})
	}
}

// handleRename renames one entry and migrates its notes.
func (s *Server) handleRename() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req renameRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, _, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		if !visitor.Admin {
			writeError(w, http.StatusForbidden, "admin_only", "只有管理员可以重命名")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		oldRel, _ := perms.Normalize(req.Path)
		newRel, err := root.Rename(oldRel, req.Name)
		if err != nil {
			writeOpError(w, s, err)
			return
		}
		s.migrateNotes(w, r, share, oldRel, newRel)
	}
}

// handleMove moves one entry to another directory of the same share.
func (s *Server) handleMove() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req moveRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, _, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		if !visitor.Admin {
			writeError(w, http.StatusForbidden, "admin_only", "只有管理员可以移动")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		oldRel, _ := perms.Normalize(req.Path)
		newRel, err := root.Move(oldRel, req.To)
		if err != nil {
			writeOpError(w, s, err)
			return
		}
		s.migrateNotes(w, r, share, oldRel, newRel)
	}
}

// migrateNotes carries a path's notes to its new location and answers success.
// The filesystem move already happened, so a note failure is logged rather than
// failing the request — the file is where the user asked for it.
func (s *Server) migrateNotes(w http.ResponseWriter, r *http.Request, share store.Share, oldRel, newRel string) {
	if oldRel != newRel {
		if err := s.Store.MoveNotes(r.Context(), share.ID, oldRel, newRel); err != nil {
			s.logger().Error("备注迁移失败", "from", oldRel, "to", newRel, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": newRel})
}

// writeOpError maps filesystem errors onto the API's error shape.
func writeOpError(w http.ResponseWriter, s *Server, err error) {
	switch {
	case errors.Is(err, files.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "invalid_name", "名称或路径不合法")
	case errors.Is(err, files.ErrExists):
		writeError(w, http.StatusConflict, "exists", "同名文件或目录已存在")
	case errors.Is(err, files.ErrNotDir):
		writeError(w, http.StatusBadRequest, "not_a_directory", "目标不是目录")
	case os.IsNotExist(err):
		writeError(w, http.StatusNotFound, "not_found", "路径不存在")
	case strings.Contains(err.Error(), "Access is denied"):
		writeError(w, http.StatusForbidden, "denied", "文件被占用或权限不足")
	default:
		s.fail(w, "filesystem operation", err)
	}
}
