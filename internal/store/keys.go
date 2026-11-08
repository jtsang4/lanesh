package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
)

// Key kinds.
const (
	KindJoin   = "join"
	KindAccess = "access"
)

// Key is a lane-scoped credential (join key or access key).
type Key struct {
	ID         string     `json:"id"`
	LaneID     string     `json:"lane_id"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateKey issues a new key and returns it with the plaintext token.
func (s *Store) CreateKey(ctx context.Context, laneID, kind, name string, expiresAt *time.Time) (Key, string, error) {
	prefix := auth.PrefixJoin
	if kind == KindAccess {
		prefix = auth.PrefixAccess
	} else if kind != KindJoin {
		return Key{}, "", errors.New("unknown key kind")
	}
	token := auth.NewToken(prefix)
	k := Key{
		ID: auth.NewID("key_"), LaneID: laneID, Kind: kind, Name: name,
		Prefix: auth.DisplayPrefix(token), ExpiresAt: expiresAt, CreatedAt: time.Unix(s.unix(), 0),
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO keys (id, lane_id, kind, name, hash, prefix, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.LaneID, k.Kind, k.Name, auth.Hash(token), k.Prefix, nullUnix(expiresAt), k.CreatedAt.Unix())
	if err != nil {
		return Key{}, "", err
	}
	return k, token, nil
}

const keyColumns = "id, lane_id, kind, name, prefix, expires_at, created_at, last_used_at, revoked_at"

func scanKey(row interface{ Scan(...any) error }) (Key, error) {
	var k Key
	var created int64
	var expires, used, revoked sql.NullInt64
	if err := row.Scan(&k.ID, &k.LaneID, &k.Kind, &k.Name, &k.Prefix, &expires, &created, &used, &revoked); err != nil {
		return k, err
	}
	k.CreatedAt = time.Unix(created, 0)
	k.ExpiresAt, k.LastUsedAt, k.RevokedAt = nullTime(expires), nullTime(used), nullTime(revoked)
	return k, nil
}

// ListKeys returns all of a lane's keys, newest first.
func (s *Store) ListKeys(ctx context.Context, laneID string) ([]Key, error) {
	keys, _, err := s.QueryKeys(ctx, KeyFilter{LaneID: laneID})
	return keys, err
}

// Key states for KeyFilter.Status.
const (
	KeyActive   = "active"   // neither revoked nor expired
	KeyInactive = "inactive" // revoked or expired
)

// KeyFilter selects a page of a lane's keys. Limit 0 returns all of them.
type KeyFilter struct {
	LaneID string
	Kind   string // KindJoin, KindAccess or "" for both
	Status string // KeyActive, KeyInactive or "" for both
	Limit  int
	Offset int
}

// QueryKeys returns a page of keys, active ones first and then newest first,
// together with the number of keys matching the filter.
func (s *Store) QueryKeys(ctx context.Context, f KeyFilter) ([]Key, int, error) {
	const active = "(revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?))"
	now := s.unix()
	where := " WHERE lane_id = ?"
	args := []any{f.LaneID}
	if f.Kind != "" {
		where += " AND kind = ?"
		args = append(args, f.Kind)
	}
	switch f.Status {
	case KeyActive:
		where += " AND " + active
		args = append(args, now)
	case KeyInactive:
		where += " AND NOT " + active
		args = append(args, now)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM keys"+where, args...).Scan(&total); err != nil { //nolint:gosec // where is built from constants.
		return nil, 0, err
	}
	q := "SELECT " + keyColumns + " FROM keys" + where + " ORDER BY CASE WHEN " + active + " THEN 0 ELSE 1 END, created_at DESC, rowid DESC"
	args = append(args, now)
	if f.Limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, f.Limit, max(f.Offset, 0))
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, k)
	}
	return out, total, rows.Err()
}

// KeyNames returns the names of the given keys of a lane, revoked ones included.
func (s *Store) KeyNames(ctx context.Context, laneID string, ids []string) (map[string]string, error) {
	return s.names(ctx, "SELECT id, name FROM keys WHERE lane_id = ? AND id IN ", laneID, ids)
}

// names runs a query selecting (id, name) pairs for ids, appending the IN list.
func (s *Store) names(ctx context.Context, prefix, laneID string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{laneID}
	marks := make([]string, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, prefix+"("+strings.Join(marks, ",")+")", args...) //nolint:gosec // Only placeholders are appended.
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// RevokeKey revokes a key.
func (s *Store) RevokeKey(ctx context.Context, laneID, id string) error {
	return expectOne(s.db.ExecContext(ctx,
		"UPDATE keys SET revoked_at = ? WHERE id = ? AND lane_id = ? AND revoked_at IS NULL", s.unix(), id, laneID))
}

// ValidateKey returns the active key of kind for token in laneID and records its use.
func (s *Store) ValidateKey(ctx context.Context, laneID, kind, token string) (Key, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+keyColumns+" FROM keys WHERE hash = ?", auth.Hash(token))
	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, err
	}
	now := s.now()
	if k.Kind != kind || k.LaneID != laneID || k.RevokedAt != nil || (k.ExpiresAt != nil && now.After(*k.ExpiresAt)) {
		return Key{}, ErrNotFound
	}
	// Record usage at most once a minute to limit writes.
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) > time.Minute {
		_, _ = s.db.ExecContext(ctx, "UPDATE keys SET last_used_at = ? WHERE id = ?", now.Unix(), k.ID)
	}
	return k, nil
}
