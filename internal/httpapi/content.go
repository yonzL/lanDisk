package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"land/internal/files"
)

// contentMIME fills the gaps in the platform MIME table for media the browser can
// stream. Getting this wrong is what made large videos download before playing:
// with application/octet-stream the browser refuses to play inline and buffers the
// whole file first.
var contentMIME = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".ogv":  "video/ogg",
	".avi":  "video/x-msvideo",
	".wmv":  "video/x-ms-wmv",
	".flv":  "video/x-flv",
	".ts":   "video/mp2t",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".pdf":  "application/pdf",
}

// mimeFor returns the content type to serve a file with inline.
func mimeFor(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if v, ok := contentMIME[ext]; ok {
		return v
	}
	if v := mime.TypeByExtension(ext); v != "" {
		return v
	}
	return "application/octet-stream"
}

// handleContent serves a file inline with Range support, so <video> and PDF.js can
// stream it instead of downloading the whole thing first. The download endpoint
// (/raw) stays as an attachment; this one is for the previewer.
func (s *Server) handleContent() http.HandlerFunc {
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
		f, info, err := root.Open(relPath)
		if err != nil {
			if errors.Is(err, files.ErrInvalidPath) || errors.Is(err, files.ErrNotDir) || os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "not_found", "路径不存在")
				return
			}
			s.fail(w, "open file", err)
			return
		}
		defer f.Close()

		// Inline, typed, range-capable: the three things a <video> or PDF.js needs
		// to stream. ServeContent adds Accept-Ranges and honours Range/If-Range.
		w.Header().Set("Content-Type", mimeFor(info.Name()))
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}
