package store

import (
	"context"
	"fmt"
	"time"
)

// IPRule is one allow/deny entry, keyed by a CIDR (single IPs are stored /32).
type IPRule struct {
	ID        int64
	CIDR      string
	Kind      string // allow | deny
	Note      string
	Hits      int64
	LastHitAt time.Time
	CreatedAt time.Time
}

// ListIPRules returns every rule, allow and deny together.
func (s *Store) ListIPRules(ctx context.Context) ([]IPRule, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, cidr, kind, note, hits, COALESCE(last_hit_at, 0), created_at FROM ip_rules ORDER BY kind, cidr")
	if err != nil {
		return nil, fmt.Errorf("list ip rules: %w", err)
	}
	defer rows.Close()
	var out []IPRule
	for rows.Next() {
		var r IPRule
		var lastHit, created int64
		if err := rows.Scan(&r.ID, &r.CIDR, &r.Kind, &r.Note, &r.Hits, &lastHit, &created); err != nil {
			return nil, fmt.Errorf("scan ip rule: %w", err)
		}
		if lastHit > 0 {
			r.LastHitAt = time.Unix(lastHit, 0)
		}
		r.CreatedAt = time.Unix(created, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertIPRule writes or replaces the rule for a CIDR.
func (s *Store) UpsertIPRule(ctx context.Context, cidr, kind, note string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ip_rules(cidr, kind, note, hits, created_at) VALUES(?, ?, ?, 0, ?)
		ON CONFLICT(cidr) DO UPDATE SET kind = excluded.kind, note = excluded.note`,
		cidr, kind, note, now.Unix())
	if err != nil {
		return fmt.Errorf("write ip rule: %w", err)
	}
	return nil
}

// DeleteIPRule removes one rule by CIDR.
func (s *Store) DeleteIPRule(ctx context.Context, cidr string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM ip_rules WHERE cidr = ?", cidr); err != nil {
		return fmt.Errorf("delete ip rule: %w", err)
	}
	return nil
}

// BumpIPRuleHits persists rejection counters (batched by the caller).
func (s *Store) BumpIPRuleHits(ctx context.Context, cidr string, hits int64, lastHit time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE ip_rules SET hits = hits + ?, last_hit_at = ? WHERE cidr = ?", hits, lastHit.Unix(), cidr); err != nil {
		return fmt.Errorf("bump ip rule hits: %w", err)
	}
	return nil
}
