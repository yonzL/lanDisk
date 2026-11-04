package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTree(t *testing.T) Root {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{
		"图纸/2024/合同.pdf",
		"图纸/2023/合同-旧.pdf",
		"合同/说明.txt",
		"其他.txt",
		"机密/合同-机密.pdf",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".landpart-1"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSearchMatchesNamesRecursively(t *testing.T) {
	root := buildTree(t)
	res, err := root.Search(context.Background(), "", SearchOptions{Query: "合同"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	names := map[string]bool{}
	for _, e := range res.Entries {
		names[e.Name] = true
	}
	if !names["合同.pdf"] || !names["合同-旧.pdf"] || !names["合同-机密.pdf"] {
		t.Fatalf("missing matches: %v", names)
	}
	if names["说明.txt"] {
		t.Fatalf("non-matching entry returned: %v", names)
	}

	// Case-insensitive on a case-insensitive filesystem.
	res, err = root.Search(context.Background(), "", SearchOptions{Query: "PDF"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("case-insensitive search found %d, want 3", len(res.Entries))
	}
}

func TestSearchRespectsFilterAndReservedNames(t *testing.T) {
	root := buildTree(t)
	res, err := root.Search(context.Background(), "", SearchOptions{
		Query: "合同",
		Filter: func(p string, isDir bool) bool {
			return !strings.HasPrefix(p, "机密")
		},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Path, "机密") {
			t.Fatalf("filtered subtree leaked: %s", e.Path)
		}
	}

	res, _ = root.Search(context.Background(), "", SearchOptions{Query: "landpart"})
	if len(res.Entries) != 0 {
		t.Fatalf("reserved file matched: %v", res.Entries)
	}
}

func TestSearchReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		name := filepath.Join(dir, "hit-"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := root.Search(context.Background(), "", SearchOptions{Query: "hit", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Entries) != 5 || !res.Truncated {
		t.Fatalf("entries=%d truncated=%v, want 5/true", len(res.Entries), res.Truncated)
	}
}

func TestSearchTimesOut(t *testing.T) {
	root := buildTree(t)
	// Cancel before the call: timers on Windows have millisecond granularity, so
	// a 1ns Timeout would not reliably fire before a small tree is walked.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := root.Search(ctx, "", SearchOptions{Query: "合同"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.TimedOut {
		t.Fatal("search did not report a timeout")
	}
}

func TestSearchRespectsDepthLimit(t *testing.T) {
	root := buildTree(t)
	res, err := root.Search(context.Background(), "", SearchOptions{Query: "合同", MaxDepth: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, e := range res.Entries {
		if strings.Count(e.Path, "/") >= 1 {
			t.Fatalf("depth limit exceeded: %s", e.Path)
		}
	}
}

func TestSearchEmptyQueryReturnsNothing(t *testing.T) {
	root := buildTree(t)
	res, err := root.Search(context.Background(), "", SearchOptions{Query: "   "})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Entries) != 0 || res.Truncated {
		t.Fatalf("empty query returned %+v", res)
	}
}

func TestSearchReportsDepthLimitFlag(t *testing.T) {
	root := buildTree(t)
	res, err := root.Search(context.Background(), "", SearchOptions{Query: "合同", MaxDepth: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.DepthLimited {
		t.Fatal("pruned subtree did not set DepthLimited")
	}
}
