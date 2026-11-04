package store

import (
	"context"
	"fmt"
	"strings"
)

// Notes are keyed by the case-folded relative path, matching how rule paths are
// stored: on a case-insensitive filesystem "HR/合同.pdf" and "hr/合同.pdf" are
// the same file and must not carry two different notes.

// NoteFor returns the note attached to one path, if any.
func (s *Store) NoteFor(ctx context.Context, shareID int64, relPath string) (string, bool, error) {
	var note string
	err := s.db.QueryRowContext(ctx,
		"SELECT note FROM notes WHERE share_id = ? AND rel_path = ?", shareID, strings.ToLower(relPath)).Scan(&note)
	if err != nil {
		if isNoRows(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read note: %w", err)
	}
	return note, true, nil
}

// NotesFor loads the notes of many paths at once, so a directory listing costs
// one query instead of one per row. Large path sets are chunked to stay well
// inside SQLite's bound-parameter limit.
func (s *Store) NotesFor(ctx context.Context, shareID int64, relPaths []string) (map[string]string, error) {
	out := make(map[string]string, len(relPaths))
	const chunk = 400
	for start := 0; start < len(relPaths); start += chunk {
		end := start + chunk
		if end > len(relPaths) {
			end = len(relPaths)
		}
		part := relPaths[start:end]
		placeholders := make([]string, len(part))
		args := make([]any, 0, len(part)+1)
		args = append(args, shareID)
		for i, p := range part {
			placeholders[i] = "?"
			args = append(args, strings.ToLower(p))
		}
		query := "SELECT rel_path, note FROM notes WHERE share_id = ? AND rel_path IN (" + strings.Join(placeholders, ",") + ")"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("read notes: %w", err)
		}
		for rows.Next() {
			var p, note string
			if err := rows.Scan(&p, &note); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan note: %w", err)
			}
			out[p] = note
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// SetNote stores (or clears, when note is empty) the note of one path.
func (s *Store) SetNote(ctx context.Context, shareID int64, relPath, note string, now int64) error {
	key := strings.ToLower(relPath)
	if strings.TrimSpace(note) == "" {
		if _, err := s.db.ExecContext(ctx,
			"DELETE FROM notes WHERE share_id = ? AND rel_path = ?", shareID, key); err != nil {
			return fmt.Errorf("clear note: %w", err)
		}
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO notes(share_id, rel_path, note, updated_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(share_id, rel_path) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at`,
		shareID, key, note, now); err != nil {
		return fmt.Errorf("write note: %w", err)
	}
	return nil
}

// MoveNotes re-attaches every note under fromRel to toRel, in one transaction.
// It runs as part of a rename/move so notes follow the file instead of going
// stale. Directory moves migrate the whole subtree.
func (s *Store) MoveNotes(ctx context.Context, shareID int64, fromRel, toRel string) error {
	from := strings.ToLower(fromRel)
	to := strings.ToLower(toRel)
	if from == to {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("move notes: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		"SELECT rel_path, note, updated_at FROM notes WHERE share_id = ? AND (rel_path = ? OR rel_path LIKE ?)",
		shareID, from, from+"/%")
	if err != nil {
		return fmt.Errorf("move notes: %w", err)
	}
	type row struct {
		path, note string
		updated    int64
	}
	var moves []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.path, &r.note, &r.updated); err != nil {
			rows.Close()
			return fmt.Errorf("move notes: %w", err)
		}
		moves = append(moves, r)
	}
	rows.Close()
	if len(moves) == 0 {
		return nil
	}

	for _, m := range moves {
		next := to + strings.TrimPrefix(m.path, from)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO notes(share_id, rel_path, note, updated_at) VALUES(?, ?, ?, ?)
			 ON CONFLICT(share_id, rel_path) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at`,
			shareID, next, m.note, m.updated); err != nil {
			return fmt.Errorf("move notes: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM notes WHERE share_id = ? AND rel_path = ?", shareID, m.path); err != nil {
			return fmt.Errorf("move notes: %w", err)
		}
	}
	return tx.Commit()
}
