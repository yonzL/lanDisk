// Package thumbs renders small previews of images for the file list. Thumbnails
// are produced on demand and cached in memory only: the data directory must stay
// "landisk.exe + landisk.db" (architecture.md "运行期文件").
package thumbs

import (
	"container/list"
	"context"
	"fmt"
	"image"
	"sync"
	"time"
)

// Size is the edge length of a rendered thumbnail, in pixels. The list shows
// 40 CSS px; 96 keeps it crisp on HiDPI screens without much cost.
const Size = 96

// maxEntries / maxBytes bound the cache. A LAN tool with a few hundred photos per
// folder stays far below these; the limits exist so a huge folder cannot grow the
// process without bound.
const (
	maxEntries = 512
	maxBytes   = 64 << 20
)

// Key identifies a rendered thumbnail. ModTime and file size make a stale entry
// impossible: editing the image produces a new key, and the old one ages out.
type Key struct {
	Path    string
	ModUnix int64
	Size    int64
	Px      int
}

type entry struct {
	key  Key
	data []byte
	mime string
}

// Cache is a size-bounded LRU of rendered thumbnails.
type Cache struct {
	mu      sync.Mutex
	items   map[Key]*list.Element
	order   *list.List // front = most recently used
	bytes   int64
	entries int

	// One decode at a time per key: two rows requesting the same image should not
	// both spend CPU on it.
	inflight map[Key]*call
}

type call struct {
	done chan struct{}
	data []byte
	mime string
	err  error
}

// New builds an empty cache.
func New() *Cache {
	return &Cache{
		items:    map[Key]*list.Element{},
		order:    list.New(),
		inflight: map[Key]*call{},
	}
}

// Get returns a cached thumbnail, if present.
func (c *Cache) Get(k Key) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(k)
}

// GetOrRender returns the cached thumbnail or renders it once. Concurrent callers
// for the same key wait on the first render instead of duplicating the work.
func (c *Cache) GetOrRender(ctx context.Context, k Key, render func() ([]byte, string, error)) ([]byte, string, error) {
	if data, mime, ok := c.Get(k); ok {
		return data, mime, nil
	}

	c.mu.Lock()
	if data, mime, ok := c.getLocked(k); ok {
		c.mu.Unlock()
		return data, mime, nil
	}
	if existing, ok := c.inflight[k]; ok {
		c.mu.Unlock()
		select {
		case <-existing.done:
			return existing.data, existing.mime, existing.err
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	pending := &call{done: make(chan struct{})}
	c.inflight[k] = pending
	c.mu.Unlock()

	data, mime, err := render()

	c.mu.Lock()
	pending.data, pending.mime, pending.err = data, mime, err
	delete(c.inflight, k)
	if err == nil {
		c.storeLocked(k, data, mime)
	}
	c.mu.Unlock()
	close(pending.done)
	return data, mime, err
}

// storeLocked inserts an entry, evicting until both bounds hold.
func (c *Cache) storeLocked(k Key, data []byte, mime string) {
	if el, ok := c.items[k]; ok {
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&entry{key: k, data: data, mime: mime})
	c.items[k] = el
	c.bytes += int64(len(data))
	c.entries++

	for c.entries > maxEntries || c.bytes > maxBytes {
		back := c.order.Back()
		if back == nil {
			break
		}
		old := back.Value.(*entry)
		c.order.Remove(back)
		delete(c.items, old.key)
		c.bytes -= int64(len(old.data))
		c.entries--
	}
}

// Stats reports the cache size, for logs and tests.
func (c *Cache) Stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries, c.bytes
}

// Age is how long a rendered thumbnail may be reused; the key already carries the
// mtime, so this only matters for entries whose file vanished and reappeared.
const Age = time.Hour

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

// getLocked is the cache hit path used while the mutex is already held.
func (c *Cache) getLocked(k Key) ([]byte, string, bool) {
	el, ok := c.items[k]
	if !ok {
		return nil, "", false
	}
	c.order.MoveToFront(el)
	e := el.Value.(*entry)
	return e.data, e.mime, true
}
