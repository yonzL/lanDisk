package app

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"land/internal/store"
)

// freePort asks the OS for an unused port so tests never bind :80 or collide
// with a running service.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newAppOnPort(t *testing.T, port int) (*App, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "land.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.SetSetting(t.Context(), store.KeyPort, itoa(port)); err != nil {
		t.Fatalf("set port: %v", err)
	}
	st.Close()

	a, err := New(dbPath, "test", nil)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return a, dbPath
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestNewReadsPortFromSettings(t *testing.T) {
	port := freePort(t)
	a, _ := newAppOnPort(t, port)
	defer a.Shutdown(t.Context())

	want := ":" + itoa(port)
	if got := a.Addr(); got != want {
		t.Fatalf("addr = %q, want %q", got, want)
	}
}

func TestStartFailsWhenPortTaken(t *testing.T) {
	port := freePort(t)
	a, _ := newAppOnPort(t, port)
	defer a.Shutdown(t.Context())

	first, _ := newAppOnPort(t, port)
	if err := first.Start(); err != nil {
		t.Fatalf("first start on free port %d: %v", port, err)
	}
	defer first.Shutdown(t.Context())

	if err := a.Start(); err == nil {
		t.Fatalf("second Start on port %d succeeded, want error", port)
	}
}

func TestShutdownIsPromptAndClean(t *testing.T) {
	port := freePort(t)
	a, _ := newAppOnPort(t, port)
	if err := a.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Shutdown(t.Context()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not return within 3s (completion signal consumed twice)")
	}
}

func TestParseListeningPID(t *testing.T) {
	out := `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       912
  TCP    0.0.0.0:80             0.0.0.0:0              LISTENING       4132
  TCP    127.0.0.1:8080         0.0.0.0:0              LISTENING       777
  TCP    192.168.1.20:80        192.168.1.31:52100     ESTABLISHED     4132
  TCP    [::]:80                [::]:0                 LISTENING       4132
`
	for _, tc := range []struct {
		port int
		want int
		ok   bool
	}{
		{port: 80, want: 4132, ok: true},
		{port: 180, want: 0, ok: false},
		{port: 8080, want: 777, ok: true},
	} {
		pid, ok := parseListeningPID(out, tc.port)
		if ok != tc.ok || pid != tc.want {
			t.Fatalf("port %d → (%d,%v), want (%d,%v)", tc.port, pid, ok, tc.want, tc.ok)
		}
	}
}

// TestServeAndShutdownShareTheCompletionSignal reproduces the console wiring:
// a goroutine consumes Serve() while Shutdown runs. Both must return; neither
// may swallow the other's completion signal.
func TestServeAndShutdownShareTheCompletionSignal(t *testing.T) {
	port := freePort(t)
	a, _ := newAppOnPort(t, port)
	if err := a.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- a.Serve() }()

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- a.Shutdown(t.Context()) }()

	for name, ch := range map[string]chan error{"Serve": served, "Shutdown": shutdownDone} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("%s returned error: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s did not return within 3s", name)
		}
	}
}

// TestShutdownReportsDrainedTimeout: a handler that outlives the shutdown
// deadline must make Shutdown return an error, not a silent success.
func TestShutdownReportsDrainedTimeout(t *testing.T) {
	port := freePort(t)
	a, _ := newAppOnPort(t, port)
	a.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	if err := a.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	go func() {
		resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Shutdown(ctx); err == nil {
		t.Fatal("Shutdown returned nil while a handler was still running past the deadline")
	}
}
