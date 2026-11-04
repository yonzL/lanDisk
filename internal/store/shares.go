package store

import (
	"context"
	"fmt"
)

// CountShares reports how many shared directories are configured. The health
// probe surfaces it so an operator can tell "running, nothing shared" from
// "running, misconfigured".
func (s *Store) CountShares(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM shares").Scan(&n); err != nil {
		return 0, fmt.Errorf("count shares: %w", err)
	}
	return n, nil
}
