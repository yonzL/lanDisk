package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

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

// Handler serves the SPA: real files when they exist, index.html for client
// routes. API and preview-asset paths are never answered here — they are
// registered on the same mux and win over this catch-all.
func Handler() http.Handler {
	root := FS()
	fileServer := http.FileServer(http.FS(root))
	index, indexErr := fs.ReadFile(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
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
	if strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/preview-assets/") || strings.HasPrefix(p, "/file-viewer/") {
		return true
	}
	rest := strings.TrimPrefix(p, "/")
	return !strings.Contains(rest, "/") && path.Ext(rest) != ""
}
