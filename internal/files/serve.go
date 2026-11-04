package files

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Open returns an open file handle for a single entry. Directories are refused:
// they are served through Zip instead.
func (r Root) Open(relPath string) (*os.File, os.FileInfo, error) {
	abs, _, err := r.Resolve(relPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, ErrNotDir
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("打开文件失败：%w", err)
	}
	return f, info, nil
}

// ZipOptions bounds a streamed archive.
type ZipOptions struct {
	// Filter decides whether an entry may be included (permission visibility).
	Filter func(relPath string, isDir bool) bool
}

// WriteZip streams relPath (a directory, or a single file) to w as a zip
// archive. Nothing is buffered on disk: entries are written as they are read,
// so a 10 GB folder costs one file handle at a time.
func (r Root) WriteZip(w io.Writer, relPath string, opts ZipOptions) error {
	abs, cleanRel, err := r.Resolve(relPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}

	zw := zip.NewWriter(w)
	defer zw.Close()

	if !info.IsDir() {
		return addZipFile(zw, abs, filepath.Base(abs), info)
	}

	base := path.Base(cleanRel)
	if cleanRel == "" {
		base = filepath.Base(r.Dir)
	}
	return filepath.Walk(abs, func(p string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == abs {
			return nil
		}
		name := fi.Name()
		if IsReservedName(name) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		entryRel := path.Join(cleanRel, rel)
		if opts.Filter != nil && !opts.Filter(entryRel, fi.IsDir()) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		zipName := path.Join(base, rel)
		if fi.IsDir() {
			_, err := zw.Create(zipName + "/")
			return err
		}
		return addZipFile(zw, p, zipName, fi)
	})
}

func addZipFile(zw *zip.Writer, abs, name string, info os.FileInfo) error {
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	src, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer src.Close()
	_, err = io.Copy(dst, src)
	return err
}

// ContentDisposition builds an RFC 5987 attachment header that survives
// non-ASCII file names.
func ContentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	var b strings.Builder
	b.WriteString(`attachment; filename="`)
	b.WriteString(ascii)
	b.WriteString(`"; filename*=UTF-8''`)
	b.WriteString(percentEncode(name))
	return b.String()
}

func percentEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
