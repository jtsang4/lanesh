package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Machine is a daemon installation, identified by its persistent machine ID.
type Machine struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	Hostname  string    `json:"hostname"`
	Version   string    `json:"version"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
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

// UpsertMachine records a machine's latest details.
func (s *Store) UpsertMachine(ctx context.Context, m Machine) error {
	now := s.unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO machines (id, name, os, arch, hostname, version, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, os = excluded.os, arch = excluded.arch,
			hostname = excluded.hostname, version = excluded.version, last_seen = excluded.last_seen`,
		m.ID, m.Name, m.OS, m.Arch, m.Hostname, m.Version, now, now)
	return err
}

// Join adds or re-activates a membership with a new credential. The display
// name is made unique within the lane.
func (s *Store) Join(ctx context.Context, laneID, machineID, wantName, credentialHash string, policy protocol.Policy) (Membership, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.

	var existing string
	err = tx.QueryRowContext(ctx, "SELECT display_name FROM memberships WHERE lane_id = ? AND machine_id = ?", laneID, machineID).Scan(&existing)
	switch {
	case err == nil:
		wantName = existing
	case !errors.Is(err, sql.ErrNoRows):
		return Membership{}, err
	default:
		wantName, err = uniqueName(ctx, tx, laneID, wantName)
		if err != nil {
			return Membership{}, err
		}
	}
	now := s.unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO memberships
		(lane_id, machine_id, display_name, credential_hash, expose_skills, allow_exec, joined_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(lane_id, machine_id) DO UPDATE SET credential_hash = excluded.credential_hash,
			expose_skills = excluded.expose_skills, allow_exec = excluded.allow_exec, revoked_at = NULL`,
		laneID, machineID, wantName, credentialHash, policy.ExposeSkills, policy.AllowExec, now, now)
	if err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(); err != nil {
		return Membership{}, err
	}
	return s.GetMembership(ctx, laneID, machineID)
}

func uniqueName(ctx context.Context, tx *sql.Tx, laneID, name string) (string, error) {
	if name == "" {
		name = "machine"
	}
	candidate := name
	for i := 2; ; i++ {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM memberships WHERE lane_id = ? AND display_name = ?", laneID, candidate).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return candidate, nil
		}
		candidate = name + "-" + strconv.Itoa(i)
	}
}

const membershipQuery = `SELECT m.lane_id, m.display_name, m.expose_skills, m.allow_exec, m.joined_at, m.last_seen,
	x.id, x.name, x.os, x.arch, x.hostname, x.version, x.first_seen, x.last_seen
	FROM memberships m JOIN machines x ON x.id = m.machine_id
	WHERE m.revoked_at IS NULL`

func scanMembership(row interface{ Scan(...any) error }) (Membership, error) {
	var m Membership
	var joined, seen, first, mseen int64
	err := row.Scan(&m.LaneID, &m.DisplayName, &m.Policy.ExposeSkills, &m.Policy.AllowExec, &joined, &seen,
		&m.Machine.ID, &m.Machine.Name, &m.Machine.OS, &m.Machine.Arch, &m.Machine.Hostname, &m.Machine.Version, &first, &mseen)
	m.JoinedAt, m.LastSeen = time.Unix(joined, 0), time.Unix(seen, 0)
	m.Machine.FirstSeen, m.Machine.LastSeen = time.Unix(first, 0), time.Unix(mseen, 0)
	return m, err
}

// GetMembership returns an active membership.
func (s *Store) GetMembership(ctx context.Context, laneID, machineID string) (Membership, error) {
	m, err := scanMembership(s.db.QueryRowContext(ctx, membershipQuery+" AND m.lane_id = ? AND m.machine_id = ?", laneID, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// MembershipByCredential returns the active membership for a machine credential.
func (s *Store) MembershipByCredential(ctx context.Context, laneID, credentialHash string) (Membership, error) {
	m, err := scanMembership(s.db.QueryRowContext(ctx, membershipQuery+" AND m.lane_id = ? AND m.credential_hash = ?", laneID, credentialHash))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// ListMemberships returns a lane's active memberships ordered by name.
func (s *Store) ListMemberships(ctx context.Context, laneID string) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, membershipQuery+" AND m.lane_id = ? ORDER BY m.display_name", laneID)
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

// TouchMembership records that a membership is connected and updates its policy.
func (s *Store) TouchMembership(ctx context.Context, laneID, machineID string, policy protocol.Policy) error {
	_, err := s.db.ExecContext(ctx, "UPDATE memberships SET last_seen = ?, expose_skills = ?, allow_exec = ? WHERE lane_id = ? AND machine_id = ?",
		s.unix(), policy.ExposeSkills, policy.AllowExec, laneID, machineID)
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

// SaveSkills stores the latest skill index of a machine.
func (s *Store) SaveSkills(ctx context.Context, machineID string, idx protocol.SkillIndex) error {
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO skill_snapshots (machine_id, index_json, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(machine_id) DO UPDATE SET index_json = excluded.index_json, updated_at = excluded.updated_at`,
		machineID, string(raw), s.unix())
	return err
}

// Skills returns the last stored skill index of a machine.
func (s *Store) Skills(ctx context.Context, machineID string) (protocol.SkillIndex, time.Time, error) {
	var raw string
	var updated int64
	err := s.db.QueryRowContext(ctx, "SELECT index_json, updated_at FROM skill_snapshots WHERE machine_id = ?", machineID).Scan(&raw, &updated)
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
