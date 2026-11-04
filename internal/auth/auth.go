// Package auth owns password hashing, session tokens, and login throttling.
// It deliberately has no HTTP types: httpapi decides when to call it.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"land/internal/store"
)

// ErrLockedOut is returned while a client is in a login back-off window.
var ErrLockedOut = errors.New("auth: too many failed attempts")

// MaxFailures and LockoutWindow bound brute force on the single admin account.
const (
	MaxFailures   = 5
	LockoutWindow = 60 * time.Second
)

// HashPassword produces a bcrypt hash.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword verifies a password against a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// RandomPassword returns a short, typeable password for `reset-password`.
func RandomPassword() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		panic("auth: no entropy: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)[:16]
}

// NewToken returns an unguessable session token.
func NewToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("auth: no entropy: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// BootstrapAdmin creates the admin account on first start. The password is
// random and never printed or written to disk: the operator obtains one with
// `land.exe reset-password` (see architecture.md "启动顺序与引导").
func BootstrapAdmin(ctx context.Context, st *store.Store) (bool, error) {
	// Any existing account means this is not a first start: the operator may have
	// renamed it, so checking for "admin" by name would create a second account.
	if _, err := st.SingleAdmin(ctx); err == nil {
		return false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	hash, err := HashPassword(RandomPassword())
	if err != nil {
		return false, err
	}
	return st.CreateAdmin(ctx, hash, true)
}

// Throttle tracks failed logins per client IP. In-memory on purpose: a restart
// clearing the counters is acceptable for a LAN tool, and it keeps the database
// out of the login hot path.
type Throttle struct {
	mu      sync.Mutex
	fails   map[string]int
	blocked map[string]time.Time
	now     func() time.Time
}

// NewThrottle builds an empty throttle.
func NewThrottle() *Throttle {
	return &Throttle{fails: map[string]int{}, blocked: map[string]time.Time{}, now: time.Now}
}

// Allowed reports whether ip may attempt a login now.
func (t *Throttle) Allowed(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	until, ok := t.blocked[ip]
	if !ok {
		return true
	}
	if t.now().Before(until) {
		return false
	}
	delete(t.blocked, ip)
	delete(t.fails, ip)
	return true
}

// Fail records a failed attempt and starts a lockout once the limit is hit.
func (t *Throttle) Fail(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fails[ip]++
	if t.fails[ip] >= MaxFailures {
		t.blocked[ip] = t.now().Add(LockoutWindow)
	}
}

// Success clears a client's failure counter.
func (t *Throttle) Success(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, ip)
	delete(t.blocked, ip)
}
