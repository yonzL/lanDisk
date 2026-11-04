package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AdminUsername is the only account this product has. Everything else is an
// anonymous visitor (see goal-and-requirements.md "权限与访问控制").
const AdminUsername = "admin"

// SessionCookieMaxAge keeps the login cookie on disk so restarting the browser
// does not log the operator out. Sessions themselves never expire: an admin stays
// signed in until they press 退出登录 (or the credentials change).
const SessionCookieMaxAge = 365 * 24 * time.Hour

// User is an account row.
type User struct {
	ID         int64
	Username   string
	PassHash   string
	MustChange bool
}

// UserByUsername looks up an account.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	var mustChange int
	err := s.db.QueryRowContext(ctx,
		"SELECT id, username, pass_hash, must_change FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PassHash, &mustChange)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user %q: %w", username, err)
	}
	u.MustChange = mustChange != 0
	return u, nil
}

// SetPassword replaces an account's password hash and must-change flag, and
// drops every existing session for that account: a password rescue (or any
// change) must force a fresh login, otherwise a stolen cookie keeps working.
func (s *Store) SetPassword(ctx context.Context, userID int64, hash string, mustChange bool) error {
	flag := 0
	if mustChange {
		flag = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"UPDATE users SET pass_hash = ?, must_change = ? WHERE id = ?", hash, flag, userID); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("drop sessions after password change: %w", err)
	}
	return tx.Commit()
}

// CreateAdmin inserts the admin account with the given hash, reporting whether
// it was created (false when the account already exists).
func (s *Store) CreateAdmin(ctx context.Context, hash string, mustChange bool) (bool, error) {
	flag := 0
	if mustChange {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO users(username, pass_hash, must_change) VALUES(?, ?, ?) ON CONFLICT(username) DO NOTHING",
		AdminUsername, hash, flag)
	if err != nil {
		return false, fmt.Errorf("create admin: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("create admin: %w", err)
	}
	return n > 0, nil
}

// Session is a resolved login session.
type Session struct {
	Token      string
	UserID     int64
	CreatedAt  time.Time
	LastSeen   time.Time
	MustChange bool
}

// CreateSession stores a new session token for a user.
func (s *Store) CreateSession(ctx context.Context, token string, userID int64, now time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions(token, user_id, created_at, last_seen) VALUES(?, ?, ?, ?)",
		token, userID, now.Unix(), now.Unix()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// TouchSession validates a token and records the last request. There is no idle
// expiry: the admin stays signed in until an explicit logout.
func (s *Store) TouchSession(ctx context.Context, token string, now time.Time) (Session, bool, error) {
	var sess Session
	var created, lastSeen int64
	var mustChange int
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token, s.user_id, s.created_at, s.last_seen, u.must_change
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ?`, token).
		Scan(&sess.Token, &sess.UserID, &created, &lastSeen, &mustChange)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("read session: %w", err)
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.LastSeen = time.Unix(lastSeen, 0)
	sess.MustChange = mustChange != 0

	if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen = ? WHERE token = ?", now.Unix(), token); err != nil {
		return Session{}, false, fmt.Errorf("refresh session: %w", err)
	}
	sess.LastSeen = now
	return sess, true, nil
}

// DeleteSession removes one session (logout).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SingleAdmin returns the one account this product has, whatever it is called.
// The username is a login name the operator may change, so lookups by user id
// (not by the "admin" default) are what the rest of the app uses.
func (s *Store) SingleAdmin(ctx context.Context) (User, error) {
	var u User
	var mustChange int
	err := s.db.QueryRowContext(ctx,
		"SELECT id, username, pass_hash, must_change FROM users ORDER BY id LIMIT 1").
		Scan(&u.ID, &u.Username, &u.PassHash, &mustChange)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read admin: %w", err)
	}
	u.MustChange = mustChange != 0
	return u, nil
}

// SetCredentials replaces the login name and password hash together, and drops
// every session so whoever held the old name has to sign in again. Doing both in
// one transaction keeps the account from existing in a half-renamed state.
func (s *Store) SetCredentials(ctx context.Context, userID int64, username, hash string, mustChange bool) error {
	flag := 0
	if mustChange {
		flag = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set credentials: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"UPDATE users SET username = ?, pass_hash = ?, must_change = ? WHERE id = ?",
		username, hash, flag, userID); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: 用户名 %s 已被占用", ErrConflict, username)
		}
		return fmt.Errorf("set credentials: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("drop sessions after credential change: %w", err)
	}
	return tx.Commit()
}
