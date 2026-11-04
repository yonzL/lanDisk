package store

import (
	"context"
	"testing"
)

func seedShare(t *testing.T, st *Store) int64 {
	t.Helper()
	sh, err := st.CreateShare(context.Background(), Share{Name: "s", VirtualPath: "/s", Root: "/tmp/s"}, PermRead)
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	return sh.ID
}

func TestSetNoteRoundTripAndClear(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	shareID := seedShare(t, st)

	if err := st.SetNote(ctx, shareID, "图纸/a.dwg", "甲方终版", 1); err != nil {
		t.Fatalf("set note: %v", err)
	}
	note, ok, err := st.NoteFor(ctx, shareID, "图纸/a.dwg")
	if err != nil || !ok || note != "甲方终版" {
		t.Fatalf("note = (%q,%v,%v), want 甲方终版", note, ok, err)
	}

	// Case-folded lookup, mirroring the filesystem.
	if note, ok, _ := st.NoteFor(ctx, shareID, "图纸/A.DWG"); !ok || note != "甲方终版" {
		t.Fatalf("case-folded lookup failed: (%q,%v)", note, ok)
	}

	if err := st.SetNote(ctx, shareID, "图纸/a.dwg", "   ", 2); err != nil {
		t.Fatalf("clear note: %v", err)
	}
	if _, ok, _ := st.NoteFor(ctx, shareID, "图纸/a.dwg"); ok {
		t.Fatal("blank note was stored instead of clearing")
	}
}

func TestNotesForBulkAndChunking(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	shareID := seedShare(t, st)

	var paths []string
	for i := 0; i < 450; i++ {
		p := "d/f" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".txt"
		paths = append(paths, p)
		if err := st.SetNote(ctx, shareID, p, "n"+p, 1); err != nil {
			t.Fatalf("set note: %v", err)
		}
	}
	notes, err := st.NotesFor(ctx, shareID, paths)
	if err != nil {
		t.Fatalf("bulk notes: %v", err)
	}
	if len(notes) != len(paths) {
		t.Fatalf("got %d notes, want %d (chunking must not drop rows)", len(notes), len(paths))
	}
}

func TestMoveNotesFollowsFileAndSubtree(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	shareID := seedShare(t, st)

	for p, note := range map[string]string{
		"图纸/a.dwg":      "a 的备注",
		"图纸/2024/b.dwg": "b 的备注",
		"图纸/其它.txt":     "不该跟着走的备注",
	} {
		if err := st.SetNote(ctx, shareID, p, note, 1); err != nil {
			t.Fatalf("set note: %v", err)
		}
	}

	// Moving a file keeps its note.
	if err := st.MoveNotes(ctx, shareID, "图纸/a.dwg", "归档/a.dwg"); err != nil {
		t.Fatalf("move file notes: %v", err)
	}
	if note, ok, _ := st.NoteFor(ctx, shareID, "归档/a.dwg"); !ok || note != "a 的备注" {
		t.Fatalf("moved file lost its note: (%q,%v)", note, ok)
	}
	if _, ok, _ := st.NoteFor(ctx, shareID, "图纸/a.dwg"); ok {
		t.Fatal("stale note left at the old path")
	}

	// Moving a directory migrates its whole subtree.
	if err := st.MoveNotes(ctx, shareID, "图纸", "资料"); err != nil {
		t.Fatalf("move dir notes: %v", err)
	}
	if note, ok, _ := st.NoteFor(ctx, shareID, "资料/2024/b.dwg"); !ok || note != "b 的备注" {
		t.Fatalf("subtree note not migrated: (%q,%v)", note, ok)
	}
	if note, ok, _ := st.NoteFor(ctx, shareID, "资料/其它.txt"); !ok || note != "不该跟着走的备注" {
		t.Fatalf("sibling note not migrated: (%q,%v)", note, ok)
	}
}

func TestMoveNotesDoesNotTouchSiblings(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	shareID := seedShare(t, st)

	if err := st.SetNote(ctx, shareID, "a", "a 的", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNote(ctx, shareID, "ab", "ab 的", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveNotes(ctx, shareID, "a", "x"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, ok, _ := st.NoteFor(ctx, shareID, "ab"); !ok {
		t.Fatal("sibling note was moved by a prefix match")
	}
}
