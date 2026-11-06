package httpapi

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"land/internal/files"
	"land/internal/perms"
	"land/internal/store"
	webui "land/web"
)

// The legacy view is a server-rendered file browser for IE11, where the SPA cannot
// run at all (Vue 3 needs ES modules and Proxy; the previewer needs Workers/WASM).
// It deliberately uses no JavaScript: plain HTML tables, links and forms, so the
// machines that cannot have a modern browser still get browse / download / upload.

// isLegacyIE reports whether the request comes from Internet Explorer.
func isLegacyIE(r *http.Request) bool {
	ua := r.UserAgent()
	return strings.Contains(ua, "Trident/7.0") || strings.Contains(ua, "MSIE ")
}

// wantsModern lets an operator force the SPA from a legacy browser (or a proxy
// health check) with ?modern=1.
func wantsModern(r *http.Request) bool {
	return r.URL.Query().Get("modern") == "1"
}

// legacyHandler serves the SPA for modern browsers and the plain HTML view for IE.
func (s *Server) legacyHandler() http.Handler {
	spa := s.webHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLegacyIE(r) && !wantsModern(r) {
			s.serveLegacy(w, r)
			return
		}
		spa.ServeHTTP(w, r)
	})
}

// serveLegacy maps the SPA routes (and the explicit /lite/ routes) onto the
// server-rendered views.
func (s *Server) serveLegacy(w http.ResponseWriter, r *http.Request) {
	p := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))

	// Form posts from the legacy pages.
	if r.Method == http.MethodPost {
		if strings.HasPrefix(p, "/lite/s/") {
			s.serveLiteAPI(w, r)
			return
		}
		http.Error(w, "请使用现代浏览器执行该操作", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "请使用现代浏览器执行该操作", http.StatusMethodNotAllowed)
		return
	}

	switch {
	case p == "/":
		s.legacyRoot(w, r)
	case strings.HasPrefix(p, "/s/"), strings.HasPrefix(p, "/lite/s/"):
		rest := strings.TrimPrefix(strings.TrimPrefix(p, "/lite"), "/s/")
		segs := strings.SplitN(rest, "/", 2)
		id, err := parseID(segs[0])
		if err != nil {
			s.legacyError(w, r, "共享不存在", http.StatusNotFound)
			return
		}
		rel := ""
		if len(segs) == 2 {
			rel = strings.TrimPrefix(segs[1], "/")
			// The SPA's viewer route cannot work here: show the containing
			// directory so the file can be downloaded.
			if strings.HasPrefix(rel, "preview/") {
				rel = strings.TrimPrefix(rel, "preview/")
				if idx := strings.LastIndex(rel, "/"); idx >= 0 {
					rel = rel[:idx]
				} else {
					rel = ""
				}
			}
		}
		s.legacyListing(w, r, id, rel)
	default:
		s.legacyRoot(w, r)
	}
}

// serveLiteAPI handles the two forms the legacy view posts to.
func (s *Server) serveLiteAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/upload"):
		s.legacyUpload(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/mkdir"):
		s.legacyMkdir(w, r)
	default:
		s.legacyError(w, r, "不支持的操作", http.StatusNotFound)
	}
}

func parseID(s string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil || id <= 0 {
		return 0, errors.New("bad id")
	}
	return id, nil
}

// --- rendering -------------------------------------------------------------

