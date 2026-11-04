// Package files owns the filesystem side of a share: turning a virtual path
// into a safe absolute path, listing directories, and (in later steps) serving,
// writing and moving entries. Every disk access must go through Resolve.
package files

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"land/internal/perms"
	"land/internal/store"
)

// ErrInvalidPath is returned when a relative path escapes its share, or uses a
// reserved name.
var ErrInvalidPath = errors.New("files: invalid path")

// ErrNotDir is returned when a listing target is not a directory.
var ErrNotDir = errors.New("files: not a directory")

// Entry is one row of a directory listing.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Note    string    `json:"note"`
}

// SortKey selects the listing order.
type SortKey string

// Supported sort keys.
const (
	SortName    SortKey = "name"
	SortSize    SortKey = "size"
	SortModTime SortKey = "mod_time"
	SortNote    SortKey = "note"
)

// Notes supplies notes for sorting by the note column. Without it a note sort
// would compare empty strings and silently keep directory order.
type Notes func(path string) string

// ListOptions carries pagination and ordering for a listing.
type ListOptions struct {
	Page     int
	PageSize int
	Sort     SortKey
	Desc     bool

	// Notes, when set, is consulted while sorting (SortNote) so the order is
	// computed from real note text.
	Notes Notes

	// Filter decides whether an entry may appear at all. Permission filtering
	// happens here, before pagination, so page sizes and totals describe what
	// the visitor can actually see (a hidden subdirectory must not leave a gap).
	Filter func(path string, isDir bool) bool
}

// MaxPageSize bounds a single listing response.
const MaxPageSize = 500

// Root is a share's resolved base directory.
type Root struct {
	Dir string // absolute, symlink-resolved
}

// NewRoot validates a share root once, so per-request resolution can compare
// against a stable, fully resolved base.
func NewRoot(dir string) (Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Root{}, fmt.Errorf("共享目录不合法：%w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Root{}, fmt.Errorf("共享目录不可访问：%s", abs)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return Root{}, fmt.Errorf("共享目录不是目录：%s", resolved)
	}
	return Root{Dir: resolved}, nil
}

// Resolve maps relPath to an absolute path inside the share.
//
// Security boundary (architecture.md "路径安全不变量"):
//  1. the relative path is normalised (no "..", no reserved names);
//  2. the joined path stays under the share root, case-insensitively;
//  3. any symlink/junction on the way is resolved and must still land inside the
//     root — a junction pointing elsewhere is rejected until it is registered
//     as a mount (the mount registry lands with the trash step).
func (r Root) Resolve(relPath string) (string, string, error) {
	rel, ok := perms.Normalize(relPath)
	if !ok {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidPath, relPath)
	}

	joined := filepath.Join(r.Dir, filepath.FromSlash(rel))
	if !withinRoot(r.Dir, joined) {
		return "", "", fmt.Errorf("%w: %q escapes the share", ErrInvalidPath, relPath)
	}

	// Resolve the existing prefix so a symlink cannot be used to climb out, and
	// so an NTFS 8.3 alias (LANDPA~1 → .landpart-abc) is caught by the reserved
	// name check below instead of slipping through as an ordinary name.
	resolved := joined
	for {
		next, err := filepath.EvalSymlinks(resolved)
		if err == nil {
			resolved = next
			break
		}
		if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("解析路径失败：%w", err)
		}
		parent := filepath.Dir(resolved)
		if parent == resolved {
			break
		}
		resolved = parent
	}
	if !withinRoot(r.Dir, resolved) {
		return "", "", fmt.Errorf("%w: %q resolves outside the share", ErrInvalidPath, relPath)
	}
	if hasReservedComponent(r.Dir, resolved) {
		return "", "", fmt.Errorf("%w: %q resolves to a reserved name", ErrInvalidPath, relPath)
	}
	return joined, rel, nil
}

// hasReservedComponent reports whether any component of the resolved path
// (relative to the share root) is internal bookkeeping.
func hasReservedComponent(root, resolved string) bool {
	rel, err := filepath.Rel(root, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "." || part == "" {
			continue
		}
		if IsReservedName(part) {
			return true
		}
	}
	return false
}

// List reads one directory, attaches notes, sorts and paginates it.
//
// Filesystem cost is what decides whether a 100k-file directory answers within
// the product's 1-second budget, so the work is staged: ReadDir gives names and
// types, permission filtering and sorting happen on those, and only then are
// the size/mtime of the *page* read (a full stat sweep happens only when the
// caller sorts by a field that requires it).
func (r Root) List(relPath string, opts ListOptions) ([]Entry, int, error) {
	abs, cleanRel, err := r.Resolve(relPath)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, 0, err
	}
	if !info.IsDir() {
		return nil, 0, ErrNotDir
	}

	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, 0, fmt.Errorf("读取目录失败：%w", err)
	}

	rows := make([]Entry, 0, len(dirents))
	for _, de := range dirents {
		name := de.Name()
		if IsReservedName(name) {
			continue
		}
		entryRel := path.Join(cleanRel, name)
		isDir := de.IsDir()
		// A junction inside the share may behave like a directory, but one
		// that points elsewhere must not be listed at all. Only directories can
		// redirect, so files skip the extra syscall.
		if isDir && de.Type()&os.ModeSymlink != 0 {
			if target, err := filepath.EvalSymlinks(filepath.Join(abs, name)); err != nil || !withinRoot(r.Dir, target) {
				continue
			}
		}
		if opts.Filter != nil && !opts.Filter(entryRel, isDir) {
			continue
		}
		rows = append(rows, Entry{Name: name, Path: entryRel, IsDir: isDir})
	}

	if needsStat(opts.Sort) {
		fillStat(abs, rows, 0, len(rows))
	}
	sortEntries(rows, opts)
	total := len(rows)
	page := paginate(rows, opts)
	if !needsStat(opts.Sort) {
		fillStat(abs, page, 0, len(page))
	}
	return page, total, nil
}

