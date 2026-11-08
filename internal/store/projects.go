package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jtsang4/lanesh/internal/auth"
)

// Limits for projects.
const (
	MaxProjectName      = 64
	MaxProjectContext   = 20 << 10 // bytes; an agent reads the whole context at once
	MaxProjectLocations = 20
	MaxLocationPath     = 1024
	// ProjectHistoryLimit is how many revisions of a project are kept.
	ProjectHistoryLimit = 20
)

// ErrStale is returned when a project changed after the revision an update
// was based on.
var ErrStale = errors.New("the project changed since that revision")

// Location is where a project lives on one machine: an entry point for
// agents, not a limit on where they may work.
type Location struct {
	MachineID string `json:"machine_id"`
	Path      string `json:"path"`
}

// Project is a piece of work in a lane: its locations and its context, free
// Markdown about what it is, its rules and where it stands.
type Project struct {
	ID        string     `json:"id"`
	LaneID    string     `json:"lane_id"`
	Name      string     `json:"name"`
	Context   string     `json:"context"`
	Locations []Location `json:"locations"`
	Revision  int        `json:"revision"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	// UpdatedBy is "admin" or the ID of the access key that made the change.
	UpdatedBy string `json:"updated_by"`
}

// ProjectRevision is a project as it was after one change.
type ProjectRevision struct {
	Revision  int        `json:"revision"`
	Name      string     `json:"name"`
	Context   string     `json:"context"`
	Locations []Location `json:"locations"`
	UpdatedAt time.Time  `json:"updated_at"`
	UpdatedBy string     `json:"updated_by"`
}

// Summary is the first paragraph of the project's context, skipping
// headings, for lists of projects.
func (p Project) Summary() string {
	var para []string
	for _, line := range strings.Split(p.Context, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			if len(para) > 0 {
				return strings.Join(para, " ")
			}
		case strings.HasPrefix(line, "#"):
			if len(para) > 0 {
				return strings.Join(para, " ")
			}
		default:
			para = append(para, line)
		}
	}
	return strings.Join(para, " ")
}

// Validate checks and normalizes a project's name, context and locations.
func (p *Project) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	switch {
	case p.Name == "":
		return errors.New("project name is empty")
	case utf8.RuneCountInString(p.Name) > MaxProjectName:
		return fmt.Errorf("project name is longer than %d characters", MaxProjectName)
	case strings.IndexFunc(p.Name, unicode.IsControl) >= 0:
		return errors.New("project name contains control characters")
	case len(p.Context) > MaxProjectContext:
		return fmt.Errorf("project context is %d bytes; the limit is %d (keep it short: the source is the full record)", len(p.Context), MaxProjectContext)
	case len(p.Locations) > MaxProjectLocations:
		return fmt.Errorf("a project has at most %d locations", MaxProjectLocations)
	}
	seen := map[Location]bool{}
	out := make([]Location, 0, len(p.Locations))
	for _, l := range p.Locations {
		l.Path = strings.TrimSpace(l.Path)
		switch {
		case l.MachineID == "":
			return errors.New("a location needs a machine")
		case l.Path == "":
			return errors.New("a location needs a path")
		case len(l.Path) > MaxLocationPath:
			return errors.New("location path is too long")
		case strings.IndexFunc(l.Path, unicode.IsControl) >= 0:
			return errors.New("location path contains control characters")
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	p.Locations = out
	return nil
}

const projectColumns = "id, lane_id, name, context, locations, revision, created_at, updated_at, updated_by"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var locations string
	var created, updated int64
	if err := row.Scan(&p.ID, &p.LaneID, &p.Name, &p.Context, &locations, &p.Revision, &created, &updated, &p.UpdatedBy); err != nil {
		return p, err
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	if err := json.Unmarshal([]byte(locations), &p.Locations); err != nil || p.Locations == nil {
		p.Locations = []Location{}
	}
	return p, nil
}

func encodeLocations(l []Location) string {
	if l == nil {
		l = []Location{}
	}
	b, _ := json.Marshal(l)
	return string(b)
}

// CreateProject creates a project at revision 1. It returns ErrConflict when
// the lane already has a project of that name (compared case-insensitively).
func (s *Store) CreateProject(ctx context.Context, laneID string, p Project, actor string) (Project, error) {
	if err := p.Validate(); err != nil {
		return Project{}, err
	}
	now := time.Unix(s.unix(), 0)
	p.ID, p.LaneID, p.Revision, p.CreatedAt, p.UpdatedAt, p.UpdatedBy = auth.NewID("prj_"), laneID, 1, now, now, actor
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	if _, err := tx.ExecContext(ctx, "INSERT INTO projects ("+projectColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.ID, p.LaneID, p.Name, p.Context, encodeLocations(p.Locations), p.Revision, now.Unix(), now.Unix(), actor); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Project{}, fmt.Errorf("a project named %q: %w", p.Name, ErrConflict)
		}
		return Project{}, err
	}
	if err := saveRevision(ctx, tx, p); err != nil {
		return Project{}, err
	}
	return p, tx.Commit()
}

// ListProjects returns a lane's projects by name.
func (s *Store) ListProjects(ctx context.Context, laneID string) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectColumns+" FROM projects WHERE lane_id = ? ORDER BY name COLLATE NOCASE", laneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns a project of a lane by ID or by name (case-insensitive).
func (s *Store) GetProject(ctx context.Context, laneID, ref string) (Project, error) {
	return getProject(ctx, s.db, laneID, ref)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getProject(ctx context.Context, q queryer, laneID, ref string) (Project, error) {
	ref = strings.TrimSpace(ref)
	p, err := scanProject(q.QueryRowContext(ctx, "SELECT "+projectColumns+
		" FROM projects WHERE lane_id = ? AND (id = ? OR name = ? COLLATE NOCASE) ORDER BY id = ? DESC LIMIT 1", laneID, ref, ref, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

// UpdateProject applies change to a project and saves it as a new revision.
// With base > 0 the update fails with ErrStale unless the project is still at
// revision base. A change that leaves the project as it was saves nothing.
func (s *Store) UpdateProject(ctx context.Context, laneID, ref string, base int, actor string, change func(*Project) error) (Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	cur, err := getProject(ctx, tx, laneID, ref)
	if err != nil {
		return Project{}, err
	}
	if base > 0 && base != cur.Revision {
		return cur, fmt.Errorf("%w (%d; it is now at %d)", ErrStale, base, cur.Revision)
	}
	next := cur
	next.Locations = append([]Location(nil), cur.Locations...)
	if err := change(&next); err != nil {
		return cur, err
	}
	if err := next.Validate(); err != nil {
		return cur, err
	}
	if next.Name == cur.Name && next.Context == cur.Context && encodeLocations(next.Locations) == encodeLocations(cur.Locations) {
		return cur, nil
	}
	next.Revision, next.UpdatedAt, next.UpdatedBy = cur.Revision+1, time.Unix(s.unix(), 0), actor
	if _, err := tx.ExecContext(ctx, "UPDATE projects SET name = ?, context = ?, locations = ?, revision = ?, updated_at = ?, updated_by = ? WHERE id = ?",
		next.Name, next.Context, encodeLocations(next.Locations), next.Revision, next.UpdatedAt.Unix(), actor, cur.ID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return cur, fmt.Errorf("a project named %q: %w", next.Name, ErrConflict)
		}
		return cur, err
	}
	if err := saveRevision(ctx, tx, next); err != nil {
		return cur, err
	}
	return next, tx.Commit()
}

func saveRevision(ctx context.Context, tx *sql.Tx, p Project) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_revisions (project_id, revision, name, context, locations, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.ID, p.Revision, p.Name, p.Context, encodeLocations(p.Locations), p.UpdatedAt.Unix(), p.UpdatedBy); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM project_revisions WHERE project_id = ? AND revision <= ?", p.ID, p.Revision-ProjectHistoryLimit)
	return err
}

// ProjectRevisions returns the kept revisions of a project, newest first.
func (s *Store) ProjectRevisions(ctx context.Context, projectID string) ([]ProjectRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT revision, name, context, locations, updated_at, updated_by
		FROM project_revisions WHERE project_id = ? ORDER BY revision DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectRevision{}
	for rows.Next() {
		var r ProjectRevision
		var locations string
		var updated int64
		if err := rows.Scan(&r.Revision, &r.Name, &r.Context, &locations, &updated, &r.UpdatedBy); err != nil {
			return nil, err
		}
		r.UpdatedAt = time.Unix(updated, 0)
		if err := json.Unmarshal([]byte(locations), &r.Locations); err != nil || r.Locations == nil {
			r.Locations = []Location{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteProject deletes a project and its revisions.
func (s *Store) DeleteProject(ctx context.Context, laneID, id string) error {
	return expectOne(s.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ? AND lane_id = ?", id, laneID))
}
