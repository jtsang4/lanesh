package server

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
)

// inspectTimeout bounds how long a project view waits for its machines.
const inspectTimeout = 3 * time.Second

// inspectLocations asks each machine about its project locations, all
// machines at once, and reports the result per location in order.
func (s *Server) inspectLocations(ctx context.Context, laneID string, locs []store.Location) []mcp.LocationView {
	out := make([]mcp.LocationView, len(locs))
	views, _ := s.machineViews(ctx, laneID)
	byID := make(map[string]MachineView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}
	var removedIDs []string
	byMachine := map[string][]int{}
	for i, l := range locs {
		out[i] = mcp.LocationView{MachineID: l.MachineID, MachineName: l.MachineID, Path: l.Path}
		v, ok := byID[l.MachineID]
		switch {
		case !ok:
			out[i].Removed = true
			removedIDs = append(removedIDs, l.MachineID)
		case !v.Online:
			out[i].MachineName, out[i].Note = v.Name, "machine offline; not checked"
		case !v.Policy.AllowExec:
			out[i].MachineName, out[i].Online = v.Name, true
			out[i].Note = "the machine does not allow file access; not checked"
		default:
			out[i].MachineName, out[i].Online = v.Name, true
			byMachine[l.MachineID] = append(byMachine[l.MachineID], i)
		}
	}
	if len(removedIDs) > 0 {
		names, _ := s.store.MachineNames(ctx, laneID, removedIDs)
		for i := range out {
			if out[i].Removed && names[out[i].MachineID] != "" {
				out[i].MachineName = names[out[i].MachineID]
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for id, idx := range byMachine {
		wg.Go(func() {
			paths := make([]string, len(idx))
			for j, i := range idx {
				paths[j] = locs[i].Path
			}
			var res protocol.PathInspectResult
			err := s.call(ctx, laneID, id, protocol.MethodPathInspect, protocol.PathInspectParams{Paths: paths}, &res)
			for j, i := range idx {
				switch {
				case err == nil && j < len(res.Paths):
					info := res.Paths[j]
					out[i].Info = &info
				case err != nil && isUnsupported(err):
					out[i].Note = "the machine runs an older Lanesh and updates itself shortly; not checked"
				case err != nil:
					out[i].Note = "not checked: " + err.Error()
				}
			}
		})
	}
	wg.Wait()
	return out
}

func isUnsupported(err error) bool {
	var pe *protocol.Error
	return errors.As(err, &pe) && pe.Code == protocol.CodeUnsupported
}

// actorName names "admin" or an access key for "updated by".
func (s *Server) actorName(ctx context.Context, laneID, actor string) string {
	if actor == "admin" || actor == "" {
		return "admin"
	}
	names, _ := s.store.KeyNames(ctx, laneID, []string{actor})
	if n := names[actor]; n != "" {
		return n
	}
	return actor
}

// --- MCP backend ---

func (b backend) ListProjects(ctx context.Context, laneID string) ([]store.Project, error) {
	return b.s.store.ListProjects(ctx, laneID)
}

func (b backend) GetProject(ctx context.Context, laneID, ref string) (store.Project, error) {
	return b.s.store.GetProject(ctx, laneID, ref)
}

func (b backend) CreateProject(ctx context.Context, laneID string, p store.Project, actor string) (store.Project, error) {
	return b.s.store.CreateProject(ctx, laneID, p, actor)
}

func (b backend) UpdateProject(ctx context.Context, laneID, ref string, base int, actor string, change func(*store.Project) error) (store.Project, error) {
	return b.s.store.UpdateProject(ctx, laneID, ref, base, actor, change)
}

func (b backend) InspectLocations(ctx context.Context, laneID string, locs []store.Location) []mcp.LocationView {
	return b.s.inspectLocations(ctx, laneID, locs)
}

func (b backend) ActorName(ctx context.Context, laneID, actor string) string {
	return b.s.actorName(ctx, laneID, actor)
}

// --- admin API ---

// ProjectSummary is a project in the list of a lane's projects.
type ProjectSummary struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Summary   string             `json:"summary"`
	Locations []mcp.LocationView `json:"locations"`
	Revision  int                `json:"revision"`
	UpdatedAt time.Time          `json:"updated_at"`
	UpdatedBy string             `json:"updated_by"`
}

// ProjectView is a project with its locations as their machines report them.
type ProjectView struct {
	store.Project
	Locations     []mcp.LocationView `json:"locations"`
	UpdatedByName string             `json:"updated_by_name"`
}

// projectBody is the body of project create and update requests. Fields left
// out of an update keep their value.
type projectBody struct {
	Name      *string           `json:"name"`
	Context   *string           `json:"context"`
	Locations *[]store.Location `json:"locations"`
	// BaseRevision makes an update fail with 409 if the project changed since.
	BaseRevision int `json:"base_revision"`
}

func (s *Server) apiListProjects(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	ps, err := s.store.ListProjects(r.Context(), l.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views, _ := s.machineViews(r.Context(), l.ID)
	byID := make(map[string]MachineView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}
	var removed []string
	for _, p := range ps {
		for _, loc := range p.Locations {
			if _, ok := byID[loc.MachineID]; !ok {
				removed = append(removed, loc.MachineID)
			}
		}
	}
	removedNames, _ := s.store.MachineNames(r.Context(), l.ID, unique(removed))
	out := make([]ProjectSummary, 0, len(ps))
	for _, p := range ps {
		sum := ProjectSummary{ID: p.ID, Name: p.Name, Summary: p.Summary(), Revision: p.Revision, UpdatedAt: p.UpdatedAt.UTC(),
			UpdatedBy: s.actorName(r.Context(), l.ID, p.UpdatedBy), Locations: make([]mcp.LocationView, 0, len(p.Locations))}
		for _, loc := range p.Locations {
			lv := mcp.LocationView{MachineID: loc.MachineID, Path: loc.Path}
			if v, ok := byID[loc.MachineID]; ok {
				lv.MachineName, lv.Online = v.Name, v.Online
			} else {
				lv.MachineName, lv.Removed = cmp.Or(removedNames[loc.MachineID], loc.MachineID), true
			}
			sum.Locations = append(sum.Locations, lv)
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) projectView(ctx context.Context, p store.Project) ProjectView {
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return ProjectView{Project: p, Locations: s.inspectLocations(ctx, p.LaneID, p.Locations), UpdatedByName: s.actorName(ctx, p.LaneID, p.UpdatedBy)}
}

func (s *Server) apiGetProject(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetProject(r.Context(), l.ID, r.PathValue("project"))
	if !s.projectErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, s.projectView(r.Context(), p))
}

// checkLocations makes sure every location names a machine of the lane.
func (s *Server) checkLocations(ctx context.Context, laneID string, locs []store.Location) error {
	views, err := s.machineViews(ctx, laneID)
	if err != nil {
		return err
	}
	ids := make(map[string]bool, len(views))
	for _, v := range views {
		ids[v.ID] = true
	}
	for _, l := range locs {
		if !ids[l.MachineID] {
			return errors.New("a location names a machine that is not in this lane")
		}
	}
	return nil
}

func (s *Server) apiCreateProject(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	var body projectBody
	if !decode(w, r, &body) {
		return
	}
	p := store.Project{}
	if body.Name != nil {
		p.Name = *body.Name
	}
	if body.Context != nil {
		p.Context = *body.Context
	}
	if body.Locations != nil {
		p.Locations = *body.Locations
		if err := s.checkLocations(r.Context(), l.ID, p.Locations); err != nil {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
	}
	p, err := s.store.CreateProject(r.Context(), l.ID, p, "admin")
	if !s.projectErr(w, err) {
		return
	}
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "project.create", Detail: map[string]any{"name": p.Name, "revision": p.Revision}, Result: "ok"})
	writeJSON(w, http.StatusCreated, s.projectView(r.Context(), p))
}

func (s *Server) apiUpdateProject(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	var body projectBody
	if !decode(w, r, &body) {
		return
	}
	if body.Locations != nil {
		if err := s.checkLocations(r.Context(), l.ID, *body.Locations); err != nil {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
	}
	var changed []string
	p, err := s.store.UpdateProject(r.Context(), l.ID, r.PathValue("project"), body.BaseRevision, "admin", func(p *store.Project) error {
		if body.Name != nil && *body.Name != p.Name {
			p.Name = *body.Name
			changed = append(changed, "renamed")
		}
		if body.Context != nil && *body.Context != p.Context {
			p.Context = *body.Context
			changed = append(changed, "context")
		}
		if body.Locations != nil && !slices.Equal(*body.Locations, p.Locations) {
			p.Locations = *body.Locations
			changed = append(changed, "locations")
		}
		return nil
	})
	if errors.Is(err, store.ErrStale) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "project": s.projectView(r.Context(), p)})
		return
	}
	if !s.projectErr(w, err) {
		return
	}
	if len(changed) > 0 {
		s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "project.update",
			Detail: map[string]any{"name": p.Name, "revision": p.Revision, "changes": strings.Join(changed, ", ")}, Result: "ok"})
	}
	writeJSON(w, http.StatusOK, s.projectView(r.Context(), p))
}

