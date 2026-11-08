package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
)

// Machine describes a daemon as reported to one lane. Machine details are
// stored per membership so that one lane can never alter what another sees.
type Machine struct {
	ID       string `json:"id"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

// Membership is a machine's membership in a lane.
type Membership struct {
	LaneID      string          `json:"lane_id"`
	Machine     Machine         `json:"machine"`
	DisplayName string          `json:"display_name"`
	Policy      protocol.Policy `json:"policy"`
	JoinedAt    time.Time       `json:"joined_at"`
	LastSeen    time.Time       `json:"last_seen"`
}

// Join adds a membership, re-activates a removed one, or rotates the
// credential of an active one. Rotating requires proofHash to match the
// current credential, so a join key alone cannot take over a machine that is
// already a member. The display name is made unique within the lane.
func (s *Store) Join(ctx context.Context, laneID string, m Machine, wantName, credentialHash, proofHash string, policy protocol.Policy) (Membership, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.

	var existingName, existingHash string
	var revoked sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT display_name, credential_hash, revoked_at FROM memberships WHERE lane_id = ? AND machine_id = ?",
		laneID, m.ID).Scan(&existingName, &existingHash, &revoked)
	switch {
	case err == nil:
		if !revoked.Valid && (proofHash == "" || !auth.Equal(proofHash, existingHash)) {
			return Membership{}, ErrConflict
		}
		wantName = existingName
	case errors.Is(err, sql.ErrNoRows):
		if wantName, err = uniqueName(ctx, tx, laneID, wantName); err != nil {
			return Membership{}, err
		}
	default:
		return Membership{}, err
	}
	now := s.unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO memberships
		(lane_id, machine_id, display_name, credential_hash, os, arch, hostname, version, expose_skills, allow_exec, joined_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(lane_id, machine_id) DO UPDATE SET credential_hash = excluded.credential_hash,
			os = excluded.os, arch = excluded.arch, hostname = excluded.hostname, version = excluded.version,
			expose_skills = excluded.expose_skills, allow_exec = excluded.allow_exec, revoked_at = NULL`,
		laneID, m.ID, wantName, credentialHash, m.OS, m.Arch, m.Hostname, m.Version, policy.ExposeSkills, policy.AllowExec, now, now)
	if err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(); err != nil {
		return Membership{}, err
	}
	return s.GetMembership(ctx, laneID, m.ID)
}

func uniqueName(ctx context.Context, tx *sql.Tx, laneID, name string) (string, error) {
	if name == "" {
		name = "machine"
	}
	candidate := name
	for i := 2; ; i++ {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM memberships WHERE lane_id = ? AND display_name = ? AND revoked_at IS NULL", laneID, candidate).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return candidate, nil
		}
		candidate = name + "-" + strconv.Itoa(i)
	}
}

const membershipQuery = `SELECT lane_id, machine_id, display_name, os, arch, hostname, version,
	expose_skills, allow_exec, joined_at, last_seen
	FROM memberships WHERE revoked_at IS NULL`

func scanMembership(row interface{ Scan(...any) error }) (Membership, error) {
	var m Membership
	var joined, seen int64
	err := row.Scan(&m.LaneID, &m.Machine.ID, &m.DisplayName, &m.Machine.OS, &m.Machine.Arch, &m.Machine.Hostname, &m.Machine.Version,
		&m.Policy.ExposeSkills, &m.Policy.AllowExec, &joined, &seen)
	m.JoinedAt, m.LastSeen = time.Unix(joined, 0), time.Unix(seen, 0)
	return m, err
}

