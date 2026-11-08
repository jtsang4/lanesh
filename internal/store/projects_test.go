package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjects(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	l, _ := s.CreateLane(ctx, "work")
	other, _ := s.CreateLane(ctx, "other")

	p, err := s.CreateProject(ctx, l.ID, Project{
		Name: "  Lanesh ", Context: "# Lanesh\nShare skills.",
		Locations: []Location{{MachineID: "mch_a", Path: "~/code/lanesh"}, {MachineID: "mch_a", Path: "~/code/lanesh "}},
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Lanesh" || p.Revision != 1 || len(p.Locations) != 1 {
		t.Fatalf("created: %+v", p)
	}
	if _, err := s.CreateProject(ctx, l.ID, Project{Name: "lanesh"}, "admin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("names are unique per lane, ignoring case: %v", err)
	}
	if _, err := s.CreateProject(ctx, other.ID, Project{Name: "lanesh"}, "admin"); err != nil {
		t.Fatalf("other lanes may reuse a name: %v", err)
	}
	for _, bad := range []Project{
		{Name: " "},
		{Name: "x", Context: strings.Repeat("a", MaxProjectContext+1)},
		{Name: "x", Locations: []Location{{MachineID: "mch_a"}}},
		{Name: "a\nb"},
	} {
		if _, err := s.CreateProject(ctx, l.ID, bad, "admin"); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}

	// By name (any case) or ID, and only within its lane.
	if got, err := s.GetProject(ctx, l.ID, "LANESH"); err != nil || got.ID != p.ID {
		t.Fatalf("by name: %+v %v", got, err)
	}
	if got, err := s.GetProject(ctx, l.ID, p.ID); err != nil || got.Name != "Lanesh" {
		t.Fatalf("by id: %+v %v", got, err)
	}
	if _, err := s.GetProject(ctx, other.ID, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other lane: %v", err)
	}

	// Updates make revisions; a stale base is refused; no-op changes save nothing.
	p2, err := s.UpdateProject(ctx, l.ID, "lanesh", 1, "key_1", func(p *Project) error {
		p.Context += "\nStatus: v0.2 in progress."
		return nil
	})
	if err != nil || p2.Revision != 2 || p2.UpdatedBy != "key_1" {
		t.Fatalf("update: %+v %v", p2, err)
	}
	if _, err := s.UpdateProject(ctx, l.ID, p.ID, 1, "key_2", func(p *Project) error { p.Context = "x"; return nil }); !errors.Is(err, ErrStale) {
		t.Fatalf("stale base: %v", err)
	}
	same, err := s.UpdateProject(ctx, l.ID, p.ID, 0, "key_2", func(*Project) error { return nil })
	if err != nil || same.Revision != 2 {
		t.Fatalf("no-op: %+v %v", same, err)
	}
	if _, err := s.UpdateProject(ctx, l.ID, p.ID, 0, "admin", func(p *Project) error { p.Name = "LANESH"; return nil }); err != nil {
		t.Fatalf("renaming to another case of its own name: %v", err)
	}
	s.CreateProject(ctx, l.ID, Project{Name: "notes"}, "admin") //nolint:errcheck // Checked by the rename below.
	if _, err := s.UpdateProject(ctx, l.ID, p.ID, 0, "admin", func(p *Project) error { p.Name = "Notes"; return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename onto another project: %v", err)
	}

	// History keeps the last ProjectHistoryLimit revisions, newest first.
	for i := range ProjectHistoryLimit + 3 {
		if _, err := s.UpdateProject(ctx, l.ID, p.ID, 0, "admin", func(p *Project) error {
			p.Context = strings.Repeat("x", i+1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	revs, err := s.ProjectRevisions(ctx, p.ID)
	if err != nil || len(revs) != ProjectHistoryLimit || revs[0].Revision != 3+ProjectHistoryLimit+3 || revs[0].Context != strings.Repeat("x", ProjectHistoryLimit+3) {
		t.Fatalf("history: %d revisions, newest %+v, %v", len(revs), revs[0].Revision, err)
	}

	list, _ := s.ListProjects(ctx, l.ID)
	if len(list) != 2 || list[0].Name != "LANESH" || list[1].Name != "notes" {
		t.Fatalf("list: %+v", list)
	}
	if err := s.DeleteProject(ctx, l.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if revs, _ := s.ProjectRevisions(ctx, p.ID); len(revs) != 0 {
		t.Fatalf("revisions survive their project: %d", len(revs))
	}
}
