package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
)

// CreateSession creates an admin UI session and returns its token.
func (s *Store) CreateSession(ctx context.Context, ttl time.Duration) (string, error) {
	token := auth.RandomString(48)
	now := s.unix()
	_, err := s.db.ExecContext(ctx, "INSERT INTO admin_sessions (hash, created_at, expires_at) VALUES (?, ?, ?)",
		auth.Hash(token), now, now+int64(ttl.Seconds()))
	return token, err
}

// ValidSession reports whether token is an unexpired admin session.
func (s *Store) ValidSession(ctx context.Context, token string) bool {
	var expires int64
	err := s.db.QueryRowContext(ctx, "SELECT expires_at FROM admin_sessions WHERE hash = ?", auth.Hash(token)).Scan(&expires)
	return err == nil && expires > s.unix()
}

// DeleteSession ends an admin session.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE hash = ?", auth.Hash(token))
	return err
}

// AuditEntry is one audit log record.
type AuditEntry struct {
	ID        int64          `json:"id"`
	Time      time.Time      `json:"time"`
	LaneID    string         `json:"lane_id"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	MachineID string         `json:"machine_id"`
	Detail    map[string]any `json:"detail"`
	Result    string         `json:"result"`
}

// AddAudit appends an audit record.
func (s *Store) AddAudit(ctx context.Context, e AuditEntry) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}
	if e.Detail == nil {
		detail = []byte("{}")
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO audit_log (ts, lane_id, actor, action, machine_id, detail, result) VALUES (?, ?, ?, ?, ?, ?, ?)",
		s.unix(), e.LaneID, e.Actor, e.Action, e.MachineID, string(detail), e.Result)
	return err
}

// AuditFilter selects audit records.
type AuditFilter struct {
	LaneID    string
	MachineID string
	Action    string
	Actor     string
	Before    int64 // return records with ID lower than this (pagination)
	Limit     int
}

// ListAudit returns matching audit records, newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	q := "SELECT id, ts, lane_id, actor, action, machine_id, detail, result FROM audit_log WHERE 1=1"
	var args []any
	add := func(cond string, v any) {
		q += " AND " + cond //nolint:gosec // cond is a constant; values are bound parameters.
		args = append(args, v)
	}
	if f.LaneID != "" {
		add("lane_id = ?", f.LaneID)
	}
	if f.MachineID != "" {
		add("machine_id = ?", f.MachineID)
	}
	if f.Action != "" {
		add("action = ?", f.Action)
	}
	if f.Actor != "" {
		add("actor = ?", f.Actor)
	}
	if f.Before > 0 {
		add("id < ?", f.Before)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, f.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var ts int64
		var detail string
		if err := rows.Scan(&e.ID, &ts, &e.LaneID, &e.Actor, &e.Action, &e.MachineID, &detail, &e.Result); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0)
		_ = json.Unmarshal([]byte(detail), &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune deletes audit records and sessions older than the retention period.
func (s *Store) Prune(ctx context.Context, auditRetention time.Duration) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at < ?", s.unix()); err != nil {
		return err
	}
	if auditRetention <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM audit_log WHERE ts < ?", s.now().Add(-auditRetention).Unix())
	return err
}

// Stats summarizes stored records for the instance overview.
type Stats struct {
	Lanes    int `json:"lanes"`
	Machines int `json:"machines"`
}

// Stats counts lanes and machines with an active membership.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM lanes").Scan(&st.Lanes); err != nil {
		return st, err
	}
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT machine_id) FROM memberships WHERE revoked_at IS NULL").Scan(&st.Machines)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return st, err
}
