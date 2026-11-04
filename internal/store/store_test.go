package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "land.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestOpenSeedsSettingsDefaults(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	port, ok, err := st.Setting(ctx, KeyPort)
	if err != nil {
		t.Fatalf("read port: %v", err)
	}
	if !ok || port != "80" {
		t.Fatalf("port default = %q (present=%v), want 80", port, ok)
	}

	logDays, err := st.SettingInt(ctx, KeyLogRetentionDays, -1)
	if err != nil {
		t.Fatalf("read log retention: %v", err)
	}
	trashDays, err := st.SettingInt(ctx, KeyTrashRetentionDays, -1)
	if err != nil {
		t.Fatalf("read trash retention: %v", err)
	}
	if logDays != 90 || trashDays != 90 {
		t.Fatalf("retention defaults = log:%d trash:%d, want 90/90", logDays, trashDays)
	}
}

func TestSetSettingRoundTripSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "land.db")
	ctx := context.Background()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.SetSetting(ctx, KeyPort, "8080"); err != nil {
		t.Fatalf("write port: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer again.Close()

	port, _, err := again.Setting(ctx, KeyPort)
	if err != nil {
		t.Fatalf("read port after reopen: %v", err)
	}
	if port != "8080" {
		t.Fatalf("port after reopen = %q, want 8080", port)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "land.db")

	for i := 0; i < 3; i++ {
		st, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close #%d: %v", i+1, err)
		}
	}
}

func TestEnsureSettingsKeepsOperatorValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "land.db")
	ctx := context.Background()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.SetSetting(ctx, KeySiteTitle, "研发部共享"); err != nil {
		t.Fatalf("write title: %v", err)
	}
	st.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer again.Close()

	title, _, err := again.Setting(ctx, KeySiteTitle)
	if err != nil {
		t.Fatalf("read title: %v", err)
	}
	if title != "研发部共享" {
		t.Fatalf("title after reopen = %q, want operator value preserved", title)
	}
}

func TestSchemaCreatesReservedNameTables(t *testing.T) {
	st := openTemp(t)
	// Every table the architecture promises must exist after migration; a
	// missing one would only surface much later as a runtime error.
	want := []string{
		"settings", "shares", "share_mounts", "share_rules", "notes",
		"users", "sessions", "uploads", "trash", "ip_rules", "hosts", "logs",
	}
	for _, table := range want {
		var name string
		err := st.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing after migration: %v", table, err)
		}
	}
}

func TestSetSettingRejectsNonIntegerForNumericKeys(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if err := st.SetSetting(ctx, KeyPort, "80B0"); err == nil {
		t.Fatalf("writing %q to %s succeeded, want validation error", "80B0", KeyPort)
	}
	if err := st.SetSetting(ctx, KeyPort, "70000"); err == nil {
		t.Fatalf("out-of-range port accepted, want error")
	}
	if err := st.SetSetting(ctx, KeyLogRetentionDays, "90"); err != nil {
		t.Fatalf("valid integer rejected: %v", err)
	}
}

func TestSettingIntReportsUnparsableValue(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Bypass SetSetting validation to simulate a hand-edited database.
	if _, err := st.db.Exec("UPDATE settings SET value='80B0' WHERE key=?", KeyPort); err != nil {
		t.Fatalf("corrupt setting: %v", err)
	}
	if _, err := st.SettingInt(ctx, KeyPort, 80); err == nil {
		t.Fatalf("SettingInt accepted a non-integer value, want error")
	}
}

func TestSettingIntRangeChecksHandEditedValues(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ value string }{{"0"}, {"70000"}} {
		st := openTemp(t)
		if _, err := st.db.Exec("UPDATE settings SET value=? WHERE key=?", tc.value, KeyPort); err != nil {
			t.Fatalf("corrupt port: %v", err)
		}
		if _, err := st.SettingInt(ctx, KeyPort, 80); err == nil {
			t.Fatalf("port=%s accepted on read, want range error", tc.value)
		}
	}
}

func TestDecodeLogPathsRewritesEncodedTargets(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	entries := []LogEntry{
		{TS: time.Now(), IP: "127.0.0.1", Action: "download", Path: "/api/shares/1/raw?path=%E6%8A%80%E6%9C%AF%2F%E5%9B%BE.dwg", Result: "ok"},
		{TS: time.Now(), IP: "127.0.0.1", Action: "browse", Path: "/api/shares/1/list?path=a/b", Result: "ok"},
	}
	if err := st.InsertLogs(ctx, entries); err != nil {
		t.Fatalf("insert: %v", err)
	}
	n, err := st.DecodeLogPaths(ctx)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != 1 {
		t.Fatalf("decoded %d rows, want 1", n)
	}
	got, _, err := st.QueryLogs(ctx, LogFilter{Action: "download"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got[0].Path != "/api/shares/1/raw?path=技术/图.dwg" {
		t.Fatalf("path = %q", got[0].Path)
	}
}
