package store

import (
	"context"
	"database/sql"
	"errors"
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

// ListKeys returns a lane's keys, newest first.
func (s *Store) ListKeys(ctx context.Context, laneID string) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+keyColumns+" FROM keys WHERE lane_id = ? ORDER BY created_at DESC, id", laneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
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
