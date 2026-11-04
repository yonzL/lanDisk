package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"land/internal/perms"
	"land/internal/store"
)

// shareView is what the interface needs: the share plus its rules.
type shareView struct {
	Share store.Share  `json:"share"`
	Rules []store.Rule `json:"rules"`
}

func (s *Server) handleAdminListShares() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		shares, err := s.Store.ListShares(r.Context())
		if err != nil {
			s.fail(w, "list shares", err)
			return
		}
		views := make([]shareView, 0, len(shares))
		for _, sh := range shares {
			rules, err := s.Store.Rules(r.Context(), sh.ID)
			if err != nil {
				s.fail(w, "list rules", err)
				return
			}
			views = append(views, shareView{Share: sh, Rules: rules})
		}
		writeJSON(w, http.StatusOK, views)
	})
}

type shareRequest struct {
	Name        string `json:"name"`
	VirtualPath string `json:"virtual_path"`
	Root        string `json:"root"`
	RootPerm    string `json:"root_perm"`
	Sort        int    `json:"sort"`
}

func (s *Server) handleAdminCreateShare() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var req shareRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, rootPerm, perr := validateShareRequest(req, true)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid_share", perr.Error())
			return
		}
		created, err := s.Store.CreateShare(r.Context(), share, rootPerm)
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		if err != nil {
			s.fail(w, "create share", err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	})
}

func (s *Server) handleAdminUpdateShare() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req shareRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		share, _, perr := validateShareRequest(req, false)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid_share", perr.Error())
			return
		}
		share.ID = id
		if err := s.Store.UpdateShare(r.Context(), share); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "共享不存在")
				return
			}
			if errors.Is(err, store.ErrConflict) {
				writeError(w, http.StatusConflict, "conflict", err.Error())
				return
			}
			s.fail(w, "update share", err)
			return
		}
		if stored, err := s.Store.ShareByID(r.Context(), id); err == nil {
			writeJSON(w, http.StatusOK, stored)
			return
		}
		writeJSON(w, http.StatusOK, share)
	})
}

func (s *Server) handleAdminDeleteShare() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		// Part files belong to upload sessions that this delete cascades away; the
		// sweep only walks rows, so they must be removed before the share goes.
		if uploads, err := s.Store.UploadsByShare(r.Context(), id); err == nil {
			for _, u := range uploads {
				if err := os.Remove(u.PartPath); err != nil && !os.IsNotExist(err) {
					s.logger().Error("删除共享时清理分块文件失败", "path", u.PartPath, "error", err)
				}
			}
		}
		if err := s.Store.DeleteShare(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "共享不存在")
				return
			}
			s.fail(w, "delete share", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})
}

func (s *Server) handleAdminListRules() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok || !s.shareExists(w, r, id) {
			return
		}
		rules, err := s.Store.Rules(r.Context(), id)
		if err != nil {
			s.fail(w, "list rules", err)
			return
		}
		writeJSON(w, http.StatusOK, rules)
	})
}

type ruleRequest struct {
	RelPath string `json:"rel_path"`
	Perm    string `json:"perm"`
}

func (s *Server) handleAdminSetRule() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req ruleRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		rel, okPath := perms.Normalize(req.RelPath)
		if !okPath {
			writeError(w, http.StatusBadRequest, "invalid_path", "路径不合法（不能包含 .. 或保留名）")
			return
		}
		perm := store.Perm(req.Perm)
		if !store.ValidPerm(perm) {
			writeError(w, http.StatusBadRequest, "invalid_perm", "权限只能是 none / r / rw")
			return
		}
		if !s.shareExists(w, r, id) {
			return
		}
		if err := s.Store.SetRule(r.Context(), store.Rule{ShareID: id, RelPath: rel, Perm: perm}); err != nil {
			s.fail(w, "set rule", err)
			return
		}
		writeJSON(w, http.StatusOK, store.Rule{ShareID: id, RelPath: rel, Perm: perm})
	})
}

