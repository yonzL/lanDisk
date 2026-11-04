package files

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestListPageDoesNotStatWholeDirectory guards the "100k files, first page under
// a second" goal structurally: with a name sort, only the page may be stat'ed.
// (A timing bound alone would pass even if every row were stat'ed.)
func TestListPageDoesNotStatWholeDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large directory test in -short mode")
	}
	dir := t.TempDir()
	const n = 5000
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file-%05d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	original := statFile
	calls := 0
	statFile = func(name string) (os.FileInfo, error) {
		calls++
		return original(name)
	}
	defer func() { statFile = original }()

	page, total, err := root.List("", ListOptions{Page: 1, PageSize: 100, Sort: SortName})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != n || len(page) != 100 {
		t.Fatalf("total=%d len=%d, want %d/100", total, len(page), n)
	}
	if page[0].Size != 1 {
		t.Fatalf("page entry size = %d, want the file's size", page[0].Size)
	}
	if calls > 100 {
		t.Fatalf("statFile called %d times for a 100-row page — the whole directory is being stat'ed", calls)
	}
}

// TestNoteSortUsesProvidedNotes proves the note column actually orders rows.
func TestNoteSortUsesProvidedNotes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{"a.txt": "zzz", "b.txt": "aaa", "c.txt": "mmm"}
	page, _, err := root.List("", ListOptions{
		Page: 1, PageSize: 10, Sort: SortNote,
		Notes: func(p string) string { return notes[p] },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"b.txt", "c.txt", "a.txt"}
	for i, name := range want {
		if page[i].Name != name {
			t.Fatalf("note order = %v, want %v", entryNames(page), want)
		}
	}

	page, _, err = root.List("", ListOptions{
		Page: 1, PageSize: 10, Sort: SortNote, Desc: true,
		Notes: func(p string) string { return notes[p] },
	})
	if err != nil {
		t.Fatal(err)
	}
	if page[0].Name != "a.txt" {
		t.Fatalf("descending note order starts with %q, want a.txt", page[0].Name)
	}
}

func entryNames(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
