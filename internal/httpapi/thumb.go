package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"land/internal/files"
	"land/internal/thumbs"
)

// decodeSlots bounds concurrent image decoding; a folder of 200 photos would
// otherwise pin every core while scrolling.
const decodeSlots = 4

var decodeSem = make(chan struct{}, decodeSlots)

// thumbCache is the server-wide disk cache. It is initialised by app.New with a
// directory under the system temp path; tests that build a Server directly get a
// lazily created one on first use.
var (
	thumbCacheOnce sync.Once
	thumbCache     *thumbs.Disk
)

// ThumbCacheDir is the cache location used when app.New does not override it.
// The system temp directory keeps the program directory at exe + db.
func ThumbCacheDir() string {
	if dir := os.Getenv("LAND_THUMB_CACHE"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "lanDisk-thumbs")
}

// SetThumbCache installs the cache built at startup (app.New calls this).
func SetThumbCache(cache *thumbs.Disk) {
	thumbCacheOnce.Do(func() { thumbCache = cache })
}

func (s *Server) thumbStore() *thumbs.Disk {
	thumbCacheOnce.Do(func() {
		cache, err := thumbs.NewDisk(ThumbCacheDir())
		if err != nil {
			cache, _ = thumbs.NewDisk(os.TempDir())
		}
		thumbCache = cache
	})
	return thumbCache
}

func (s *Server) handleThumb() http.HandlerFunc {
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
		abs, cleanRel, err := root.Resolve(relPath)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "路径不存在")
			return
		}
		if !thumbs.IsImageName(path.Base(cleanRel)) {
			writeError(w, http.StatusNotFound, "not_an_image", "不是图片文件")
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			writeError(w, http.StatusNotFound, "not_found", "路径不存在")
			return
		}

		etag := etagFor(info.ModTime().Unix(), info.Size(), thumbs.Size)
		if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		key := thumbs.Key{Path: abs, ModUnix: info.ModTime().Unix(), Size: info.Size(), Px: thumbs.Size}
		data, mime, err := s.thumbStore().GetOrRender(r.Context(), key, func() ([]byte, string, error) {
			decodeSem <- struct{}{}
			defer func() { <-decodeSem }()
			return thumbs.Render(abs, thumbs.Size)
		})
		if err != nil {
			if errors.Is(err, thumbs.ErrNotImage) {
				writeError(w, http.StatusUnsupportedMediaType, "not_an_image", "无法解码该图片")
				return
			}
			if r.Context().Err() != nil {
				return // client went away while decoding
			}
			s.fail(w, "render thumbnail", err)
			return
		}

		w.Header().Set("Content-Type", mime)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	}
}

// etagFor ties a cached response to the exact file version and target size.
func etagFor(modUnix, size int64, px int) string {
	return `"` + strconv.FormatInt(modUnix, 36) + "-" + strconv.FormatInt(size, 36) + "-" + strconv.Itoa(px) + `"`
}
