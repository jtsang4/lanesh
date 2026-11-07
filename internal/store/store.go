// Package store persists Lanesh server state in SQLite.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // SQLite driver (pure Go).
)

// Errors returned by the store.
var (
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when joining over an active membership without
	// proving possession of its current credential.
	ErrConflict = errors.New("conflict")
)

// Store wraps the SQLite database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

var migrations = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	CREATE TABLE lanes (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE keys (
		id TEXT PRIMARY KEY,
		lane_id TEXT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		hash TEXT NOT NULL UNIQUE,
		prefix TEXT NOT NULL,
		expires_at INTEGER,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER,
		revoked_at INTEGER
	);
	CREATE INDEX keys_lane ON keys(lane_id);
	CREATE TABLE memberships (
		lane_id TEXT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
		machine_id TEXT NOT NULL,
		display_name TEXT NOT NULL,
		credential_hash TEXT NOT NULL UNIQUE,
		os TEXT NOT NULL DEFAULT '',
		arch TEXT NOT NULL DEFAULT '',
		hostname TEXT NOT NULL DEFAULT '',
		version TEXT NOT NULL DEFAULT '',
		expose_skills INTEGER NOT NULL,
		allow_exec INTEGER NOT NULL,
		joined_at INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		revoked_at INTEGER,
		PRIMARY KEY (lane_id, machine_id)
	);
	CREATE TABLE skill_snapshots (
		lane_id TEXT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
		machine_id TEXT NOT NULL,
		index_json TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (lane_id, machine_id)
	);
	CREATE TABLE skill_history (
		lane_id TEXT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
		machine_id TEXT NOT NULL,
		hash TEXT NOT NULL,
		index_json TEXT NOT NULL,
		seen_at INTEGER NOT NULL,
		PRIMARY KEY (lane_id, machine_id, hash)
	);
	CREATE INDEX skill_history_recent ON skill_history(lane_id, machine_id, seen_at);
	CREATE TABLE admin_sessions (
		hash TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE TABLE audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		lane_id TEXT NOT NULL,
		actor TEXT NOT NULL,
		action TEXT NOT NULL,
		machine_id TEXT NOT NULL,
		detail TEXT NOT NULL,
		result TEXT NOT NULL
	);
	CREATE INDEX audit_lane_ts ON audit_log(lane_id, ts);`,
}

// Open opens (creating if needed) the database in dataDir and migrates it.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + filepath.Join(dataDir, "lanesh.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	return open(ctx, dsn)
}

// OpenMemory opens an in-memory database, for tests.
func OpenMemory(ctx context.Context) (*Store, error) {
	return open(ctx, "file::memory:?_pragma=foreign_keys(1)")
}

func open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection serializes writes and keeps in-memory databases shared.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) unix() int64 { return s.now().Unix() }

// Secret returns a persistent random secret called name, creating it on first use.
func (s *Store) Secret(ctx context.Context, name string) ([]byte, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", "secret:"+name).Scan(&v)
	if err == nil {
		return hex.DecodeString(v)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES (?, ?)", "secret:"+name, hex.EncodeToString(b)); err != nil {
		return nil, err
	}
	return b, nil
}

// MetaGet reads a meta value.
func (s *Store) MetaGet(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// MetaSet writes a meta value.
func (s *Store) MetaSet(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

func nullUnix(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}
