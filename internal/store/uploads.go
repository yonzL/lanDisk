package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Upload is an in-flight chunked upload. The bit map lives in the database so a
// restart does not lose progress; the bytes live in a hidden part file next to
// the destination (never in the program directory).
type Upload struct {
	ID        string
	ShareID   int64
	RelPath   string // final destination, relative to the share root
	Size      int64
	Chunk     int64
	Bitmap    []byte
	PartPath  string // absolute path of the hidden *.landpart-<id> file
	CreatedAt time.Time
	LastSeen  time.Time
}

// CreateUpload records a new upload session.
func (s *Store) CreateUpload(ctx context.Context, u Upload) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO uploads(id, share_id, rel_path, size, chunk, bitmap, part_path, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.ShareID, u.RelPath, u.Size, u.Chunk, u.Bitmap, u.PartPath, u.CreatedAt.Unix()); err != nil {
		return fmt.Errorf("create upload: %w", err)
	}
	return nil
}

// UploadByID loads an upload session.
func (s *Store) UploadByID(ctx context.Context, id string) (Upload, error) {
	var u Upload
	var created, lastSeen int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id, share_id, rel_path, size, chunk, bitmap, part_path, created_at, last_seen FROM uploads WHERE id = ?", id).
		Scan(&u.ID, &u.ShareID, &u.RelPath, &u.Size, &u.Chunk, &u.Bitmap, &u.PartPath, &created, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("read upload: %w", err)
	}
	u.CreatedAt = time.Unix(created, 0)
	u.LastSeen = time.Unix(lastSeen, 0)
	return u, nil
}

// SetUploadBitmap persists chunk progress after a chunk lands.
func (s *Store) SetUploadBitmap(ctx context.Context, id string, bitmap []byte) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE uploads SET bitmap = ? WHERE id = ?", bitmap, id); err != nil {
		return fmt.Errorf("update upload bitmap: %w", err)
	}
	return nil
}

// DeleteUpload removes a finished (or abandoned) session.
func (s *Store) DeleteUpload(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM uploads WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete upload: %w", err)
	}
	return nil
}

// ExpiredUploads returns sessions older than cutoff, so their part files can be
// removed before the rows are dropped.
func (s *Store) ExpiredUploads(ctx context.Context, cutoff time.Time) ([]Upload, error) {
	// last_seen, not created_at: a slow upload that keeps receiving chunks must
	// not be purged mid-flight (it would force the client to restart from zero).
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, share_id, rel_path, size, chunk, bitmap, part_path, created_at, last_seen FROM uploads WHERE max(last_seen, created_at) < ?",
		cutoff.Unix())
	if err != nil {
		return nil, fmt.Errorf("list expired uploads: %w", err)
	}
	defer rows.Close()

	var out []Upload
	for rows.Next() {
		var u Upload
		var created, lastSeen int64
		if err := rows.Scan(&u.ID, &u.ShareID, &u.RelPath, &u.Size, &u.Chunk, &u.Bitmap, &u.PartPath, &created, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		u.CreatedAt = time.Unix(created, 0)
		u.LastSeen = time.Unix(lastSeen, 0)
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkUploadChunk records one received chunk. A single INSERT OR IGNORE is
// atomic, so two concurrent chunk PUTs on the same session can never lose each
// other's progress (a blob read-modify-write would, and its transaction upgrade
// also races into SQLITE_BUSY).
func (s *Store) MarkUploadChunk(ctx context.Context, id string, index int64, now time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO upload_chunks(upload_id, chunk_index) VALUES(?, ?)", id, index); err != nil {
		return fmt.Errorf("mark chunk: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE uploads SET last_seen = ? WHERE id = ?", now.Unix(), id); err != nil {
		return fmt.Errorf("mark chunk: %w", err)
	}
	return nil
}

// ReceivedChunks lists the chunk indices stored for an upload, ascending.
func (s *Store) ReceivedChunks(ctx context.Context, id string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT chunk_index FROM upload_chunks WHERE upload_id = ? ORDER BY chunk_index", id)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var idx int64
		if err := rows.Scan(&idx); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// UploadsByShare lists a share's upload sessions (used to delete their part
// files before the share itself disappears).
func (s *Store) UploadsByShare(ctx context.Context, shareID int64) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, share_id, rel_path, size, chunk, bitmap, part_path, created_at, last_seen FROM uploads WHERE share_id = ?", shareID)
	if err != nil {
		return nil, fmt.Errorf("list share uploads: %w", err)
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		var u Upload
		var created, lastSeen int64
		if err := rows.Scan(&u.ID, &u.ShareID, &u.RelPath, &u.Size, &u.Chunk, &u.Bitmap, &u.PartPath, &created, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		u.CreatedAt = time.Unix(created, 0)
		u.LastSeen = time.Unix(lastSeen, 0)
		out = append(out, u)
	}
	return out, rows.Err()
}
