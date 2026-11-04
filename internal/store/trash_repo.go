package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TrashEntry is one deleted entry. TrashDir is the share-relative directory the
// entry was moved into (always ".land-trash"); TrashPath is its name there.
type TrashEntry struct {
	ID           int64
	ShareID      int64
	OrigRelPath  string
	TrashRelPath string
	DeletedAt    time.Time
	IP           string
}

// CreateTrash records a deletion, returning the row id used to build the
// on-disk name (so a file's trash name never has to be guessed from a timestamp).
func (s *Store) CreateTrash(ctx context.Context, shareID int64, origRel, trashRel, ip string, deletedAt time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO trash(share_id, orig_rel_path, trash_rel_path, deleted_at, ip) VALUES(?, ?, ?, ?, ?)`,
		shareID, origRel, trashRel, deletedAt.Unix(), ip)
	if err != nil {
		return 0, fmt.Errorf("create trash entry: %w", err)
	}
	return res.LastInsertId()
}

// TrashByID loads one entry.
func (s *Store) TrashByID(ctx context.Context, id int64) (TrashEntry, error) {
	var t TrashEntry
	var deleted int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id, share_id, orig_rel_path, trash_rel_path, deleted_at, ip FROM trash WHERE id = ?", id).
		Scan(&t.ID, &t.ShareID, &t.OrigRelPath, &t.TrashRelPath, &deleted, &t.IP)
	if errors.Is(err, sql.ErrNoRows) {
		return TrashEntry{}, ErrNotFound
	}
	if err != nil {
		return TrashEntry{}, fmt.Errorf("read trash entry: %w", err)
	}
	t.DeletedAt = time.Unix(deleted, 0)
	return t, nil
}

// ListTrash returns entries newest first, optionally for one share (0 = all).
func (s *Store) ListTrash(ctx context.Context, shareID int64) ([]TrashEntry, error) {
	query := "SELECT id, share_id, orig_rel_path, trash_rel_path, deleted_at, ip FROM trash"
	args := []any{}
	if shareID > 0 {
		query += " WHERE share_id = ?"
		args = append(args, shareID)
	}
	query += " ORDER BY deleted_at DESC, id DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trash: %w", err)
	}
	defer rows.Close()

	var out []TrashEntry
	for rows.Next() {
		var t TrashEntry
		var deleted int64
		if err := rows.Scan(&t.ID, &t.ShareID, &t.OrigRelPath, &t.TrashRelPath, &deleted, &t.IP); err != nil {
			return nil, fmt.Errorf("scan trash: %w", err)
		}
		t.DeletedAt = time.Unix(deleted, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTrashEntry removes one row (after its file is gone or restored).
func (s *Store) DeleteTrashEntry(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM trash WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete trash entry: %w", err)
	}
	return nil
}

// ExpiredTrash returns entries older than cutoff.
func (s *Store) ExpiredTrash(ctx context.Context, cutoff time.Time) ([]TrashEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, share_id, orig_rel_path, trash_rel_path, deleted_at, ip FROM trash WHERE deleted_at < ?", cutoff.Unix())
	if err != nil {
		return nil, fmt.Errorf("list expired trash: %w", err)
	}
	defer rows.Close()
	var out []TrashEntry
	for rows.Next() {
		var t TrashEntry
		var deleted int64
		if err := rows.Scan(&t.ID, &t.ShareID, &t.OrigRelPath, &t.TrashRelPath, &deleted, &t.IP); err != nil {
			return nil, fmt.Errorf("scan trash: %w", err)
		}
		t.DeletedAt = time.Unix(deleted, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetTrashPath fills in the on-disk name once the move has happened.
func (s *Store) SetTrashPath(ctx context.Context, id int64, trashRel string) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE trash SET trash_rel_path = ? WHERE id = ?", trashRel, id); err != nil {
		return fmt.Errorf("update trash path: %w", err)
	}
	return nil
}
