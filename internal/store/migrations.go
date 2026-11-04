package store

import (
	"context"
	"fmt"
)

// schemaVersion is the highest migration this binary knows. A database newer
// than this refuses to open rather than run against an unknown schema.
const schemaVersion = 3

// migrations[i] upgrades schema version i to i+1.
var migrations = []string{
	// v0 -> v1: the whole initial schema (see architecture.md "存储模型").
	`
CREATE TABLE settings(
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE shares(
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	name         TEXT    NOT NULL,
	virtual_path TEXT    NOT NULL UNIQUE,
	root         TEXT    NOT NULL,
	sort         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE share_mounts(
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	share_id INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	rel_path TEXT    NOT NULL,
	target   TEXT    NOT NULL,
	added_at INTEGER NOT NULL
);
CREATE INDEX idx_share_mounts_share ON share_mounts(share_id);

CREATE TABLE share_rules(
	share_id INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	rel_path TEXT    NOT NULL,
	perm     TEXT    NOT NULL CHECK(perm IN ('none','r','rw')),
	PRIMARY KEY(share_id, rel_path)
);

CREATE TABLE notes(
	share_id   INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	rel_path   TEXT    NOT NULL,
	note       TEXT    NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY(share_id, rel_path)
);

CREATE TABLE users(
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	username    TEXT    NOT NULL UNIQUE,
	pass_hash   TEXT    NOT NULL,
	must_change INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE sessions(
	token      TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL
);

CREATE TABLE uploads(
	id         TEXT PRIMARY KEY,
	share_id   INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	rel_path   TEXT    NOT NULL,
	size       INTEGER NOT NULL,
	chunk      INTEGER NOT NULL,
	bitmap     BLOB    NOT NULL,
	part_path  TEXT    NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE TABLE trash(
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	share_id        INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	orig_rel_path   TEXT    NOT NULL,
	trash_rel_path  TEXT    NOT NULL,
	deleted_at      INTEGER NOT NULL,
	ip              TEXT    NOT NULL
);

CREATE TABLE ip_rules(
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	cidr        TEXT    NOT NULL UNIQUE,
	kind        TEXT    NOT NULL CHECK(kind IN ('allow','deny')),
	note        TEXT    NOT NULL DEFAULT '',
	hits        INTEGER NOT NULL DEFAULT 0,
	last_hit_at INTEGER,
	created_at  INTEGER NOT NULL
);

CREATE TABLE hosts(
	ip         TEXT PRIMARY KEY,
	hostname   TEXT NOT NULL,
	source     TEXT NOT NULL CHECK(source IN ('manual','dns')),
	note       TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
);

CREATE TABLE logs(
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	ts       INTEGER NOT NULL,
	ip       TEXT    NOT NULL,
	host     TEXT    NOT NULL DEFAULT '',
	action   TEXT    NOT NULL,
	share_id INTEGER,
	path     TEXT    NOT NULL DEFAULT '',
	result   TEXT    NOT NULL,
	detail   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_logs_ts        ON logs(ts);
CREATE INDEX idx_logs_ip_ts     ON logs(ip, ts);
CREATE INDEX idx_logs_action_ts ON logs(action, ts);
`,

	// v1 -> v2: track when an upload last received data, so a slow but active
	// upload is not purged at a fixed age (created_at) while chunks still arrive.
	`
ALTER TABLE uploads ADD COLUMN last_seen INTEGER NOT NULL DEFAULT 0;
UPDATE uploads SET last_seen = created_at;
`,

	// v2 -> v3: chunk progress as rows instead of one blob. A single
	// INSERT OR IGNORE is atomic, so concurrent chunk PUTs on one session cannot
	// lose each other's bits (and no read-modify-write transaction is needed).
	`
CREATE TABLE upload_chunks(
	upload_id   TEXT    NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
	chunk_index INTEGER NOT NULL,
	PRIMARY KEY(upload_id, chunk_index)
);
`,
}

// migrate brings the database to schemaVersion, one version per transaction so
// a failed upgrade leaves the previous version intact.
func (s *Store) migrate() error {
	ctx := context.Background()

	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("database schema v%d is newer than this build (v%d)", current, schemaVersion)
	}
	for v := current; v < schemaVersion; v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration to v%d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate to v%d: %w", v+1, err)
		}
		// PRAGMA does not accept placeholders.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("bump schema version to v%d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration to v%d: %w", v+1, err)
		}
	}
	return s.ensureSettings(ctx)
}
