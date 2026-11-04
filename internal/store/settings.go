package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Setting keys. Every runtime parameter lives in the settings table; there is
// no configuration file (see architecture.md "启动顺序与引导").
const (
	KeyPort               = "port"
	KeyLogRetentionDays   = "log_retention_days"
	KeyTrashRetentionDays = "trash_retention_days"
	KeyMaxConns           = "max_conns"
	KeyDownloadRateKbps   = "download_rate_limit_kbps"
	KeyUploadRateKbps     = "upload_rate_limit_kbps"
	KeySiteTitle          = "site_title"
	KeySiteNotice         = "site_notice"
)

// defaults are the values written on first start. Default port 80 keeps the
// shared address as short as possible; retention defaults follow the product
// spec (90 days for logs and for the recycle bin, independently).
var defaults = map[string]string{
	KeyPort:               "80",
	KeyLogRetentionDays:   "90",
	KeyTrashRetentionDays: "90",
	KeyMaxConns:           "64",
	KeyDownloadRateKbps:   "0",
	KeyUploadRateKbps:     "0",
	KeySiteTitle:          "lan disk",
	KeySiteNotice:         "",
}

// ensureSettings inserts missing defaults without touching existing values, so
// upgrading a database never resets an operator's choices.
func (s *Store) ensureSettings(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings bootstrap: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)")
	if err != nil {
		return fmt.Errorf("prepare settings bootstrap: %w", err)
	}
	defer stmt.Close()
	for k, v := range defaults {
		if _, err := stmt.ExecContext(ctx, k, v); err != nil {
			return fmt.Errorf("seed setting %q: %w", k, err)
		}
	}
	return tx.Commit()
}

// Setting reads one setting, reporting whether the row exists.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %q: %w", key, err)
	}
	return v, true, nil
}

// SettingInt reads one setting as an integer. A missing row falls back to def;
// otherwise the read side enforces exactly the same invariant as the write
// side — silently using the default would bind the wrong port (or the wrong
// retention) with nothing to alert anyone.
func (s *Store) SettingInt(ctx context.Context, key string, def int) (int, error) {
	v, ok, err := s.Setting(ctx, key)
	if err != nil || !ok {
		return def, err
	}
	n, validateErr := validateInt(key, v)
	if validateErr != nil {
		return def, fmt.Errorf("配置项 %s 不合法，请用 lanDisk.exe config set %s <整数> 修正：%w", key, key, validateErr)
	}
	return n, nil
}

// integerKeys lists the settings whose values must parse as non-negative
// integers; SetSetting rejects anything else so a typo cannot be stored.
var integerKeys = map[string]bool{
	KeyPort:               true,
	KeyLogRetentionDays:   true,
	KeyTrashRetentionDays: true,
	KeyMaxConns:           true,
	KeyDownloadRateKbps:   true,
	KeyUploadRateKbps:     true,
}

// validateInt is the single range/format check shared by the write path
// (SetSetting) and the read path (SettingInt), so a hand-edited database
// cannot slip past the same rules the CLI enforces.
func validateInt(key, value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s 需要整数，收到 %q", key, value)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s 不能为负数：%d", key, n)
	}
	if key == KeyPort && (n < 1 || n > 65535) {
		return 0, fmt.Errorf("端口必须在 1-65535 之间，收到 %d", n)
	}
	return n, nil
}

// SetSetting writes one setting. Both the admin API and `lanDisk.exe config set`
// funnel through here, so the two writers cannot drift apart.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if integerKeys[key] {
		n, err := validateInt(key, value)
		if err != nil {
			return err
		}
		value = strconv.Itoa(n)
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

// Settings returns every setting, defaults included.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, len(defaults))
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	return out, nil
}

// KnownSetting reports whether key is one of the parameters this build owns;
// it guards `lanDisk.exe config set` from inventing keys by typo.
func KnownSetting(key string) bool {
	_, ok := defaults[key]
	return ok
}
