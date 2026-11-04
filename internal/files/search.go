package files

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SearchOptions bounds a recursive name search. Every field exists because an
// unbounded walk of a 100k-file share would outlive the HTTP request.
type SearchOptions struct {
	Query    string
	Limit    int
	MaxDepth int
	Timeout  time.Duration
	// Filter drops entries the visitor may not see (and their subtrees).
	Filter func(relPath string, isDir bool) bool
}

// SearchResult is what a search produced, plus why it stopped. Every bound
// that cut the walk short reports itself: a short list without a flag would look
// like "nothing matched" instead of "I stopped looking".
type SearchResult struct {
	Entries      []Entry
	Truncated    bool // hit Limit
	TimedOut     bool // hit Timeout
	DepthLimited bool // a subtree was pruned by MaxDepth
	Visited      int
}

// Defaults for SearchOptions.
const (
	DefaultSearchLimit   = 200
	DefaultSearchDepth   = 12
	DefaultSearchTimeout = 3 * time.Second
	MaxSearchLimit       = 1000
	MaxSearchDepth       = 32
)

// Search walks relPath recursively matching entry names (case-insensitive
// substring). It never follows a directory that resolves outside the share, and
// it always reports which bound stopped it instead of silently returning a
// short list.
func (r Root) Search(ctx context.Context, relPath string, opts SearchOptions) (SearchResult, error) {
	abs, cleanRel, err := r.Resolve(relPath)
	if err != nil {
		return SearchResult{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return SearchResult{}, err
	}
	if !info.IsDir() {
		return SearchResult{}, ErrNotDir
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	depthLimit := opts.MaxDepth
	if depthLimit <= 0 {
		depthLimit = DefaultSearchDepth
	}
	if depthLimit > MaxSearchDepth {
		depthLimit = MaxSearchDepth
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSearchTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	needle := strings.ToLower(strings.TrimSpace(opts.Query))
	if needle == "" {
		return SearchResult{}, nil
	}

	res := SearchResult{Entries: []Entry{}}

	var walk func(dir string, relDir string, depth int) bool // returns "keep going"
	walk = func(dir, relDir string, depth int) bool {
		if depth > depthLimit {
			res.DepthLimited = true
			return true
		}
		select {
		case <-ctx.Done():
			res.TimedOut = true
			return false
		default:
		}

		dirents, err := os.ReadDir(dir)
		if err != nil {
			return true
		}
		for _, de := range dirents {
			select {
			case <-ctx.Done():
				res.TimedOut = true
				return false
			default:
			}

			name := de.Name()
			if IsReservedName(name) {
				continue
			}
			entryRel := path.Join(relDir, name)
			isDir := de.IsDir()
			childAbs := filepath.Join(dir, name)

			if isDir && de.Type()&os.ModeSymlink != 0 {
				if target, err := filepath.EvalSymlinks(childAbs); err != nil || !withinRoot(r.Dir, target) {
					continue
				}
			}
			if opts.Filter != nil && !opts.Filter(entryRel, isDir) {
				continue
			}
			res.Visited++

			if strings.Contains(strings.ToLower(name), needle) {
				entry := Entry{Name: name, Path: entryRel, IsDir: isDir}
				if fi, err := statFile(childAbs); err == nil {
					entry.Size = sizeOf(fi)
					entry.ModTime = fi.ModTime()
				}
				res.Entries = append(res.Entries, entry)
				if len(res.Entries) >= limit {
					res.Truncated = true
					return false
				}
			}
			if isDir {
				if !walk(childAbs, entryRel, depth+1) {
					return false
				}
			}
		}
		return true
	}

	walk(abs, cleanRel, 1)
	return res, nil
}
