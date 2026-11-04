package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Host is an IP → hostname mapping. Manual rows are the operator's curation and
// are never overwritten by reverse-DNS results.
type Host struct {
	IP       string
	Hostname string
	Source   string // manual | dns
	Note     string
	Updated  time.Time
}

// HostByIP looks up one mapping.
func (s *Store) HostByIP(ctx context.Context, ip string) (Host, error) {
	var h Host
	var updated int64
	err := s.db.QueryRowContext(ctx,
		"SELECT ip, hostname, source, note, updated_at FROM hosts WHERE ip = ?", ip).
		Scan(&h.IP, &h.Hostname, &h.Source, &h.Note, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrNotFound
	}
	if err != nil {
		return Host{}, fmt.Errorf("read host: %w", err)
	}
	h.Updated = time.Unix(updated, 0)
	return h, nil
}

// ListHosts returns every mapping.
func (s *Store) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT ip, hostname, source, note, updated_at FROM hosts ORDER BY ip")
	if err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		var h Host
		var updated int64
		if err := rows.Scan(&h.IP, &h.Hostname, &h.Source, &h.Note, &updated); err != nil {
			return nil, fmt.Errorf("scan host: %w", err)
		}
		h.Updated = time.Unix(updated, 0)
		out = append(out, h)
	}
	return out, rows.Err()
}

// SetHost upserts a mapping with the given source.
func (s *Store) SetHost(ctx context.Context, h Host, now time.Time) error {
	if h.Source == "" {
		h.Source = "manual"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hosts(ip, hostname, source, note, updated_at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET hostname = excluded.hostname, source = excluded.source,
			note = excluded.note, updated_at = excluded.updated_at`,
		h.IP, h.Hostname, h.Source, h.Note, now.Unix())
	if err != nil {
		return fmt.Errorf("write host: %w", err)
	}
	return nil
}

// SetHostFromDNS records a reverse-DNS result, but only when no manual row
// exists: the operator's curation always wins.
func (s *Store) SetHostFromDNS(ctx context.Context, ip, hostname string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hosts(ip, hostname, source, note, updated_at) VALUES(?, ?, 'dns', '', ?)
		ON CONFLICT(ip) DO UPDATE SET hostname = excluded.hostname, source = 'dns',
			updated_at = excluded.updated_at
		WHERE hosts.source = 'dns'`,
		ip, hostname, now.Unix())
	if err != nil {
		return fmt.Errorf("write dns host: %w", err)
	}
	return nil
}

// DeleteHost removes a mapping.
func (s *Store) DeleteHost(ctx context.Context, ip string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM hosts WHERE ip = ?", ip); err != nil {
		return fmt.Errorf("delete host: %w", err)
	}
	return nil
}
