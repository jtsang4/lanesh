package mcp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
)

// LocationView is a project location with what its machine reports about it
// now. Info is nil when the path could not be checked; Note says why.
type LocationView struct {
	MachineID   string             `json:"machine_id"`
	MachineName string             `json:"machine_name"`
	Path        string             `json:"path"`
	Online      bool               `json:"online"`
	Removed     bool               `json:"removed,omitempty"` // the machine left the lane
	Info        *protocol.PathInfo `json:"info,omitempty"`
	Note        string             `json:"note,omitempty"`
}

// Projects is the part of Backend that stores projects.
type Projects interface {
	ListProjects(ctx context.Context, laneID string) ([]store.Project, error)
	GetProject(ctx context.Context, laneID, ref string) (store.Project, error)
	CreateProject(ctx context.Context, laneID string, p store.Project, actor string) (store.Project, error)
	UpdateProject(ctx context.Context, laneID, ref string, base int, actor string, change func(*store.Project) error) (store.Project, error)
	// InspectLocations checks each location on its machine, in order.
	InspectLocations(ctx context.Context, laneID string, locs []store.Location) []LocationView
	// ActorName names "admin" or an access key, for "updated by".
	ActorName(ctx context.Context, laneID, actor string) string
}

// --- get_project ---

// GetProjectInput selects a project.
type GetProjectInput struct {
	Project string `json:"project,omitempty" jsonschema:"project name; omit to list projects"`
}

func (t *Tools) getProject(ctx context.Context, c caller, in GetProjectInput) (*text, error) {
	if strings.TrimSpace(in.Project) == "" {
		return t.listProjects(ctx, c)
	}
	p, err := t.backend.GetProject(ctx, c.laneID, in.Project)
	if errors.Is(err, store.ErrNotFound) {
		return nil, t.unknownProject(ctx, c, in.Project)
	}
	if err != nil {
		return nil, err
	}
	var out text
	out.line("[lanesh] project %q (revision %d, updated %s by %s)", p.Name, p.Revision,
		p.UpdatedAt.UTC().Format("2006-01-02 15:04 UTC"), t.backend.ActorName(ctx, c.laneID, p.UpdatedBy))
	writeLocations(&out, t.backend.InspectLocations(ctx, c.laneID, p.Locations))
	if strings.TrimSpace(p.Context) == "" {
		out.line("Context: empty. Add what an agent should know with update_project.")
	} else {
		out.line("[lanesh] begin context")
		out.raw(p.Context)
		out.line("[lanesh] end context")
	}
	out.line("Change it with update_project (old_text/new_text, or context with revision %d).", p.Revision)
	return &out, nil
}

func (t *Tools) listProjects(ctx context.Context, c caller) (*text, error) {
	ps, err := t.backend.ListProjects(ctx, c.laneID)
	if err != nil {
		return nil, err
	}
	var out text
	if len(ps) == 0 {
		out.line("[lanesh] this lane has no projects yet. Create one with update_project and create: true.")
		return &out, nil
	}
	out.line("[lanesh] %s in this lane:", plural(len(ps), "project", "projects"))
	shown := 0
	for _, p := range ps {
		line := "- " + p.Name
		if s := p.Summary(); s != "" {
			line += ": " + shorten(s, descMaxRunes)
		}
		if out.tokens()+estimateTokens(line) > resultBudget {
			break
		}
		out.raw(line)
		shown++
	}
	if rest := len(ps) - shown; rest > 0 {
		names := make([]string, 0, rest)
		for _, p := range ps[shown:] {
			names = append(names, p.Name)
		}
		out.line("[lanesh] %d more, names only: %s", rest, strings.Join(names, ", "))
	}
	out.line("Read one with get_project and its name.")
	return &out, nil
}

func (t *Tools) unknownProject(ctx context.Context, c caller, ref string) error {
	ps, _ := t.backend.ListProjects(ctx, c.laneID)
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	if len(names) == 0 {
		return fmt.Errorf("no project %q; this lane has no projects (create one with update_project and create: true)", ref)
	}
	return fmt.Errorf("no project %q (projects: %s)", ref, strings.Join(names, ", "))
}

// writeLocations lists a project's locations with what their machines report.
func writeLocations(out *text, locs []LocationView) {
	if len(locs) == 0 {
		out.line("Locations: none")
		return
	}
	out.line("Locations (where this project lives; a guide, not a limit):")
	for _, l := range locs {
		out.line("- %s %s: %s", l.MachineName, l.Path, locationState(l))
	}
}

func locationState(l LocationView) string {
	switch {
	case l.Removed:
		return "the machine left the lane"
	case l.Info == nil:
		return l.Note
	case l.Info.Error != "":
		return l.Info.Error
	case !l.Info.Exists:
		return "does not exist"
	case !l.Info.Dir:
		return "a file, not a directory"
	}
	var parts []string
	if l.Info.Git {
		g := "git"
		if l.Info.Worktree {
			g = "git worktree"
		}
		if l.Info.Branch != "" {
			g += ", branch " + l.Info.Branch
		}
		parts = append(parts, g)
	}
	if len(l.Info.AgentFiles) > 0 {
		files := make([]string, len(l.Info.AgentFiles))
		for i, f := range l.Info.AgentFiles {
			if name, target, ok := strings.Cut(f, " -> "); ok {
				f = name + " (same file as " + target + ")"
			}
			files[i] = f
		}
		read := "read it before working there"
		if len(files) > 1 {
			read = "read them before working there"
		}
		parts = append(parts, "has "+strings.Join(files, ", ")+": "+read)
	}
	if len(parts) == 0 {
		return "exists"
	}
	return strings.Join(parts, "; ")
}

// --- update_project ---

