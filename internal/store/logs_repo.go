package store

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// LogEntry is one recorded operation. Host is the snapshot taken at write time;
// queries join the hosts table so a later curation applies to old rows too.
type LogEntry struct {
	ID      int64
	TS      time.Time
	IP      string
	Host    string // resolved at query time when a host mapping exists
	Action  string
	ShareID int64 // 0 = not share-scoped
	Path    string
	Result  string
	Detail  string
}

// InsertLogs writes a batch of entries in one transaction.
func (s *Store) InsertLogs(ctx context.Context, entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert logs: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO logs(ts, ip, host, action, share_id, path, result, detail) VALUES(?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("insert logs: %w", err)
	}
	defer stmt.Close()
	for _, e := range entries {
		var shareID any
		if e.ShareID > 0 {
			shareID = e.ShareID
		}
		if _, err := stmt.ExecContext(ctx, e.TS.Unix(), e.IP, e.Host, e.Action, shareID, e.Path, e.Result, e.Detail); err != nil {
			return fmt.Errorf("insert log: %w", err)
		}
	}
	return tx.Commit()
}

// LogFilter narrows a log query.
type LogFilter struct {
	From     time.Time
	To       time.Time
	IP       string
	Action   string
	Page     int
	PageSize int
}

// QueryLogs returns a page of entries plus the total matching count. Rows join
// the hosts table so a hostname curated today shows up on yesterday's rows.
func (s *Store) QueryLogs(ctx context.Context, f LogFilter) ([]LogEntry, int, error) {
	where, args := logWhere(f)

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs l "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count logs: %w", err)
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	size := f.PageSize
	if size < 1 {
		size = 100
	}
	if size > 500 {
		size = 500
	}

	query := "SELECT l.id, l.ts, l.ip, COALESCE(h.hostname, l.host), l.action, COALESCE(l.share_id, 0), l.path, l.result, l.detail " +
		"FROM logs l LEFT JOIN hosts h ON h.ip = l.ip " + where + " ORDER BY l.ts DESC, l.id DESC LIMIT ? OFFSET ?"
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.IP, &e.Host, &e.Action, &e.ShareID, &e.Path, &e.Result, &e.Detail); err != nil {
			return nil, 0, fmt.Errorf("scan log: %w", err)
		}
		e.TS = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// StreamLogs calls fn for every matching entry, newest first, for CSV export.
func (s *Store) StreamLogs(ctx context.Context, f LogFilter, fn func(LogEntry) error) error {
	where, args := logWhere(f)
	query := "SELECT l.id, l.ts, l.ip, COALESCE(h.hostname, l.host), l.action, COALESCE(l.share_id, 0), l.path, l.result, l.detail " +
		"FROM logs l LEFT JOIN hosts h ON h.ip = l.ip " + where + " ORDER BY l.ts DESC, l.id DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("export logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e LogEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.IP, &e.Host, &e.Action, &e.ShareID, &e.Path, &e.Result, &e.Detail); err != nil {
			return fmt.Errorf("export logs: %w", err)
		}
		e.TS = time.Unix(ts, 0)
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// PurgeLogs deletes entries older than the retention window in bounded batches
// so a long-running install never takes a big write lock.
func (s *Store) PurgeLogs(ctx context.Context, olderThan time.Time) (int64, error) {
	cutoff := olderThan.Unix()
	var removed int64
	for {
		res, err := s.db.ExecContext(ctx,
			"DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE ts < ? LIMIT 5000)", cutoff)
		if err != nil {
			return removed, fmt.Errorf("purge logs: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return removed, nil
		}
		removed += n
		if n < 5000 {
			return removed, nil
		}
	}
}

func logWhere(f LogFilter) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	// Columns are qualified because the query joins the hosts table, which has
	// its own ip column.
	if !f.From.IsZero() {
		clauses = append(clauses, "l.ts >= ?")
		args = append(args, f.From.Unix())
	}
	if !f.To.IsZero() {
		clauses = append(clauses, "l.ts <= ?")
		args = append(args, f.To.Unix())
	}
	if f.IP != "" {
		clauses = append(clauses, "l.ip = ?")
		args = append(args, f.IP)
	}
	if f.Action != "" {
		clauses = append(clauses, "l.action = ?")
		args = append(args, f.Action)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

// DecodeLogPaths rewrites historical rows whose path still contains a
// percent-encoded query (written before the audit middleware decoded targets).
// It runs once at startup behind a settings flag and returns how many rows moved.
func (s *Store) DecodeLogPaths(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, path FROM logs WHERE instr(path, '?') > 0 AND instr(path, '%') > 0")
	if err != nil {
		return 0, fmt.Errorf("scan encoded log paths: %w", err)
	}
	type pending struct {
		id   int64
		path string
	}
	var updates []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.path); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan encoded log path: %w", err)
		}
		updates = append(updates, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("decode log paths: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE logs SET path = ? WHERE id = ?")
	if err != nil {
		return 0, fmt.Errorf("decode log paths: %w", err)
	}
	defer stmt.Close()
	changed := 0
	for _, u := range updates {
		decoded := decodeTargetString(u.path)
		if decoded == u.path {
			continue
		}
		if _, err := stmt.ExecContext(ctx, decoded, u.id); err != nil {
			return 0, fmt.Errorf("decode log path %d: %w", u.id, err)
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("decode log paths: %w", err)
	}
	return changed, nil
}

// decodeTargetString decodes the query half of a "/api/...?a=b" target.
func decodeTargetString(target string) string {
	i := strings.IndexByte(target, '?')
	if i < 0 {
		return target
	}
	values, err := url.ParseQuery(target[i+1:])
	if err != nil {
		return target
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, key+"="+value)
		}
	}
	return target[:i+1] + strings.Join(parts, "&")
}

// SettingKeyLogPathsDecoded marks the one-time decode pass as done.
const SettingKeyLogPathsDecoded = "logs_paths_decoded_v1"