// needsStat reports whether the sort key requires size/mtime for every row.
func needsStat(key SortKey) bool {
	switch key {
	case SortSize, SortModTime:
		return true
	}
	return false
}

// fillStat reads size/mtime for rows[from:to], skipping entries that are gone.
func fillStat(dir string, rows []Entry, from, to int) {
	for i := from; i < to; i++ {
		fi, err := statFile(filepath.Join(dir, rows[i].Name))
		if err != nil {
			continue
		}
		rows[i].IsDir = fi.IsDir()
		rows[i].Size = sizeOf(fi)
		rows[i].ModTime = fi.ModTime()
	}
}

// AttachNotes fills Entry.Note from a preloaded map (case-folded keys).
func AttachNotes(entries []Entry, notes map[string]string) {
	for i := range entries {
		entries[i].Note = notes[strings.ToLower(entries[i].Path)]
	}
}

func sizeOf(fi os.FileInfo) int64 {
	if fi.IsDir() {
		return 0
	}
	return fi.Size()
}

func sortEntries(entries []Entry, opts ListOptions) {
	key := opts.Sort
	if key == "" {
		key = SortName
	}
	noteOf := func(e Entry) string {
		if opts.Notes == nil {
			return e.Note
		}
		return opts.Notes(e.Path)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		// Directories first keeps the list scannable regardless of the key.
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		var less bool
		switch key {
		case SortSize:
			less = a.Size < b.Size
		case SortModTime:
			less = a.ModTime.Before(b.ModTime)
		case SortNote:
			less = strings.ToLower(noteOf(a)) < strings.ToLower(noteOf(b))
		default:
			less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if opts.Desc {
			return !less && !equalOn(key, a, b, noteOf)
		}
		return less
	})
}

func equalOn(key SortKey, a, b Entry, noteOf func(Entry) string) bool {
	switch key {
	case SortSize:
		return a.Size == b.Size
	case SortModTime:
		return a.ModTime.Equal(b.ModTime)
	case SortNote:
		return strings.EqualFold(noteOf(a), noteOf(b))
	default:
		return strings.EqualFold(a.Name, b.Name)
	}
}

func paginate(entries []Entry, opts ListOptions) []Entry {
	page := opts.Page
	if page < 1 {
		page = 1
	}
	size := opts.PageSize
	if size < 1 {
		size = 100
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	// Compare before multiplying: page comes from a client and can be huge
	// enough that (page-1)*size overflows into a negative slice bound.
	if page-1 >= (len(entries)+size-1)/size {
		return []Entry{}
	}
	start := (page - 1) * size
	end := start + size
	if end > len(entries) {
		end = len(entries)
	}
	return entries[start:end]
}

// IsReservedName reports whether a directory entry is internal bookkeeping
// (recycle bin, in-flight upload chunks, half-restored files) that must never
// appear in a listing.
func IsReservedName(name string) bool {
	lower := strings.ToLower(strings.TrimRight(name, ". "))
	return lower == ".land-trash" ||
		strings.Contains(lower, ".landpart-") ||
		strings.Contains(lower, ".landrestore-")
}

// withinRoot reports whether target is root or inside it, comparing
// case-insensitively the way NTFS does.
func withinRoot(root, target string) bool {
	root = strings.TrimRight(filepath.Clean(root), string(filepath.Separator))
	target = filepath.Clean(target)
	if strings.EqualFold(root, target) {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix))
}

// RootFor builds a Root for a share, memoised by the caller.
func RootFor(share store.Share) (Root, error) { return NewRoot(share.Root) }

// statFile is the one place listing reads file metadata. Tests swap it to count
// syscalls and prove that listing a page does not stat the whole directory.
var statFile = os.Stat

// AllEntries returns every entry of one directory (no stats, no pagination). It
// exists so a caller that must sort by note can load the notes of the whole
// directory before the order is known.
func (r Root) AllEntries(relPath string, filter func(string, bool) bool) ([]Entry, error) {
	abs, cleanRel, err := r.Resolve(relPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrNotDir
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败：%w", err)
	}
	out := make([]Entry, 0, len(dirents))
	for _, de := range dirents {
		name := de.Name()
		if IsReservedName(name) {
			continue
		}
		entryRel := path.Join(cleanRel, name)
		if filter != nil && !filter(entryRel, de.IsDir()) {
			continue
		}
		out = append(out, Entry{Name: name, Path: entryRel, IsDir: de.IsDir()})
	}
	return out, nil
}

// ResolveTrash resolves a path inside the recycle bin. Only the trash package
// may use it: the normal Resolve deliberately rejects ".land-trash" so no HTTP
// request can reach deleted content.
func (r Root) ResolveTrash(relPath string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(relPath, "\\", "/"), "/")))
	if rel == "." || rel == "" {
		return "", fmt.Errorf("%w: empty trash path", ErrInvalidPath)
	}
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, relPath)
	}
	first := strings.Split(filepath.ToSlash(rel), "/")[0]
	if !strings.EqualFold(strings.TrimRight(first, ". "), ".land-trash") {
		return "", fmt.Errorf("%w: %q is not a trash path", ErrInvalidPath, relPath)
	}
	abs := filepath.Join(r.Dir, rel)
	if !withinRoot(r.Dir, abs) {
		return "", fmt.Errorf("%w: %q escapes the share", ErrInvalidPath, relPath)
	}
	return abs, nil
}