func (s *Server) apiDeleteProject(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetProject(r.Context(), l.ID, r.PathValue("project"))
	if !s.projectErr(w, err) {
		return
	}
	if err := s.store.DeleteProject(r.Context(), l.ID, p.ID); !s.projectErr(w, err) {
		return
	}
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "project.delete", Detail: map[string]any{"name": p.Name}, Result: "ok"})
	w.WriteHeader(http.StatusNoContent)
}

// ProjectRevisionView is a kept revision, with the name of who made it.
type ProjectRevisionView struct {
	store.ProjectRevision
	UpdatedByName string `json:"updated_by_name"`
}

func (s *Server) apiProjectRevisions(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetProject(r.Context(), l.ID, r.PathValue("project"))
	if !s.projectErr(w, err) {
		return
	}
	revs, err := s.store.ProjectRevisions(r.Context(), p.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	out := make([]ProjectRevisionView, len(revs))
	for i, rev := range revs {
		rev.UpdatedAt = rev.UpdatedAt.UTC()
		out[i] = ProjectRevisionView{ProjectRevision: rev, UpdatedByName: s.actorName(r.Context(), l.ID, rev.UpdatedBy)}
	}
	writeJSON(w, http.StatusOK, out)
}

// projectErr writes the response for a project store error and reports
// whether the request may continue.
func (s *Server) projectErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "project not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "%v", err)
	default:
		writeError(w, http.StatusBadRequest, "%v", err)
	}
	return false
}
