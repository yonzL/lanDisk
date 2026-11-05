// Package thumbs renders small previews of images for the file list.
//
// Rendered thumbnails live on disk, in a sub-directory of the system temp
// directory: the program directory stays "lanDisk.exe + landisk.db", and nothing
// is held in the process heap beyond the bytes of one response. A cache is only
// ever an optimisation here — a miss or a broken cache directory costs a decode.
package thumbs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Size is the edge length of a rendered thumbnail, in pixels. The list shows
// 36 CSS px; 96 keeps it crisp on HiDPI screens without much cost.
const Size = 96

// Disk cache bounds. Windows also cleans the temp directory on its own, so these
// are guards rather than a lifecycle.
const (
	DiskMaxBytes = 512 << 20
	DiskMaxAge   = 7 * 24 * time.Hour
	// pruneEvery triggers a size/age sweep after this many writes.
	pruneEvery = 64
)

// Key identifies a rendered thumbnail. ModTime and file size make a stale entry
// impossible: editing the image produces a new key, and the old one ages out.
type Key struct {
	Path    string
	ModUnix int64
	Size    int64
	Px      int
}

// fileName is the deterministic cache filename for a key: the same file version
// maps to the same file, across restarts and across processes.
func (k Key) fileName() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", k.Path, k.ModUnix, k.Size, k.Px)))
	return hex.EncodeToString(sum[:16])
}

// Disk is the on-disk thumbnail cache.
type Disk struct {
	dir string

	mu       sync.Mutex
	writes   int
	inflight map[Key]*call
}

type call struct {
	done chan struct{}
	data []byte
	mime string
	err  error
}

// NewDisk prepares the cache directory (creating it, pruning stale entries). An
// error means the directory is unusable; callers can still render without caching.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("thumbs: create cache dir: %w", err)
	}
	d := &Disk{dir: dir, inflight: map[Key]*call{}}
	d.prune()
	return d, nil
}

// Dir reports the cache directory (used in logs).
func (d *Disk) Dir() string { return d.dir }

// Size reports how many entries and bytes the cache currently holds.
func (d *Disk) Size() (files int, bytes int64) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		files++
		bytes += info.Size()
	}
	return files, bytes
}

// GetOrRender returns a cached thumbnail or renders it once. Concurrent callers
// for the same key wait on the first render instead of duplicating the work.
func (d *Disk) GetOrRender(ctx context.Context, k Key, render func() ([]byte, string, error)) ([]byte, string, error) {
	if data, mime, ok := d.load(k); ok {
		return data, mime, nil
	}

	d.mu.Lock()
	if existing, ok := d.inflight[k]; ok {
		d.mu.Unlock()
		select {
		case <-existing.done:
			return existing.data, existing.mime, existing.err
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	pending := &call{done: make(chan struct{})}
	d.inflight[k] = pending
	d.mu.Unlock()

	data, mime, err := render()

	d.mu.Lock()
	pending.data, pending.mime, pending.err = data, mime, err
	delete(d.inflight, k)
	d.writes++
	shouldPrune := d.writes%pruneEvery == 0
	d.mu.Unlock()

	if err == nil && len(data) > 0 {
		d.store(k, data, mime)
	}
	if shouldPrune {
		d.prune()
	}
	close(pending.done)
	return data, mime, err
}

// load reads a cached thumbnail from disk.
func (d *Disk) load(k Key) ([]byte, string, bool) {
	base := filepath.Join(d.dir, k.fileName())
	for _, ext := range []string{"jpg", "png"} {
		path := base + "." + ext
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		mime := "image/jpeg"
		if ext == "png" {
			mime = "image/png"
		}
		return data, mime, true
	}
	return nil, "", false
}

// store writes a thumbnail atomically (temp file + rename) so a concurrent reader
// never sees a partial image.
func (d *Disk) store(k Key, data []byte, mime string) {
	ext := "jpg"
	if mime == "image/png" {
		ext = "png"
	}
	final := filepath.Join(d.dir, k.fileName()+"."+ext)
	tmp, err := os.CreateTemp(d.dir, "tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return
	}
	_ = os.Rename(name, final)
}

// prune drops entries older than DiskMaxAge and, if the directory is still over
// DiskMaxBytes, the oldest files until it fits.
func (d *Disk) prune() {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		path string
		mod  time.Time
		size int64
	}
	files := make([]fileInfo, 0, len(entries))
	var total int64
	cutoff := time.Now().Add(-DiskMaxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		path := filepath.Join(d.dir, e.Name())
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
			continue
		}
		files = append(files, fileInfo{path: path, mod: info.ModTime(), size: info.Size()})
		total += info.Size()
	}
	if total <= DiskMaxBytes {
		return
	}
	// Oldest first.
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			if files[j].mod.Before(files[i].mod) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
	for _, f := range files {
		if total <= DiskMaxBytes {
			return
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
		}
	}
}

// ErrNotImage is returned when the file is not a supported image format.
var ErrNotImage = fmt.Errorf("thumbs: not a supported image")

// IsImageName reports whether the filename looks like an image we can decode.
func IsImageName(name string) bool {
	switch ext(name) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp":
		return true
	}
	return false
}

func ext(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '.':
			return lower(name[i:])
		case '/', '\\':
			return ""
		}
	}
	return ""
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// fit returns the largest size that fits inside max while keeping the aspect
// ratio, and never upscales.
func fit(src image.Rectangle, max int) (int, int) {
	w, h := src.Dx(), src.Dy()
	if w <= 0 || h <= 0 {
		return max, max
	}
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, maxInt(1, h*max/w)
	}
	return maxInt(1, w*max/h), max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
