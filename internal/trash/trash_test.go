package trash

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"land/internal/files"
	"land/internal/store"
)

func newEnv(t *testing.T) (*store.Store, files.Root, int64, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "land.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	dir := t.TempDir()
	sh, err := st.CreateShare(context.Background(), store.Share{Name: "s", VirtualPath: "/s", Root: dir}, store.PermRead)
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	root, err := files.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st, root, sh.ID, dir
}

func TestDeleteMovesToTrashAndRecordsOriginalPath(t *testing.T) {
	st, root, shareID, dir := newEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "报告.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := Delete(context.Background(), st, root, shareID, "报告.txt", "192.168.1.31", time.Now())
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if entry.OrigRelPath != "报告.txt" || entry.IP != "192.168.1.31" {
		t.Fatalf("entry = %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(dir, "报告.txt")); !os.IsNotExist(err) {
		t.Fatal("original file still present")
	}
	trashAbs := filepath.Join(dir, DirName, filepath.Base(entry.TrashRelPath))
	body, err := os.ReadFile(trashAbs)
	if err != nil || string(body) != "hello" {
		t.Fatalf("trashed file = %q (%v)", body, err)
	}

	// The recycle bin must not be reachable as a normal path.
	if _, _, err := root.Resolve(DirName); err == nil {
		t.Fatal("Resolve accepted the recycle-bin directory")
	}
}

func TestDeleteDirectorySubtree(t *testing.T) {
	st, root, shareID, dir := newEnv(t)
	if err := os.MkdirAll(filepath.Join(dir, "图纸", "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "图纸", "2024", "a.dwg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, err := Delete(context.Background(), st, root, shareID, "图纸", "ip", time.Now())
	if err != nil {
		t.Fatalf("delete dir: %v", err)
	}
	trashAbs := filepath.Join(dir, DirName, filepath.Base(entry.TrashRelPath), "2024", "a.dwg")
	if _, err := os.Stat(trashAbs); err != nil {
		t.Fatalf("subtree not preserved: %v", err)
	}
}

func TestRestoreConflictsWithoutForce(t *testing.T) {
	st, root, shareID, dir := newEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, err := Delete(ctx, st, root, shareID, "a.txt", "ip", time.Now())
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Something re-created the original path.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, st, root, entry.ID, false); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("restore without force = %v, want ErrTargetExists", err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(body) != "new" {
		t.Fatalf("restore overwrote the file without force: %q", body)
	}

	// With force it replaces the occupant and the row disappears.
	if _, err := Restore(ctx, st, root, entry.ID, true); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(body) != "old" {
		t.Fatalf("restored content = %q", body)
	}
	if _, err := st.TrashByID(ctx, entry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("trash row survived restore: %v", err)
	}
}

func TestRestoreReapsRowWhenFileIsGone(t *testing.T) {
	st, root, shareID, dir := newEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, err := Delete(ctx, st, root, shareID, "gone.txt", "ip", time.Now())
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, DirName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, st, root, entry.ID, true); err == nil {
		t.Fatal("restore of a vanished file succeeded")
	}
	if _, err := st.TrashByID(ctx, entry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("dangling trash row was not reaped")
	}
}

func TestPurgeRemovesOnlyExpiredEntries(t *testing.T) {
	st, root, shareID, dir := newEnv(t)
	ctx := context.Background()
	for _, name := range []string{"old.txt", "fresh.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldEntry, err := Delete(ctx, st, root, shareID, "old.txt", "ip", time.Now().AddDate(0, 0, -100))
	if err != nil {
		t.Fatalf("delete old: %v", err)
	}
	freshEntry, err := Delete(ctx, st, root, shareID, "fresh.txt", "ip", time.Now())
	if err != nil {
		t.Fatalf("delete fresh: %v", err)
	}

	removed, err := Purge(ctx, st, map[int64]files.Root{shareID: root}, 90, time.Now())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed != 1 {
		t.Fatalf("purged %d, want 1", removed)
	}
	if _, err := st.TrashByID(ctx, oldEntry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired entry survived purge")
	}
	if _, err := st.TrashByID(ctx, freshEntry.ID); err != nil {
		t.Fatal("fresh entry was purged")
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, filepath.Base(oldEntry.TrashRelPath))); !os.IsNotExist(err) {
		t.Fatal("expired file still on disk")
	}
}

func TestDeleteRootIsRefused(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	if _, err := Delete(context.Background(), st, root, shareID, "", "ip", time.Now()); err == nil {
		t.Fatal("deleting the share root was accepted")
	}
}
