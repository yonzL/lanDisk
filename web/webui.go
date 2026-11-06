package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The MIME table for these extensions comes from the OS registry on Windows and
// differs between machines; .mjs in particular is missing on some, which makes a
// module worker fail to load. Register what the viewer needs unconditionally.
func init() {
	for ext, typ := range map[string]string{
		".mjs":   "text/javascript; charset=utf-8",
		".js":    "text/javascript; charset=utf-8",
		".wasm":  "application/wasm",
		".woff2": "font/woff2",
		".woff":  "font/woff",
		".ttf":   "font/ttf",
		".json":  "application/json",
		".map":   "application/json",
		".svg":   "image/svg+xml",
		".webp":  "image/webp",
		".avif":  "image/avif",
		".mp4":   "video/mp4",
		".webm":  "video/webm",
		".mkv":   "video/x-matroska",
		".m3u8":  "application/vnd.apple.mpegurl",
		".ts":    "video/mp2t",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

//go:embed all:dist
var embedded embed.FS

// notBuiltPage is served when the binary was compiled without running the
// frontend build. It keeps `go build` usable from a fresh clone without
// committing build output.
const notBuiltPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>lan disk</title></head>
<body style="font-family:'Microsoft YaHei',sans-serif;padding:40px">
<h1>lan disk</h1>
<p>前端产物未构建。运行 <code>bun install &amp;&amp; bun run build</code> 后重新编译 lanDisk.exe。</p>
</body></html>`

// FS returns the built frontend rooted at the dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// The embed directive guarantees dist exists at compile time.
		panic("webui: embedded dist missing: " + err.Error())
	}
	return sub
}

// viewerAlias maps a root-relative viewer asset path onto the canonical
// /file-viewer/ prefix. The viewer resolves some workers against the document
// base; without this alias a build whose viewer bundle regresses to "/vendor/…"
// fails in the field with "Setting up fake worker failed" (seen on 10.7.39.x with
// an older release). Serving the same embedded bytes under both prefixes makes
// that class of mistake harmless.
func viewerAlias(clean string) (string, bool) {
	for _, prefix := range []string{"/vendor/", "/wasm/"} {
		if strings.HasPrefix(clean, prefix) {
			return "/file-viewer" + clean, true
		}
	}
	return "", false
}

// AssetCount reports how many files are embedded under /file-viewer, so the health
// endpoint can show whether a deployment actually carries the preview runtime.
func AssetCount() int {
	root := FS()
	count := 0
	_ = fs.WalkDir(root, "file-viewer", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

// Handler serves the SPA: real files when they exist, index.html for client
// routes. API and preview-asset paths are never answered here — they are
// registered on the same mux and win over this catch-all.
func Handler() http.Handler {
	root := FS()
	fileServer := http.FileServer(http.FS(root))
	index, indexErr := fs.ReadFile(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		// Root-relative viewer assets are served from the canonical prefix (see
		// viewerAlias) before anything else looks at the path.
		if target, ok := viewerAlias(clean); ok {
			if f, err := root.Open(strings.TrimPrefix(target, "/")); err == nil {
				info, statErr := f.Stat()
				f.Close()
				if statErr == nil && !info.IsDir() {
					w.Header().Set("Cache-Control", "public, max-age=86400")
					alias := r.Clone(r.Context())
					alias.URL.Path = target
					fileServer.ServeHTTP(w, alias)
					return
				}
			}
		}
		if f, err := root.Open(strings.TrimPrefix(clean, "/")); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && !info.IsDir() {
				if isHashedAsset(clean) {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// A missing static asset must be a 404, never the SPA shell: a browser
		// holding a stale index would otherwise get HTML where it expects
		// JavaScript and die on "Unexpected token '<'". SPA deep links
		// (/s/1/preview/a.pdf) are NOT static surfaces and must still fall
		// through to the shell.
		if isStaticSurface(clean) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if indexErr != nil {
			w.Write([]byte(notBuiltPage))
			return
		}
		w.Write(index)
	})
}

// isHashedAsset reports whether the path points at a Vite-emitted asset whose
// name carries a content hash, which is safe to cache forever.
func isHashedAsset(p string) bool {
	return strings.HasPrefix(p, "/assets/")
}

// isStaticSurface reports whether a 404 (rather than the SPA shell) is the
// right answer for a path that matched no embedded file: build output
// directories, and root-level files such as /favicon.ico. Anything deeper is a
// client route and gets index.html.
func isStaticSurface(p string) bool {
	// Viewer runtime assets live under /file-viewer/. The bare /vendor/ and
	// /wasm/ prefixes exist only to make a mis-resolved asset path fail loudly
	// (404) instead of being answered with the SPA shell — that mismatch is what
	// turned a wrong PDF worker URL into "Failed to fetch dynamically imported
	// module ... (text/html)" in the field.
	for _, prefix := range []string{"/assets/", "/preview-assets/", "/file-viewer/", "/vendor/", "/wasm/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	rest := strings.TrimPrefix(p, "/")
	return !strings.Contains(rest, "/") && path.Ext(rest) != ""
}
