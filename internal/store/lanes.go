package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
)

// Lane is a group of machines.
type Lane struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateLane creates a lane.
func (s *Store) CreateLane(ctx context.Context, name string) (Lane, error) {
	l := Lane{ID: auth.NewID("ln_"), Name: name, CreatedAt: time.Unix(s.unix(), 0)}
	_, err := s.db.ExecContext(ctx, "INSERT INTO lanes (id, name, created_at) VALUES (?, ?, ?)", l.ID, l.Name, l.CreatedAt.Unix())
	return l, err
}

// GetLane returns a lane by ID.
func (s *Store) GetLane(ctx context.Context, id string) (Lane, error) {
	var l Lane
	var created int64
	err := s.db.QueryRowContext(ctx, "SELECT id, name, created_at FROM lanes WHERE id = ?", id).Scan(&l.ID, &l.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	l.CreatedAt = time.Unix(created, 0)
	return l, err
}

// ListLanes returns all lanes, oldest first.
func (s *Store) ListLanes(ctx context.Context) ([]Lane, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, created_at FROM lanes ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Lane{}
	for rows.Next() {
		var l Lane
		var created int64
		if err := rows.Scan(&l.ID, &l.Name, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(created, 0)
		out = append(out, l)
	}
	return out, rows.Err()
}

// RenameLane changes a lane's name.
func (s *Store) RenameLane(ctx context.Context, id, name string) error {
	return expectOne(s.db.ExecContext(ctx, "UPDATE lanes SET name = ? WHERE id = ?", name, id))
}

// DeleteLane removes a lane with its keys and memberships.
func (s *Store) DeleteLane(ctx context.Context, id string) error {
	return expectOne(s.db.ExecContext(ctx, "DELETE FROM lanes WHERE id = ?", id))
}

func expectOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
