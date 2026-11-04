// Package logs records every operation asynchronously. Logging is a side
// channel: a failing log write must never fail the request that produced it.
package logs

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"land/internal/store"
)

// Entry is one operation to record.
type Entry struct {
	Action  string
	IP      string
	ShareID int64
	Path    string
	Result  string
	Detail  string
}

// Writer batches entries into the database from a bounded queue.
type Writer struct {
	st        *store.Store
	logger    *slog.Logger
	queue     chan store.LogEntry
	resolving chan struct{}
	done      chan struct{}
	once      sync.Once
	drops     int64
	closed    bool
	mu        sync.Mutex
}

// New starts a writer. Call Close on shutdown to flush.
func New(st *store.Store, logger *slog.Logger) *Writer {
	w := &Writer{
		st:        st,
		logger:    logger,
		queue:     make(chan store.LogEntry, 4096),
		resolving: make(chan struct{}, 4),
		done:      make(chan struct{}),
	}
	go w.loop()
	return w
}

// Record queues one event. It never blocks: a full queue drops the entry and
// counts it, because a slow disk must not stall file transfers. After Close the
// call is a no-op — shutdown must not turn a late request into a panic on a
// closed channel.
func (w *Writer) Record(e Entry) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	now := time.Now()
	entry := store.LogEntry{
		TS:      now,
		IP:      e.IP,
		Action:  e.Action,
		ShareID: e.ShareID,
		Path:    e.Path,
		Result:  e.Result,
		Detail:  e.Detail,
	}
	// Prefer the curated mapping. DNS is resolved out of band and bounded:
	// a synchronous reverse lookup on an IP without a PTR record can stall for
	// seconds, and the audit log must never slow a request down.
	if host, err := w.st.HostByIP(context.Background(), e.IP); err == nil {
		entry.Host = host.Hostname
	} else {
		w.resolveAsync(e.IP)
	}

	select {
	case w.queue <- entry:
	default:
		w.mu.Lock()
		w.drops++
		w.mu.Unlock()
	}
}

// Drops reports how many entries were discarded because the queue was full.
func (w *Writer) Drops() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.drops
}

// Close flushes the queue and stops the writer.
func (w *Writer) Close() {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		close(w.queue)
		<-w.done
	})
}

func (w *Writer) loop() {
	defer close(w.done)
	batch := make([]store.LogEntry, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.st.InsertLogs(ctx, batch); err != nil {
			w.logger.Error("写入访问日志失败", "error", err, "count", len(batch))
		}
		batch = batch[:0]
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case e, ok := <-w.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// resolveAsync looks an IP up once, with a hard timeout, and records the result
// as a dns-sourced row (manual rows are never overwritten).
func (w *Writer) resolveAsync(ip string) {
	if net.ParseIP(ip) == nil {
		return
	}
	select {
	case w.resolving <- struct{}{}:
	default:
		return // already busy resolving: skip this one, it will be retried later
	}
	go func() {
		defer func() { <-w.resolving }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		names, err := net.DefaultResolver.LookupAddr(ctx, ip)
		if err != nil || len(names) == 0 {
			return
		}
		name := strings.TrimSuffix(names[0], ".")
		_ = w.st.SetHostFromDNS(context.Background(), ip, name, time.Now())
	}()
}
