package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Perm is a directory permission level. "none" hides a subtree from anonymous
// visitors; "r" is read-only; "rw" additionally allows upload and mkdir.
type Perm string

// Permission values.
const (
	PermNone  Perm = "none"
	PermRead  Perm = "r"
	PermWrite Perm = "rw"
)

// ValidPerm reports whether p is one of the three permission levels.
func ValidPerm(p Perm) bool {
	return p == PermNone || p == PermRead || p == PermWrite
}

// Share is a shared directory mapped to a virtual path.
type Share struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	VirtualPath string `json:"virtual_path"`
	Root        string `json:"root"`
	Sort        int    `json:"sort"`
}

// Rule sets the permission for one directory subtree inside a share. The empty
// RelPath is the share's root rule, which is also what decides whether an
// anonymous visitor can see the share at all.
type Rule struct {
	ShareID int64  `json:"share_id"`
	RelPath string `json:"rel_path"`
	Perm    Perm   `json:"perm"`
}

// ListShares returns every share ordered by sort then name.
func (s *Store) ListShares(ctx context.Context) ([]Share, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, virtual_path, root, sort FROM shares ORDER BY sort, name")
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer rows.Close()

	var out []Share
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.ID, &sh.Name, &sh.VirtualPath, &sh.Root, &sh.Sort); err != nil {
			return nil, fmt.Errorf("scan share: %w", err)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// ShareByID loads one share.
func (s *Store) ShareByID(ctx context.Context, id int64) (Share, error) {
	var sh Share
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, virtual_path, root, sort FROM shares WHERE id = ?", id).
		Scan(&sh.ID, &sh.Name, &sh.VirtualPath, &sh.Root, &sh.Sort)
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	if err != nil {
		return Share{}, fmt.Errorf("read share %d: %w", id, err)
	}
	return sh, nil
}

// CreateShare inserts a share together with its root rule. Both happen in one
// transaction: a share without a root rule would be invisible to anonymous
// visitors and could never be configured.
func (s *Store) CreateShare(ctx context.Context, sh Share, rootPerm Perm) (Share, error) {
	if !ValidPerm(rootPerm) {
		return Share{}, fmt.Errorf("非法权限 %q", rootPerm)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, fmt.Errorf("create share: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		"INSERT INTO shares(name, virtual_path, root, sort) VALUES(?, ?, ?, ?)",
		sh.Name, sh.VirtualPath, sh.Root, sh.Sort)
	if err != nil {
		if isUniqueViolation(err) {
			return Share{}, fmt.Errorf("%w: 虚拟路径 %s 已被占用", ErrConflict, sh.VirtualPath)
		}
		return Share{}, fmt.Errorf("create share: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Share{}, fmt.Errorf("create share: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO share_rules(share_id, rel_path, perm) VALUES(?, '', ?)", id, rootPerm); err != nil {
		return Share{}, fmt.Errorf("create root rule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Share{}, fmt.Errorf("create share: %w", err)
	}
	sh.ID = id
	return sh, nil
}

// UpdateShare changes the display fields of a share. The root directory is
// immutable: moving a share to another disk would leave its notes, rules and
// recycle bin pointing at the wrong place.
func (s *Store) UpdateShare(ctx context.Context, sh Share) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE shares SET name = ?, virtual_path = ?, sort = ? WHERE id = ?",
		sh.Name, sh.VirtualPath, sh.Sort, sh.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: 虚拟路径 %s 已被占用", ErrConflict, sh.VirtualPath)
		}
		return fmt.Errorf("update share: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update share: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteShare removes a share and its rules (notes/rules cascade). Shared files
// on disk are never touched.
func (s *Store) DeleteShare(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM shares WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Rules returns every rule of a share (root rule first).
func (s *Store) Rules(ctx context.Context, shareID int64) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT share_id, rel_path, perm FROM share_rules WHERE share_id = ? ORDER BY length(rel_path), rel_path", shareID)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()

	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ShareID, &r.RelPath, &r.Perm); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRule upserts one rule. The path is stored case-folded, matching how
// perms.Compile compares rules on a case-insensitive filesystem: otherwise
// "hr" and "HR" would become two rows and only one of them would ever apply.
func (s *Store) SetRule(ctx context.Context, r Rule) error {
	if !ValidPerm(r.Perm) {
		return fmt.Errorf("非法权限 %q", r.Perm)
	}
	r.RelPath = strings.ToLower(r.RelPath)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO share_rules(share_id, rel_path, perm) VALUES(?, ?, ?)
		 ON CONFLICT(share_id, rel_path) DO UPDATE SET perm = excluded.perm`,
		r.ShareID, r.RelPath, r.Perm)
	if err != nil {
		return fmt.Errorf("write rule: %w", err)
	}
	return nil
}

// DeleteRule removes a non-root rule. Removing the root rule would make the
// share unconfigurable, so it is rejected.
func (s *Store) DeleteRule(ctx context.Context, shareID int64, relPath string) error {
	if relPath == "" {
		return errors.New("根规则的权限请直接修改，不能删除")
	}
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM share_rules WHERE share_id = ? AND rel_path = ?", shareID, strings.ToLower(relPath))
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation recognises SQLite's UNIQUE constraint failure without
// importing driver-specific error types into every caller.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