var legacyPage = template.Must(template.New("legacy").Funcs(template.FuncMap{
	"size": humanSize,
	"urlq": func(s string) string { return template.URLQueryEscaper(s) },
}).Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>{{.Title}} · lan disk</title>
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<style>
body{font-family:"Microsoft YaHei",sans-serif;margin:0;background:#f6f7f9;color:#303133;font-size:14px}
header{background:#fff;border-bottom:1px solid #e4e7ed;padding:10px 16px;font-size:16px;font-weight:600}
main{max-width:1100px;margin:0 auto;padding:16px}
.crumb{background:#e2e6e9;padding:6px 10px;margin-bottom:10px}
.crumb a{color:#337ab7;text-decoration:none}
table{width:100%;border-collapse:collapse;background:#fff}
th,td{text-align:left;padding:8px 10px;border-bottom:1px solid #ebeef5;vertical-align:middle}
th{color:#333;font-weight:600}
td.n{color:#909399}
a.file{color:#337ab7;text-decoration:none} a.file:hover{text-decoration:underline}
.box{background:#fff;border:1px solid #e4e7ed;padding:12px 14px;margin-top:14px}
.box h3{margin:0 0 8px;font-size:14px}
input[type=text],input[type=file]{font-size:13px;padding:3px}
input[type=submit]{font-size:13px;padding:4px 12px;background:#409eff;color:#fff;border:0;cursor:pointer}
.msg{padding:8px 10px;margin-bottom:10px;border-left:3px solid #409eff;background:#fff}
.err{border-left-color:#f56c6c;color:#c0392b}
footer{margin:18px 0;color:#909399;font-size:12px;text-align:center}
</style></head>
<body>
<header>lan disk · 兼容模式</header>
<main>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
{{if .Error}}<div class="msg err">{{.Error}}</div>{{end}}
<div class="crumb">{{range $i, $c := .Crumbs}}{{if $i}} / {{end}}<a href="{{$c.Href}}">{{$c.Name}}</a>{{end}}</div>
{{if .Shares}}
<table>
<tr><th>共享目录</th><th>挂载路径</th></tr>
{{range .Shares}}<tr><td><a class="file" href="/lite/s/{{.ID}}">{{.Name}}</a></td><td class="n">{{.VirtualPath}}</td></tr>{{end}}
</table>
{{else if .Files}}
<table>
<tr><th>文件名</th><th>备注</th><th>大小</th><th>修改时间</th></tr>
{{range .Files}}<tr>
<td>{{if .IsDir}}<a class="file" href="{{.Href}}">{{.Name}}</a>{{else}}<a class="file" href="{{.Download}}">{{.Name}}</a>{{end}}</td>
<td class="n">{{.Note}}</td>
<td class="n">{{if .IsDir}}—{{else}}{{size .Size}}{{end}}</td>
<td class="n">{{.ModTime}}</td>
</tr>{{end}}
</table>
{{else}}
<div class="box">这个目录是空的。</div>
{{end}}

{{if .CanUpload}}
<div class="box">
<h3>上传文件</h3>
<form method="post" enctype="multipart/form-data" action="{{.UploadAction}}">
<input type="hidden" name="path" value="{{.Path}}">
<input type="file" name="file" required>
<input type="submit" value="上传">
</form>
<h3 style="margin-top:14px">新建文件夹</h3>
<form method="post" action="{{.MkdirAction}}">
<input type="hidden" name="path" value="{{.Path}}">
<input type="text" name="name" placeholder="文件夹名称" required>
<input type="submit" value="创建">
</form>
</div>
{{end}}

<div class="box" style="color:#909399">
本机浏览器（Internet Explorer）不支持在线预览与完整界面；需要预览请用 Chrome / Edge 打开同一地址。
</div>
<footer>lan disk · {{.Version}}</footer>
</main></body></html>`))

type legacyCrumb struct {
	Name string
	Href string
}

type legacyShare struct {
	ID          int64
	Name        string
	VirtualPath string
}

type legacyFile struct {
	Name     string
	IsDir    bool
	Note     string
	Size     int64
	ModTime  string
	Href     string
	Download string
}

type legacyPageData struct {
	Title        string
	Version      string
	Message      string
	Error        string
	Crumbs       []legacyCrumb
	Shares       []legacyShare
	Files        []legacyFile
	Path         string
	CanUpload    bool
	UploadAction string
	MkdirAction  string
}

func (s *Server) renderLegacy(w http.ResponseWriter, status int, data legacyPageData) {
	data.Version = s.Version
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := legacyPage.Execute(w, data); err != nil {
		s.logger().Error("render legacy page", "error", err)
	}
}

func (s *Server) legacyError(w http.ResponseWriter, r *http.Request, message string, status int) {
	s.renderLegacy(w, status, legacyPageData{
		Title:   "出错了",
		Error:   message,
		Crumbs:  []legacyCrumb{{Name: "根目录", Href: "/"}},
		Message: "",
	})
}

// legacyRoot lists the shares a visitor may see.
func (s *Server) legacyRoot(w http.ResponseWriter, r *http.Request) {
	shares, err := s.Store.ListShares(r.Context())
	if err != nil {
		s.legacyError(w, r, "读取共享目录失败", http.StatusInternalServerError)
		return
	}
	data := legacyPageData{Title: "根目录", Crumbs: []legacyCrumb{{Name: "根目录", Href: "/"}}}
	_, admin := s.session(r)
	for _, sh := range shares {
		policy, err := s.policy(r.Context(), sh.ID)
		if err != nil {
			continue
		}
		if !policy.CanRead("", perms.Visitor{Admin: admin}) {
			continue
		}
		data.Shares = append(data.Shares, legacyShare{ID: sh.ID, Name: sh.Name, VirtualPath: sh.VirtualPath})
	}
	s.renderLegacy(w, http.StatusOK, data)
}

// legacyListing renders one directory of a share.
func (s *Server) legacyListing(w http.ResponseWriter, r *http.Request, shareID int64, relPath string) {
	share, policy, visitor, ok := s.legacyShareContext(r, shareID)
	if !ok {
		s.legacyError(w, r, "共享不存在", http.StatusNotFound)
		return
	}
	cleanRel, valid := perms.Normalize(relPath)
	if !valid || !policy.CanRead(cleanRel, visitor) {
		s.legacyError(w, r, "目录不存在或无权访问", http.StatusNotFound)
		return
	}
	root, err := files.RootFor(share)
	if err != nil {
		s.legacyError(w, r, "共享目录不可访问", http.StatusInternalServerError)
		return
	}
	entries, _, err := root.List(cleanRel, files.ListOptions{
		Page:     1,
		PageSize: files.MaxPageSize,
		Sort:     files.SortName,
		Filter: func(p string, isDir bool) bool {
			return policy.CanRead(p, visitor)
		},
	})
	if err != nil {
		s.legacyError(w, r, "读取目录失败", http.StatusNotFound)
		return
	}

	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	notes, _ := s.Store.NotesFor(r.Context(), shareID, paths)
	files.AttachNotes(entries, notes)

	data := legacyPageData{
		Title:        share.Name,
		Path:         cleanRel,
		CanUpload:    policy.CanWrite(cleanRel, visitor),
		UploadAction: fmt.Sprintf("/lite/s/%d/upload", shareID),
		MkdirAction:  fmt.Sprintf("/lite/s/%d/mkdir", shareID),
		Crumbs:       legacyCrumbs(shareID, share.Name, cleanRel),
	}
	for _, e := range entries {
		item := legacyFile{
			Name:    e.Name,
			IsDir:   e.IsDir,
			Note:    e.Note,
			Size:    e.Size,
			ModTime: e.ModTime.Format("2006-01-02 15:04:05"),
		}
		if e.IsDir {
			item.Href = fmt.Sprintf("/lite/s/%d/%s", shareID, escapePath(e.Path))
		} else {
			item.Download = fmt.Sprintf("/api/shares/%d/raw?path=%s", shareID, template.URLQueryEscaper(e.Path))
		}
		data.Files = append(data.Files, item)
	}
	s.renderLegacy(w, http.StatusOK, data)
}

// legacyCrumbs builds 根目录 / 共享 / 子目录 … links.
func legacyCrumbs(shareID int64, shareName, rel string) []legacyCrumb {
	crumbs := []legacyCrumb{
		{Name: "根目录", Href: "/"},
		{Name: shareName, Href: "/"},
	}
	segs := []string{}
	if rel != "" {
		segs = strings.Split(rel, "/")
	}
	crumbs[1].Href = fmt.Sprintf("/lite/s/%d", shareID)
	for i, seg := range segs {
		crumbs = append(crumbs, legacyCrumb{
			Name: seg,
			Href: fmt.Sprintf("/lite/s/%d/%s", shareID, escapePath(strings.Join(segs[:i+1], "/"))),
		})
	}
	return crumbs
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = template.URLQueryEscaper(s)
	}
	return strings.Join(segs, "/")
}

// legacyShareContext resolves a share and its policy for a request that expects
// HTML rather than JSON.
func (s *Server) legacyShareContext(r *http.Request, shareID int64) (store.Share, perms.Policy, perms.Visitor, bool) {
	share, err := s.Store.ShareByID(r.Context(), shareID)
	if err != nil {
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	policy, err := s.policy(r.Context(), shareID)
	if err != nil {
		return store.Share{}, perms.Policy{}, perms.Visitor{}, false
	}
	_, admin := s.session(r)
	return share, policy, perms.Visitor{Admin: admin}, true
}

// legacyUpload streams a multipart upload straight into the target directory. IE11
// has no chunked upload, so the body is copied once; a partly written file is
// renamed only after the copy succeeded.
func (s *Server) legacyUpload(w http.ResponseWriter, r *http.Request) {
	shareID, err := parseLiteID(r.URL.Path)
	if err != nil {
		s.legacyError(w, r, "共享不存在", http.StatusNotFound)
		return
	}
	share, policy, visitor, ok := s.legacyShareContext(r, shareID)
	if !ok {
		s.legacyError(w, r, "共享不存在", http.StatusNotFound)
		return
	}
	root, err := files.RootFor(share)
	if err != nil {
		s.legacyError(w, r, "共享目录不可访问", http.StatusInternalServerError)
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		s.legacyError(w, r, "上传格式不正确", http.StatusBadRequest)
		return
	}
	relDir := ""
	uploaded := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.legacyError(w, r, "上传失败："+err.Error(), http.StatusBadRequest)
			return
		}
		switch part.FormName() {
		case "path":
			buf, _ := io.ReadAll(io.LimitReader(part, 4096))
			if clean, ok := perms.Normalize(string(buf)); ok {
				relDir = clean
			}
			part.Close()
		case "file":
			name := path.Base(part.FileName())
			if !policy.CanWrite(relDir, visitor) {
				s.legacyError(w, r, "该目录不允许上传", http.StatusForbidden)
				return
			}
			if _, err := files.ValidateName(name); err != nil {
				s.legacyError(w, r, "文件名不合法", http.StatusBadRequest)
				return
			}
			dirAbs, _, err := root.Resolve(relDir)
			if err != nil {
				s.legacyError(w, r, "目标目录不存在", http.StatusNotFound)
				return
			}
			tmp, err := os.CreateTemp(dirAbs, "upload-*")
			if err != nil {
				s.legacyError(w, r, "无法写入目标目录", http.StatusInternalServerError)
				return
			}
			tmpName := tmp.Name()
			if _, err := io.Copy(tmp, part); err != nil {
				tmp.Close()
				os.Remove(tmpName)
				s.legacyError(w, r, "上传中断："+err.Error(), http.StatusInternalServerError)
				return
			}
			if err := tmp.Close(); err != nil {
				os.Remove(tmpName)
				s.legacyError(w, r, "写入失败", http.StatusInternalServerError)
				return
			}
			if err := os.Rename(tmpName, filepath.Join(dirAbs, name)); err != nil {
				os.Remove(tmpName)
				s.legacyError(w, r, "保存失败："+err.Error(), http.StatusInternalServerError)
				return
			}
			uploaded++
		}
	}
	if uploaded == 0 {
		s.legacyError(w, r, "没有收到文件", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/lite/s/%d/%s?uploaded=1", shareID, escapePath(relDir)), http.StatusSeeOther)
}

// legacyMkdir creates a folder from the legacy form.
func (s *Server) legacyMkdir(w http.ResponseWriter, r *http.Request) {
	shareID, err := parseLiteID(r.URL.Path)
	if err != nil {
		s.legacyError(w, r, "共享不存在", http.StatusNotFound)
		return
	}
	share, policy, visitor, ok := s.legacyShareContext(r, shareID)
	if !ok {
		s.legacyError(w, r, "共享不存在", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.legacyError(w, r, "表单格式不正确", http.StatusBadRequest)
		return
	}
	relDir, okDir := perms.Normalize(r.PostFormValue("path"))
	if !okDir {
		s.legacyError(w, r, "目录不合法", http.StatusBadRequest)
		return
	}
	if !policy.CanWrite(relDir, visitor) {
		s.legacyError(w, r, "该目录不允许写入", http.StatusForbidden)
		return
	}
	root, err := files.RootFor(share)
	if err != nil {
		s.legacyError(w, r, "共享目录不可访问", http.StatusInternalServerError)
		return
	}
	if _, err := root.Mkdir(relDir, r.PostFormValue("name")); err != nil {
		s.legacyError(w, r, "创建失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/lite/s/%d/%s?created=1", shareID, escapePath(relDir)), http.StatusSeeOther)
}

// parseLiteID pulls the share id out of /lite/s/{id}/upload.
func parseLiteID(p string) (int64, error) {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(segs) < 3 || segs[1] != "s" {
		return 0, errors.New("bad lite path")
	}
	return parseID(segs[2])
}

// humanSize renders a size for the legacy table.
func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 10 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// webHandler is the embedded SPA handler; kept behind a method so the legacy
// dispatcher and the router share exactly one instance.
func (s *Server) webHandler() http.Handler {
	if s.spa == nil {
		s.spa = webui.Handler()
	}
	return s.spa
}