// GetMembership returns an active membership.
func (s *Store) GetMembership(ctx context.Context, laneID, machineID string) (Membership, error) {
	m, err := scanMembership(s.db.QueryRowContext(ctx, membershipQuery+" AND lane_id = ? AND machine_id = ?", laneID, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// MembershipByCredential returns the active membership for a machine credential.
func (s *Store) MembershipByCredential(ctx context.Context, laneID, credentialHash string) (Membership, error) {
	m, err := scanMembership(s.db.QueryRowContext(ctx, membershipQuery+" AND lane_id = ? AND credential_hash = ?", laneID, credentialHash))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// ListMemberships returns a lane's active memberships ordered by name.
func (s *Store) ListMemberships(ctx context.Context, laneID string) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, membershipQuery+" AND lane_id = ? ORDER BY display_name", laneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Membership{}
	for rows.Next() {
		m, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountMemberships returns the number of active memberships per lane.
func (s *Store) CountMemberships(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT lane_id, COUNT(*) FROM memberships WHERE revoked_at IS NULL GROUP BY lane_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// UpdateMembership records a connected machine's latest details and policy.
func (s *Store) UpdateMembership(ctx context.Context, laneID string, m Machine, policy protocol.Policy) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memberships SET os = ?, arch = ?, hostname = ?, version = ?,
		expose_skills = ?, allow_exec = ?, last_seen = ? WHERE lane_id = ? AND machine_id = ?`,
		m.OS, m.Arch, m.Hostname, m.Version, policy.ExposeSkills, policy.AllowExec, s.unix(), laneID, m.ID)
	return err
}

// TouchMembership records that a machine was connected just now.
func (s *Store) TouchMembership(ctx context.Context, laneID, machineID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE memberships SET last_seen = ? WHERE lane_id = ? AND machine_id = ?", s.unix(), laneID, machineID)
	return err
}

// RenameMembership changes a machine's display name within a lane.
func (s *Store) RenameMembership(ctx context.Context, laneID, machineID, name string) error {
	return expectOne(s.db.ExecContext(ctx, "UPDATE memberships SET display_name = ? WHERE lane_id = ? AND machine_id = ? AND revoked_at IS NULL", name, laneID, machineID))
}

// RevokeMembership removes a machine from a lane.
func (s *Store) RevokeMembership(ctx context.Context, laneID, machineID string) error {
	return expectOne(s.db.ExecContext(ctx, "UPDATE memberships SET revoked_at = ? WHERE lane_id = ? AND machine_id = ? AND revoked_at IS NULL", s.unix(), laneID, machineID))
}

// SkillHistoryLimit is how many recent skill index versions are kept per
// machine and lane, so that an outdated briefing token can be matched to the
// version it was issued for.
const SkillHistoryLimit = 20

// SaveSkills stores the latest skill index a machine reported to a lane and
// records it in the machine's version history.
func (s *Store) SaveSkills(ctx context.Context, laneID, machineID string, idx protocol.SkillIndex) error {
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_snapshots (lane_id, machine_id, index_json, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(lane_id, machine_id) DO UPDATE SET index_json = excluded.index_json, updated_at = excluded.updated_at`,
		laneID, machineID, string(raw), s.unix()); err != nil {
		return err
	}
	if idx.Hash != "" {
		// seq orders versions by when they were last current, using a counter
		// per machine rather than the clock (whose resolution varies by OS). A
		// version that comes back (e.g. a reverted change) becomes the newest.
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_history (lane_id, machine_id, hash, index_json, seq)
			VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM skill_history WHERE lane_id = ? AND machine_id = ?))
			ON CONFLICT(lane_id, machine_id, hash) DO UPDATE SET seq = excluded.seq`,
			laneID, machineID, idx.Hash, string(raw), laneID, machineID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM skill_history WHERE lane_id = ? AND machine_id = ? AND hash NOT IN (
			SELECT hash FROM skill_history WHERE lane_id = ? AND machine_id = ? ORDER BY seq DESC LIMIT ?)`,
			laneID, machineID, laneID, machineID, SkillHistoryLimit); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SkillHistory returns the recent skill index versions of a machine in a
// lane, newest first.
func (s *Store) SkillHistory(ctx context.Context, laneID, machineID string) ([]protocol.SkillIndex, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT index_json FROM skill_history WHERE lane_id = ? AND machine_id = ? ORDER BY seq DESC",
		laneID, machineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.SkillIndex
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var idx protocol.SkillIndex
		if err := json.Unmarshal([]byte(raw), &idx); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// Skills returns the last skill index a machine reported to a lane.
func (s *Store) Skills(ctx context.Context, laneID, machineID string) (protocol.SkillIndex, time.Time, error) {
	var raw string
	var updated int64
	err := s.db.QueryRowContext(ctx, "SELECT index_json, updated_at FROM skill_snapshots WHERE lane_id = ? AND machine_id = ?", laneID, machineID).Scan(&raw, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.SkillIndex{Skills: []protocol.Skill{}}, time.Time{}, ErrNotFound
	}
	if err != nil {
		return protocol.SkillIndex{}, time.Time{}, err
	}
	var idx protocol.SkillIndex
	err = json.Unmarshal([]byte(raw), &idx)
	return idx, time.Unix(updated, 0), err
}
