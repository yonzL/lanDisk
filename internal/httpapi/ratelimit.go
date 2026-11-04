package httpapi

import (
	"context"
	"io"
	"land/internal/store"
	"net/http"
	"sync"
	"time"
)

// concurrencyLimiter caps the number of requests being served at once. When the
// limit is reached the request is refused with 429 instead of queueing without
// bound.
type concurrencyLimiter struct {
	slots chan struct{}

	mu    sync.RWMutex
	limit int
}

func newConcurrencyLimiter(limit int) *concurrencyLimiter {
	if limit < 1 {
		limit = 64
	}
	return &concurrencyLimiter{slots: make(chan struct{}, limit), limit: limit}
}

// setLimit resizes the limiter. Shrinking does not revoke in-flight requests;
// it only stops new ones once the channel is full.
func (l *concurrencyLimiter) setLimit(n int) {
	if n < 1 {
		n = 64
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n == l.limit {
		return
	}
	l.limit = n
	l.slots = make(chan struct{}, n)
}

func (l *concurrencyLimiter) current() chan struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.slots
}

func (s *Server) concurrencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := s.Limiter
		if l == nil {
			next.ServeHTTP(w, r)
			return
		}
		// Capture the channel used to acquire: resizing the limiter in another
		// goroutine swaps s.slots, and a deferred release reading the field would
		// then block forever on the new, empty channel.
		slots := l.current()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeError(w, http.StatusTooManyRequests, "busy", "并发连接已达上限，请稍后重试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// throttleWriter paces writes to a byte-per-second budget. It is a coarse but
// honest limiter: it sleeps between writes so a single download cannot saturate
// a small office uplink.
type throttleWriter struct {
	http.ResponseWriter
	kbps  int
	start time.Time
	sent  int64
}

func newThrottleWriter(w http.ResponseWriter, kbps int) http.ResponseWriter {
	if kbps <= 0 {
		return w
	}
	return &throttleWriter{ResponseWriter: w, kbps: kbps, start: time.Now()}
}

func (t *throttleWriter) Write(b []byte) (int, error) {
	n, err := t.ResponseWriter.Write(b)
	t.sent += int64(n)
	if t.kbps > 0 {
		want := time.Duration(float64(t.sent) / float64(t.kbps*1024) * float64(time.Second))
		if sleep := want - time.Since(t.start); sleep > 0 {
			time.Sleep(sleep)
		}
	}
	return n, err
}

// throttleReader paces reads from a request body (upload throttling).
type throttleReader struct {
	r     io.Reader
	kbps  int
	start time.Time
	read  int64
}

func newThrottleReader(r io.Reader, kbps int) io.Reader {
	if kbps <= 0 {
		return r
	}
	return &throttleReader{r: r, kbps: kbps, start: time.Now()}
}

func (t *throttleReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.read += int64(n)
	if t.kbps > 0 {
		want := time.Duration(float64(t.read) / float64(t.kbps*1024) * float64(time.Second))
		if sleep := want - time.Since(t.start); sleep > 0 {
			time.Sleep(sleep)
		}
	}
	return n, err
}

// applyRuntimeLimits re-reads the rate/concurrency settings. Called on a timer so
// operators can change them without a restart.
func (s *Server) applyRuntimeLimits(ctx context.Context) {
	maxConns, err := s.Store.SettingInt(ctx, store.KeyMaxConns, 64)
	if err == nil && s.Limiter != nil {
		s.Limiter.setLimit(maxConns)
	}
	if down, err := s.Store.SettingInt(ctx, store.KeyDownloadRateKbps, 0); err == nil {
		s.mu.Lock()
		s.downloadKbps = down
		s.mu.Unlock()
	}
	if up, err := s.Store.SettingInt(ctx, store.KeyUploadRateKbps, 0); err == nil {
		s.mu.Lock()
		s.uploadKbps = up
		s.mu.Unlock()
	}
	if err == nil && s.IPFilter != nil {
		_ = s.IPFilter.load(ctx, s.Store)
	}
}

// rateFor reads the current download/upload budgets.
func (s *Server) rateFor(download bool) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if download {
		return s.downloadKbps
	}
	return s.uploadKbps
}

// NewIPFilter builds the address filter used by the server.
func NewIPFilter() *ipFilter { return newIPFilter() }

// NewConcurrencyLimiter builds a limiter with the given initial ceiling.
func NewConcurrencyLimiter(limit int) *concurrencyLimiter { return newConcurrencyLimiter(limit) }

// LoadRuntimeLimits applies settings, IP rules and rate budgets at startup.
func (s *Server) LoadRuntimeLimits(ctx context.Context) error {
	if s.Limiter == nil {
		s.Limiter = newConcurrencyLimiter(64)
	}
	if s.IPFilter == nil {
		s.IPFilter = newIPFilter()
	}
	err := s.IPFilter.load(ctx, s.Store)
	s.applyRuntimeLimits(ctx)
	return err
}

// RefreshRuntimeLimits is the periodic refresh (settings/IP rules change at
// runtime, and hit counters are flushed on the same tick).
func (s *Server) RefreshRuntimeLimits(ctx context.Context) {
	s.applyRuntimeLimits(ctx)
	if s.IPFilter != nil {
		s.IPFilter.flushHits(ctx, s.Store)
	}
}

// closeReader keeps the body closable while throttling reads.
type closeReader struct {
	io.Reader
	io.Closer
}

func newThrottledBody(r io.ReadCloser, kbps int) io.ReadCloser {
	return &closeReader{Reader: newThrottleReader(r, kbps), Closer: r}
}
