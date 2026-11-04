package files

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsDirectoriesAndEscapes(t *testing.T) {
	root := newShareTree(t)
	if _, _, err := root.Open("图纸"); err == nil {
		t.Fatal("Open accepted a directory")
	}
	if _, _, err := root.Open("../secret"); err == nil {
		t.Fatal("Open accepted an escaping path")
	}
	f, info, err := root.Open("说明.txt")
	if err != nil {
		t.Fatalf("Open file: %v", err)
	}
	defer f.Close()
	if info.Size() != 5 {
		t.Fatalf("size = %d, want 5", info.Size())
	}
}

func TestWriteZipIncludesTreeAndSkipsReserved(t *testing.T) {
	root := newShareTree(t)
	var buf bytes.Buffer
	if err := root.WriteZip(&buf, "图纸", ZipOptions{}); err != nil {
		t.Fatalf("zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["图纸/2024/"] || !names["图纸/a.dwg"] {
		t.Fatalf("zip entries = %v", names)
	}
	for _, f := range zr.File {
		if f.Name == ".landpart-abc" || filepath.Base(f.Name) == ".landpart-abc" {
			t.Fatalf("reserved file leaked into the archive: %v", names)
		}
	}
}

func TestWriteZipSkipsFilteredSubtrees(t *testing.T) {
	root := newShareTree(t)
	var buf bytes.Buffer
	err := root.WriteZip(&buf, "", ZipOptions{
		Filter: func(p string, isDir bool) bool { return p != "图纸" },
	})
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "图纸/" || len(f.Name) > 3 && f.Name[:3] == "图纸/" {
			t.Fatalf("filtered directory leaked into the archive: %s", f.Name)
		}
	}
}

func TestZipSingleFile(t *testing.T) {
	root := newShareTree(t)
	var buf bytes.Buffer
	if err := root.WriteZip(&buf, "说明.txt", ZipOptions{}); err != nil {
		t.Fatalf("zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "说明.txt" {
		t.Fatalf("entries = %v", zr.File)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "hello" {
		t.Fatalf("content = %q", body)
	}
}

func TestContentDispositionHandlesChineseNames(t *testing.T) {
	hdr := ContentDisposition("图纸 2024.pdf")
	if !bytes.Contains([]byte(hdr), []byte("filename*=UTF-8''")) {
		t.Fatalf("header lacks RFC 5987 form: %s", hdr)
	}
	if bytes.Contains([]byte(hdr), []byte("图纸")) {
		t.Fatalf("header contains raw non-ASCII: %s", hdr)
	}
}

func TestOpenFileKeepsContentAfterStat(t *testing.T) {
	root := newShareTree(t)
	f, _, err := root.Open("说明.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	if len(body) != 5 || string(body) != "hello" {
		t.Fatalf("read %q", body)
	}
	_ = os.Remove("")
}
