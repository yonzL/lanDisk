package httpapi

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"land/internal/files"
	"land/internal/store"
	"land/internal/trash"
)

type deleteRequest struct {
	Path string `json:"path"`
}

// handleDelete moves an entry into the recycle bin (admin only).
func (s *Server) handleDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req deleteRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, _, visitor, ok := s.shareRequestContext(w, r)
		if !ok {
			return
		}
		if !visitor.Admin {
			writeError(w, http.StatusForbidden, "admin_only", "只有管理员可以删除")
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		entry, err := trash.Delete(r.Context(), s.Store, root, share.ID, req.Path, clientIP(r), nowFunc())
		if err != nil {
			if errors.Is(err, files.ErrInvalidPath) {
				writeError(w, http.StatusBadRequest, "invalid_path", "不能删除共享根目录或保留目录")
				return
			}
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "not_found", "路径不存在")
				return
			}
			s.fail(w, "delete", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "trashed",
			"id":     entry.ID,
			"origin": entry.OrigRelPath,
		})
	}
}

type trashItem struct {
	ID        int64  `json:"id"`
	ShareID   int64  `json:"share_id"`
	Origin    string `json:"origin"`
	DeletedAt int64  `json:"deleted_at"`
	IP        string `json:"ip"`
}

// handleListTrash lists recycle-bin entries (admin only).
func (s *Server) handleListTrash() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		shareID := int64(atoiDefault(r.URL.Query().Get("share_id"), 0))
		entries, err := s.Store.ListTrash(r.Context(), shareID)
		if err != nil {
			s.fail(w, "list trash", err)
			return
		}
		items := make([]trashItem, len(entries))
		for i, e := range entries {
			items[i] = trashItem{ID: e.ID, ShareID: e.ShareID, Origin: e.OrigRelPath, DeletedAt: e.DeletedAt.Unix(), IP: e.IP}
		}
		writeJSON(w, http.StatusOK, items)
	})
}

// handleRestoreTrash restores one entry; ?force=1 allows overwriting.
func (s *Server) handleRestoreTrash() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "bad_id", "回收站记录 ID 不合法")
			return
		}
		entry, err := s.Store.TrashByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "回收站记录不存在")
			return
		}
		if err != nil {
			s.fail(w, "read trash entry", err)
			return
		}
		share, err := s.Store.ShareByID(r.Context(), entry.ShareID)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "共享已被删除，无法恢复")
			return
		}
		if err != nil {
			s.fail(w, "read share", err)
			return
		}
		root, err := files.RootFor(share)
		if err != nil {
			s.fail(w, "resolve share root", err)
			return
		}
		force := r.URL.Query().Get("force") == "1"
		restored, err := trash.Restore(r.Context(), s.Store, root, id, force)
		if err != nil {
			if errors.Is(err, trash.ErrTargetExists) {
				writeError(w, http.StatusConflict, "target_exists", err.Error())
				return
			}
			s.fail(w, "restore", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": restored})
	})
}

// handlePurgeTrash empties the recycle bin for one share (or all of them).
func (s *Server) handlePurgeTrash() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		shareID := int64(atoiDefault(r.URL.Query().Get("share_id"), 0))
		shares, err := s.Store.ListShares(r.Context())
		if err != nil {
			s.fail(w, "list shares", err)
			return
		}
		roots := map[int64]files.Root{}
		for _, sh := range shares {
			if shareID > 0 && sh.ID != shareID {
				continue
			}
			root, err := files.RootFor(sh)
			if err != nil {
				continue
			}
			roots[sh.ID] = root
		}
		removed, err := trash.Empty(r.Context(), s.Store, roots, shareID)
		if err != nil {
			s.fail(w, "purge trash", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"removed": removed})
	})
}
