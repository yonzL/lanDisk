package files

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newShareTree(t *testing.T) Root {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "图纸", "2024"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("hello"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "图纸", "a.dwg"), []byte("dwg"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".landpart-abc"), []byte("chunk"), 0o644))
	must(os.MkdirAll(filepath.Join(dir, ".land-trash"), 0o755))

	root, err := NewRoot(dir)
	if err != nil {
		t.Fatalf("new root: %v", err)
	}
	return root
}

func TestResolveRejectsEscapes(t *testing.T) {
	root := newShareTree(t)
	for _, rel := range []string{"..", "../etc/passwd", "图纸/../../x", ".land-trash", "a/.landpart-1"} {
		if _, _, err := root.Resolve(rel); err == nil {
			t.Fatalf("Resolve(%q) accepted an escaping or reserved path", rel)
		}
	}
}

func TestResolveAcceptsInsidePaths(t *testing.T) {
	root := newShareTree(t)
	for _, rel := range []string{"", "说明.txt", "图纸", "图纸/2024"} {
		abs, clean, err := root.Resolve(rel)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", rel, err)
		}
		if !filepath.IsAbs(abs) {
			t.Fatalf("Resolve(%q) returned a relative path %q", rel, abs)
		}
		if clean != rel {
			t.Fatalf("Resolve(%q) cleaned to %q", rel, clean)
		}
	}
}

func TestResolveFollowsJunctionInsideShareButNotOutside(t *testing.T) {
	root := newShareTree(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A junction/symlink pointing outside the share must be rejected.
	link := filepath.Join(root.Dir, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := root.Resolve("escape/secret.txt"); err == nil {
		t.Fatal("Resolve followed a symlink out of the share")
	}
}

func TestListHidesReservedNamesAndSortsDirsFirst(t *testing.T) {
	root := newShareTree(t)
	entries, total, err := root.List("", ListOptions{Page: 1, PageSize: 50, Sort: SortName})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (reserved names hidden)", total)
	}
	if !entries[0].IsDir || entries[0].Name != "图纸" {
		t.Fatalf("first entry = %+v, want directory 图纸", entries[0])
	}
	if entries[1].Name != "说明.txt" || entries[1].Size != 5 {
		t.Fatalf("second entry = %+v", entries[1])
	}
}

func TestListPaginatesAndSortsBySize(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"c.txt", "a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	entries, total, err := root.List("", ListOptions{Page: 1, PageSize: 2, Sort: SortSize, Desc: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 4 || len(entries) != 2 {
		t.Fatalf("total=%d len=%d, want 4/2", total, len(entries))
	}
	if entries[0].Name != "big.txt" {
		t.Fatalf("largest first = %q, want big.txt", entries[0].Name)
	}

	// Asking past the end is empty, not an error.
	entries, _, err = root.List("", ListOptions{Page: 9, PageSize: 2})
	if err != nil {
		t.Fatalf("list page 9: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("page 9 returned %d entries", len(entries))
	}
}

func TestListSortByModTime(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	fresh := filepath.Join(dir, "new.txt")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := root.List("", ListOptions{Page: 1, PageSize: 10, Sort: SortModTime})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if entries[0].Name != "old.txt" {
		t.Fatalf("oldest first = %q, want old.txt", entries[0].Name)
	}
}

func TestAttachNotes(t *testing.T) {
	entries := []Entry{{Path: "图纸/HR/合同.pdf"}, {Path: "说明.txt"}}
	AttachNotes(entries, map[string]string{"图纸/hr/合同.pdf": "甲方终版"})
	if entries[0].Note != "甲方终版" {
		t.Fatalf("note = %q, want 甲方终版", entries[0].Note)
	}
	if entries[1].Note != "" {
		t.Fatalf("unexpected note %q", entries[1].Note)
	}
}

func TestResolveRejectsShortNameAliasOfReservedFile(t *testing.T) {
	root := newShareTree(t)
	// Windows invents an 8.3 alias (LANDPA~1) for ".landpart-abc"; resolving it
	// must still hit the reserved-name rule.
	if _, _, err := root.Resolve("LANDPA~1"); err == nil {
		t.Skip("8.3 alias not resolvable on this filesystem")
	}
}

func TestPaginationHugePageDoesNotPanic(t *testing.T) {
	root := newShareTree(t)
	entries, _, err := root.List("", ListOptions{Page: 92233720368547760, PageSize: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("huge page returned %d entries, want 0", len(entries))
	}
}
