package logs

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"land/internal/store"
)

func newWriter(t *testing.T) (*store.Store, *Writer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "land.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	w := New(st, slog.Default())
	t.Cleanup(w.Close)
	return st, w
}

func TestRecordFlushesOnClose(t *testing.T) {
	st, w := newWriter(t)
	w.Record(Entry{Action: "download", IP: "192.168.1.31", ShareID: 1, Path: "a.txt", Result: "ok"})
	w.Close()

	entries, total, err := st.QueryLogs(context.Background(), store.LogFilter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("entries=%d total=%d, want 1", len(entries), total)
	}
	if entries[0].Action != "download" || entries[0].IP != "192.168.1.31" {
		t.Fatalf("entry = %+v", entries[0])
	}
}

func TestCuratedHostAppliesToNewEntries(t *testing.T) {
	st, w := newWriter(t)
	ctx := context.Background()
	if err := st.SetHost(ctx, store.Host{IP: "192.168.1.31", Hostname: "张三的笔记本", Source: "manual"}, time.Now()); err != nil {
		t.Fatalf("set host: %v", err)
	}
	w.Record(Entry{Action: "browse", IP: "192.168.1.31", Result: "ok"})
	w.Close()

	entries, _, err := st.QueryLogs(ctx, store.LogFilter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if entries[0].Host != "张三的笔记本" {
		t.Fatalf("host = %q, want curated name", entries[0].Host)
	}
}

func TestRecallAppliesToOldRows(t *testing.T) {
	st, w := newWriter(t)
	ctx := context.Background()
	w.Record(Entry{Action: "browse", IP: "192.168.1.40", Result: "ok"})
	w.Close()

	// Curate the mapping afterwards; the old row must show the new name.
	if err := st.SetHost(ctx, store.Host{IP: "192.168.1.40", Hostname: "会议室投影", Source: "manual"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, _, err := st.QueryLogs(ctx, store.LogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Host != "会议室投影" {
		t.Fatalf("old log row host = %q, want the curated name", entries[0].Host)
	}
}

func TestFilterByActionAndIP(t *testing.T) {
	st, w := newWriter(t)
	ctx := context.Background()
	w.Record(Entry{Action: "download", IP: "10.0.0.1", Result: "ok"})
	w.Record(Entry{Action: "delete", IP: "10.0.0.2", Result: "ok"})
	w.Record(Entry{Action: "delete", IP: "10.0.0.1", Result: "error", Detail: "403"})
	w.Close()

	entries, total, err := st.QueryLogs(ctx, store.LogFilter{Action: "delete"})
	if err != nil || total != 2 || len(entries) != 2 {
		t.Fatalf("action filter = %d entries (total %d, err %v)", len(entries), total, err)
	}
	entries, total, err = st.QueryLogs(ctx, store.LogFilter{Action: "delete", IP: "10.0.0.1"})
	if err != nil || total != 1 || entries[0].Detail != "403" {
		t.Fatalf("action+ip filter = %+v (total %d, err %v)", entries, total, err)
	}
}

func TestPurgeLogsKeepsFreshRows(t *testing.T) {
	st, w := newWriter(t)
	ctx := context.Background()
	w.Record(Entry{Action: "download", IP: "10.0.0.1", Result: "ok"})
	w.Close()

	if _, err := st.DBForTests().Exec("UPDATE logs SET ts = ?", time.Now().AddDate(0, 0, -200).Unix()); err != nil {
		t.Fatal(err)
	}
	removed, err := st.PurgeLogs(ctx, time.Now().AddDate(0, 0, -90))
	if err != nil || removed != 1 {
		t.Fatalf("purge = (%d,%v), want 1", removed, err)
	}
	if _, total, _ := st.QueryLogs(ctx, store.LogFilter{}); total != 0 {
		t.Fatalf("old rows survived purge: %d", total)
	}
}
