package upload

import (
	"context"
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
	sh, err := st.CreateShare(context.Background(), store.Share{Name: "s", VirtualPath: "/s", Root: dir}, store.PermWrite)
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	root, err := files.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st, root, sh.ID, dir
}

func TestUploadChunksResumeAndComplete(t *testing.T) {
	st, root, shareID, dir := newEnv(t)
	size := int64(ChunkSize + 10) // one full chunk + a short tail

	sess, err := Begin(st, root, shareID, "", "报告.bin", size)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	partAbs := sess.PartAbs
	if _, err := os.Stat(partAbs); err != nil {
		t.Fatalf("part file missing: %v", err)
	}

	first := make([]byte, ChunkSize)
	for i := range first {
		first[i] = 'a'
	}
	if err := sess.WriteChunk(0, first); err != nil {
		t.Fatalf("write chunk 0: %v", err)
	}
	if err := sess.MarkChunk(0); err != nil {
		t.Fatalf("mark chunk 0: %v", err)
	}

	// Simulate a browser restart: a fresh session object resumed from the store.
	chunks, err := st.ReceivedChunks(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("received chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0] != 0 {
		t.Fatalf("chunks after resume = %v, want [0]", chunks)
	}

	if err := sess.Complete(); err == nil {
		t.Fatal("complete succeeded with a chunk missing")
	}

	tail := []byte("0123456789")
	if err := sess.WriteChunk(1, tail); err != nil {
		t.Fatalf("write chunk 1: %v", err)
	}
	if err := sess.MarkChunk(1); err != nil {
		t.Fatalf("mark chunk 1: %v", err)
	}
	if err := sess.Complete(); err != nil {
		t.Fatalf("complete: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "报告.bin"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if int64(len(body)) != size {
		t.Fatalf("target size = %d, want %d", len(body), size)
	}
	if string(body[:4]) != "aaaa" || string(body[size-10:]) != string(tail) {
		t.Fatal("target content does not match the uploaded chunks")
	}
	if _, err := os.Stat(partAbs); !os.IsNotExist(err) {
		t.Fatalf("part file still present after completion: %v", err)
	}
	if _, err := st.UploadByID(context.Background(), sess.ID); err == nil {
		t.Fatal("upload row survived completion")
	}
}

func TestBeginRejectsBadNames(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	for _, name := range []string{"", ".hidden", "a/b", "a:b", "x.landpart-1", "x.landrestore-2", ".land-trash"} {
		if _, err := Begin(st, root, shareID, "", name, 1); err == nil {
			t.Fatalf("Begin accepted name %q", name)
		}
	}
}

func TestBeginRejectsEscapingDirectory(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	if _, err := Begin(st, root, shareID, "../", "x.bin", 1); err == nil {
		t.Fatal("Begin accepted an escaping directory")
	}
}

func TestAbortRemovesPartFile(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	sess, err := Begin(st, root, shareID, "", "x.bin", 10)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	part := sess.PartAbs
	if err := sess.Abort(); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("abort left the part file behind")
	}
}

func TestCleanupRemovesExpiredSessionsAndParts(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	sess, err := Begin(st, root, shareID, "", "old.bin", 10)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Age the row past MaxAge.
	if _, err := st.DBForTests().Exec("UPDATE uploads SET created_at = ? WHERE id = ?",
		time.Now().Add(-2*MaxAge).Unix(), sess.ID); err != nil {
		t.Fatalf("age upload: %v", err)
	}
	n, err := Cleanup(st, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("cleanup = (%d,%v), want 1", n, err)
	}
	if _, err := os.Stat(sess.PartAbs); !os.IsNotExist(err) {
		t.Fatal("cleanup left the part file behind")
	}
}

// TestConcurrentChunksDoNotLoseBits covers two requests marking different chunks
// of the same session at once: the DB-level read-modify-write must keep both.
func TestConcurrentChunksDoNotLoseBits(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	size := int64(ChunkSize * 4)
	sess, err := Begin(st, root, shareID, "", "race.bin", size)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	chunk := make([]byte, ChunkSize)

	done := make(chan error, 4)
	for i := int64(0); i < 4; i++ {
		go func(index int64) {
			if err := sess.WriteChunk(index, chunk); err != nil {
				done <- err
				return
			}
			done <- sess.MarkChunk(index)
		}(i)
	}
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent chunk: %v", err)
		}
	}

	chunks, err := st.ReceivedChunks(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("received chunks: %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("concurrent writes lost chunks: %v", chunks)
	}
}

// TestExpiryUsesLastSeen proves an actively-uploaded session is not purged at
// created_at + MaxAge.
func TestExpiryUsesLastSeen(t *testing.T) {
	st, root, shareID, _ := newEnv(t)
	sess, err := Begin(st, root, shareID, "", "slow.bin", ChunkSize)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Created long ago, but written to just now.
	if _, err := st.DBForTests().Exec("UPDATE uploads SET created_at = ? WHERE id = ?",
		time.Now().Add(-10*MaxAge).Unix(), sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := sess.MarkChunk(0); err != nil {
		t.Fatalf("mark chunk: %v", err)
	}
	n, err := Cleanup(st, time.Now())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 0 {
		t.Fatalf("cleanup removed %d active sessions, want 0", n)
	}
	if _, err := os.Stat(sess.PartAbs); err != nil {
		t.Fatal("active upload's part file disappeared")
	}
}