// LocationInput names a machine and a path on it.
type LocationInput struct {
	Machine string `json:"machine"`
	Path    string `json:"path"`
}

// UpdateProjectInput creates or changes a project.
type UpdateProjectInput struct {
	Project         string          `json:"project" jsonschema:"project name"`
	Create          bool            `json:"create,omitempty" jsonschema:"create it (fails if it exists)"`
	Rename          string          `json:"rename,omitempty"`
	OldText         string          `json:"old_text,omitempty" jsonschema:"context text to replace; must occur exactly once"`
	NewText         string          `json:"new_text,omitempty" jsonschema:"replacement for old_text; without old_text it is appended"`
	Context         string          `json:"context,omitempty" jsonschema:"the whole context (Markdown); replacing it needs revision"`
	Revision        int             `json:"revision,omitempty" jsonschema:"revision you read; fails if it changed since"`
	AddLocations    []LocationInput `json:"add_locations,omitempty"`
	RemoveLocations []LocationInput `json:"remove_locations,omitempty"`
}

func (t *Tools) updateProject(ctx context.Context, c caller, in UpdateProjectInput) (*text, error) {
	if strings.TrimSpace(in.Project) == "" {
		return nil, errors.New("project is required")
	}
	if in.Context != "" && (in.OldText != "" || in.NewText != "") {
		return nil, errors.New("use context, or old_text/new_text, not both")
	}
	add, err := t.resolveLocations(ctx, c, in.AddLocations)
	if err != nil {
		return nil, err
	}
	remove, err := t.resolveLocations(ctx, c, in.RemoveLocations)
	if err != nil {
		return nil, err
	}
	actor := c.keyID
	var out text

	if in.Create {
		if in.OldText != "" {
			return nil, errors.New("a new project has no text to replace; give its context")
		}
		p := store.Project{Name: in.Project, Context: in.Context + in.NewText, Locations: add}
		if in.Rename != "" {
			p.Name = in.Rename
		}
		created, err := t.backend.CreateProject(ctx, c.laneID, p, actor)
		t.auditProject(ctx, c, "project.create", cmp.Or(created.Name, strings.TrimSpace(p.Name)), created.Revision, nil, err)
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("project %q already exists; read it with get_project and update it without create", in.Project)
		}
		if err != nil {
			return nil, err
		}
		out.line("[lanesh] created project %q (revision 1).", created.Name)
		return &out, nil
	}

	var changed []string
	p, err := t.backend.UpdateProject(ctx, c.laneID, in.Project, in.Revision, actor, func(p *store.Project) error {
		changed = changed[:0]
		if in.Rename != "" && in.Rename != p.Name {
			p.Name = in.Rename
			changed = append(changed, "renamed")
		}
		switch {
		case in.Context != "":
			if in.Revision == 0 {
				return fmt.Errorf("replacing the whole context needs the revision you read (now %d), or use old_text/new_text", p.Revision)
			}
			p.Context = in.Context
			changed = append(changed, "context replaced")
		case in.OldText != "":
			n := strings.Count(p.Context, in.OldText)
			if n != 1 {
				return fmt.Errorf("old_text occurs %d times in the context; it must occur exactly once (read it again with get_project)", n)
			}
			p.Context = strings.Replace(p.Context, in.OldText, in.NewText, 1)
			changed = append(changed, "context edited")
		case in.NewText != "":
			if p.Context != "" && !strings.HasSuffix(p.Context, "\n") {
				p.Context += "\n"
			}
			p.Context += in.NewText
			changed = append(changed, "context appended")
		}
		for _, r := range remove {
			i := indexLocation(p.Locations, r)
			if i < 0 {
				return fmt.Errorf("the project has no location %s", r.Path)
			}
			p.Locations = append(p.Locations[:i], p.Locations[i+1:]...)
		}
		if len(remove) > 0 {
			changed = append(changed, plural(len(remove), "location removed", "locations removed"))
		}
		added := 0
		for _, a := range add {
			if indexLocation(p.Locations, a) < 0 {
				p.Locations = append(p.Locations, a)
				added++
			}
		}
		if added > 0 {
			changed = append(changed, plural(added, "location added", "locations added"))
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, t.unknownProject(ctx, c, in.Project)
	}
	t.auditProject(ctx, c, "project.update", p.Name, p.Revision, changed, err)
	if errors.Is(err, store.ErrStale) {
		return nil, fmt.Errorf("%w; read it again with get_project and redo your change", err)
	}
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		out.line("[lanesh] project %q unchanged (revision %d).", p.Name, p.Revision)
	} else {
		out.line("[lanesh] project %q saved as revision %d: %s.", p.Name, p.Revision, strings.Join(changed, ", "))
	}
	return &out, nil
}

func indexLocation(locs []store.Location, l store.Location) int {
	for i, x := range locs {
		if x.MachineID == l.MachineID && x.Path == l.Path {
			return i
		}
	}
	return -1
}

// resolveLocations turns machine names into IDs. Machines must be in the lane;
// paths are taken as given.
func (t *Tools) resolveLocations(ctx context.Context, c caller, in []LocationInput) ([]store.Location, error) {
	out := make([]store.Location, 0, len(in))
	for _, l := range in {
		m, err := t.backend.Resolve(ctx, c.laneID, l.Machine)
		if err != nil {
			return nil, err
		}
		out = append(out, store.Location{MachineID: m.ID, Path: strings.TrimSpace(l.Path)})
	}
	return out, nil
}

func (t *Tools) auditProject(ctx context.Context, c caller, action, name string, revision int, changed []string, err error) {
	detail := map[string]any{"name": name, "revision": revision}
	if len(changed) > 0 {
		detail["changes"] = strings.Join(changed, ", ")
	}
	t.audit(ctx, c, action, Machine{}, detail, err)
}
