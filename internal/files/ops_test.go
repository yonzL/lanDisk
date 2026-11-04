package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirCreatesAndRejectsBothBadNamesAndDuplicates(t *testing.T) {
	root := newShareTree(t)
	created, err := root.Mkdir("图纸", "2025")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if created != "图纸/2025" {
		t.Fatalf("created = %q", created)
	}
	if _, err := os.Stat(filepath.Join(root.Dir, "图纸", "2025")); err != nil {
		t.Fatalf("directory missing on disk: %v", err)
	}
	if _, err := root.Mkdir("图纸", "2025"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate mkdir = %v, want ErrExists", err)
	}
	for _, name := range []string{"", "..", "a/b", "a:b", ".landpart-1", "x.", "CON"} {
		if _, err := root.Mkdir("", name); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("mkdir %q = %v, want ErrInvalidPath", name, err)
		}
	}
}

func TestRenameKeepsContentAndRefusesOverwrite(t *testing.T) {
	root := newShareTree(t)
	newRel, err := root.Rename("说明.txt", "说明-新版.txt")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if newRel != "说明-新版.txt" {
		t.Fatalf("newRel = %q", newRel)
	}
	body, err := os.ReadFile(filepath.Join(root.Dir, "说明-新版.txt"))
	if err != nil || string(body) != "hello" {
		t.Fatalf("content after rename = %q (%v)", body, err)
	}
	if _, err := root.Rename("说明-新版.txt", "图纸"); !errors.Is(err, ErrExists) {
		t.Fatalf("rename onto existing = %v, want ErrExists", err)
	}
	if _, err := root.Rename("", "whatever"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("renaming the share root = %v, want ErrInvalidPath", err)
	}
}

func TestMoveBetweenDirectories(t *testing.T) {
	root := newShareTree(t)
	newRel, err := root.Move("说明.txt", "图纸")
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if newRel != "图纸/说明.txt" {
		t.Fatalf("newRel = %q", newRel)
	}
	if _, err := os.Stat(filepath.Join(root.Dir, "图纸", "说明.txt")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	// Moving a directory into itself is refused.
	if _, err := root.Move("图纸", "图纸/2024"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("move into own subtree = %v, want ErrInvalidPath", err)
	}
	// Moving onto an existing name is refused.
	if err := os.WriteFile(filepath.Join(root.Dir, "图纸", "另一个.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root.Dir, "另一个.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Move("另一个.txt", "图纸"); !errors.Is(err, ErrExists) {
		t.Fatalf("move onto existing = %v, want ErrExists", err)
	}
}

func TestRenameIsCaseOnlyNoop(t *testing.T) {
	root := newShareTree(t)
	if _, err := root.Rename("说明.txt", "说明.TXT"); err != nil {
		t.Fatalf("case-only rename returned error: %v", err)
	}
}
