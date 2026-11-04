package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"land/internal/store"
)

// ipFilter decides whether a request may proceed. Rules come from ip_rules:
// deny wins over allow, an empty allow list means "no allowlist", and loopback is
// always permitted so an operator cannot lock themselves out from the machine.
type ipFilter struct {
	mu       sync.RWMutex
	allow    []*net.IPNet
	deny     []*net.IPNet
	byCIDR   map[string]string // cidr -> kind, for hit accounting
	pending  sync.Map          // cidr -> *hitCounter
	loadedAt time.Time
}

type hitCounter struct {
	mu   sync.Mutex
	n    int64
	last time.Time
}

func newIPFilter() *ipFilter {
	return &ipFilter{byCIDR: map[string]string{}}
}

// load refreshes the rule set from the database. Cheap enough to call on a
// timer; the middleware never blocks a request on a database read.
func (f *ipFilter) load(ctx context.Context, st *store.Store) error {
	rules, err := st.ListIPRules(ctx)
	if err != nil {
		return err
	}
	var allow, deny []*net.IPNet
	byCIDR := map[string]string{}
	for _, r := range rules {
		_, network, parseErr := net.ParseCIDR(r.CIDR)
		if parseErr != nil {
			continue
		}
		byCIDR[r.CIDR] = r.Kind
		if r.Kind == "allow" {
			allow = append(allow, network)
		} else {
			deny = append(deny, network)
		}
	}
	f.mu.Lock()
	f.allow, f.deny, f.byCIDR, f.loadedAt = allow, deny, byCIDR, time.Now()
	f.mu.Unlock()
	return nil
}

// allowed reports whether ip may proceed, and which rule rejected it.
func (f *ipFilter) allowed(ip net.IP) (bool, string) {
	if ip.IsLoopback() {
		return true, ""
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, n := range f.deny {
		if n.Contains(ip) {
			return false, n.String()
		}
	}
	if len(f.allow) == 0 {
		return true, ""
	}
	for _, n := range f.allow {
		if n.Contains(ip) {
			return true, ""
		}
	}
	return false, "not-in-allowlist"
}

// hit records a rejection for the status view (flushed to the database later).
func (f *ipFilter) hit(cidr string) {
	if cidr == "" {
		return
	}
	value, ok := f.pending.Load(cidr)
	if !ok {
		value, _ = f.pending.LoadOrStore(cidr, &hitCounter{})
	}
	c := value.(*hitCounter)
	c.mu.Lock()
	c.n++
	c.last = time.Now()
	c.mu.Unlock()
}

// flushHits writes accumulated counters to the database.
func (f *ipFilter) flushHits(ctx context.Context, st *store.Store) {
	f.pending.Range(func(key, value any) bool {
		cidr := key.(string)
		c := value.(*hitCounter)
		c.mu.Lock()
		n, last := c.n, c.last
		c.n = 0
		c.mu.Unlock()
		if n > 0 {
			_ = st.BumpIPRuleHits(ctx, cidr, n, last)
		}
		return true
	})
}

// middleware rejects requests from filtered addresses with 403. Rejections are
// counted instead of logged: otherwise a blocked scanner floods the audit log.
func (s *Server) ipFilterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.IPFilter == nil {
			next.ServeHTTP(w, r)
			return
		}
		ip := net.ParseIP(clientIP(r))
		if ip == nil {
			next.ServeHTTP(w, r)
			return
		}
		if ok, cidr := s.IPFilter.allowed(ip); !ok {
			s.IPFilter.hit(cidr)
			writeError(w, http.StatusForbidden, "forbidden", "该地址不允许访问")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// normalizeCIDR accepts "192.168.1.10" or "192.168.1.0/24" and returns the
// canonical network form.
func normalizeCIDR(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "/") {
		ip := net.ParseIP(raw)
		if ip == nil {
			return "", false
		}
		if ip.To4() != nil {
			raw += "/32"
		} else {
			raw += "/128"
		}
	}
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		return "", false
	}
	return network.String(), true
}
