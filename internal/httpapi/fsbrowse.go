package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"land/internal/files"
)

// fsEntry is one directory in the server-side picker. Files are never returned:
// the picker only chooses where a share lives.
type fsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type fsListResponse struct {
	Path    string    `json:"path"`    // current directory ("" = drive list)
	Parent  string    `json:"parent"`  // "" when at the drive list
	Entries []fsEntry `json:"entries"` // directories only
}

// handleFSList backs the directory picker: whole-machine browsing for an admin.
// It returns directories only, and never file contents.
func (s *Server) handleFSList() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.URL.Query().Get("path"))
		if raw == "" {
			writeJSON(w, http.StatusOK, fsListResponse{Entries: driveList()})
			return
		}

		abs, err := filepath.Abs(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_path", "路径不合法")
			return
		}
		info, err := os.Stat(abs)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "目录不存在或无法访问")
			return
		}
		if !info.IsDir() {
			writeError(w, http.StatusBadRequest, "not_a_directory", "目标不是目录")
			return
		}

		dirents, err := os.ReadDir(abs)
		if err != nil {
			writeError(w, http.StatusForbidden, "denied", "无法读取该目录（权限不足）")
			return
		}
		entries := make([]fsEntry, 0, len(dirents))
		for _, de := range dirents {
			if !de.IsDir() {
				continue
			}
			name := de.Name()
			if strings.HasPrefix(name, "$") {
				// Recycler / system junctions: browsing them is never useful and
				// following them can be slow.
				continue
			}
			entries = append(entries, fsEntry{Name: name, Path: filepath.Join(abs, name), IsDir: true})
		}
		sort.Slice(entries, func(i, j int) bool {
			return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
		})

		parent := filepath.Dir(abs)
		if samePath(parent, abs) {
			parent = ""
		}
		writeJSON(w, http.StatusOK, fsListResponse{Path: abs, Parent: parent, Entries: entries})
	})
}

// handleFSMkdir creates a directory from inside the picker.
func (s *Server) handleFSMkdir() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		name, err := files.ValidateName(req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_name", "目录名不合法")
			return
		}
		abs, err := filepath.Abs(strings.TrimSpace(req.Path))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_path", "路径不合法")
			return
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			writeError(w, http.StatusNotFound, "not_found", "父目录不存在")
			return
		}
		target := filepath.Join(abs, name)
		if _, err := os.Stat(target); err == nil {
			writeError(w, http.StatusConflict, "exists", "同名目录已存在")
			return
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			if errors.Is(err, os.ErrPermission) {
				writeError(w, http.StatusForbidden, "denied", "权限不足，无法创建目录")
				return
			}
			s.fail(w, "mkdir for picker", err)
			return
		}
		writeJSON(w, http.StatusOK, fsEntry{Name: name, Path: target, IsDir: true})
	})
}

// driveList enumerates local drives. On non-Windows systems the filesystem root
// is the only entry.
func driveList() []fsEntry {
	var out []fsEntry
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + ":\\"
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			out = append(out, fsEntry{Name: root, Path: root, IsDir: true})
		}
	}
	if len(out) == 0 {
		out = append(out, fsEntry{Name: "/", Path: "/", IsDir: true})
	}
	return out
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