func (s *Server) handleAdminDeleteRule() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		delPath, okPath := perms.Normalize(r.URL.Query().Get("rel_path"))
		if !okPath {
			writeError(w, http.StatusBadRequest, "invalid_path", "路径不合法")
			return
		}
		if !s.shareExists(w, r, id) {
			return
		}
		if err := s.Store.DeleteRule(r.Context(), id, delPath); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "规则不存在")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_rule", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})
}

// publicShare is the anonymous view of a share: no filesystem paths.
type publicShare struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	VirtualPath string `json:"virtual_path"`
}

// handlePublicShares lists the shares a visitor may see. Anonymous visitors
// only see shares whose root rule grants read access; an admin session sees
// every share (goal-and-requirements: 管理员无条件下读取全部共享).
func (s *Server) handlePublicShares() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, admin := s.session(r)
		visitor := perms.Visitor{Admin: admin}

		shares, err := s.Store.ListShares(r.Context())
		if err != nil {
			s.fail(w, "list shares", err)
			return
		}
		out := make([]publicShare, 0, len(shares))
		for _, sh := range shares {
			policy, err := s.policy(r.Context(), sh.ID)
			if err != nil {
				s.fail(w, "load rules", err)
				return
			}
			if !policy.CanRead("", visitor) {
				continue
			}
			out = append(out, publicShare{ID: sh.ID, Name: sh.Name, VirtualPath: sh.VirtualPath})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// shareExists answers 404 itself when the share is gone, so a stale admin tab
// cannot create orphan rules or read a phantom rule list.
func (s *Server) shareExists(w http.ResponseWriter, r *http.Request, id int64) bool {
	if _, err := s.Store.ShareByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "共享不存在")
			return false
		}
		s.fail(w, "read share", err)
		return false
	}
	return true
}

// policy loads and compiles a share's rules.
func (s *Server) policy(ctx context.Context, shareID int64) (perms.Policy, error) {
	rules, err := s.Store.Rules(ctx, shareID)
	if err != nil {
		return perms.Policy{}, err
	}
	return perms.Compile(rules), nil
}

// validateShareRequest normalises and checks a create/update payload. It
// returns the canonical share and, for creation, the validated root permission
// — so the caller can never pass the pre-validation string by mistake.
func validateShareRequest(req shareRequest, creating bool) (store.Share, store.Perm, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return store.Share{}, "", errors.New("名称不能为空")
	}

	virtual := strings.TrimSpace(req.VirtualPath)
	if !strings.HasPrefix(virtual, "/") {
		return store.Share{}, "", errors.New("虚拟路径必须以 / 开头，例如 /图纸")
	}
	normalized, ok := perms.Normalize(virtual)
	if !ok || normalized == "" {
		return store.Share{}, "", errors.New("虚拟路径不合法")
	}
	// Persist the canonical form so "/a//b", "/a/./b" and "/a/b/" cannot
	// create three rows that route to the same address.
	virtual = "/" + normalized

	share := store.Share{Name: name, VirtualPath: virtual, Sort: req.Sort}
	rootPerm := store.PermNone
	if creating {
		root := strings.TrimSpace(req.Root)
		if root == "" {
			return store.Share{}, "", errors.New("共享目录不能为空")
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return store.Share{}, "", fmt.Errorf("目录路径不合法：%w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return store.Share{}, "", fmt.Errorf("目录不存在或无法访问：%s", abs)
		}
		if !info.IsDir() {
			return store.Share{}, "", errors.New("共享目标必须是目录")
		}
		share.Root = abs

		mode := req.RootPerm
		if mode == "" {
			mode = string(store.PermRead)
		}
		rootPerm = store.Perm(mode)
		if !store.ValidPerm(rootPerm) {
			return store.Share{}, "", errors.New("根目录权限只能是 none / r / rw")
		}
	}
	return share, rootPerm, nil
}

// decodeJSON reads a small JSON body, answering 400 itself when it cannot.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return false
	}
	return true
}

// pathID parses the {id} path segment, answering 400 itself when it is bad.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "共享 ID 不合法")
		return 0, false
	}
	return id, true
}

// fail logs an internal error and answers 500 without leaking details.
func (s *Server) fail(w http.ResponseWriter, where string, err error) {
	s.logger().Error(where, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "内部错误")
}
