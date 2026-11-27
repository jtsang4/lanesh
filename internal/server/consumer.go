package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
)

type consumerAuth struct {
	laneID string
	keyID  string
	self   string
}

type consumerCtxKey struct{}

func consumerFrom(r *http.Request) consumerAuth {
	c, _ := r.Context().Value(consumerCtxKey{}).(consumerAuth)
	return c
}

// consumerAPI exposes lane capabilities over stateless JSON HTTP endpoints
// authenticated with a lane access key, used by the consumer CLI.
func (s *Server) consumerAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /lanes/{lane}/v1/machines", s.cMachines)
	mux.HandleFunc("GET /lanes/{lane}/v1/machines/{machine}/describe", s.cDescribe)
	mux.HandleFunc("GET /lanes/{lane}/v1/skills", s.cSkills)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/skills/read", s.cSkillRead)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/skills/grep", s.cSkillGrep)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/exec", s.cExec)
	mux.HandleFunc("GET /lanes/{lane}/v1/machines/{machine}/jobs", s.cJobList)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/jobs/{job}/read", s.cJobRead)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/jobs/{job}/write", s.cJobWrite)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/jobs/{job}/kill", s.cJobKill)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/files/read", s.cFileRead)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/files/write", s.cFileWrite)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/files/edit", s.cFileEdit)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/files/list", s.cFileList)
	mux.HandleFunc("POST /lanes/{lane}/v1/machines/{machine}/files/grep", s.cFileGrep)
	mux.HandleFunc("POST /lanes/{lane}/v1/transfer", s.cTransfer)
	mux.HandleFunc("GET /lanes/{lane}/v1/projects", s.cListProjects)
	mux.HandleFunc("GET /lanes/{lane}/v1/projects/{project}", s.cGetProject)
	mux.HandleFunc("POST /lanes/{lane}/v1/projects/update", s.cUpdateProject)
	return s.requireAccessKey(mux)
}

func (s *Server) requireAccessKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		laneID := r.PathValue("lane")
		tok := bearer(r)
		if laneID == "" || tok == "" {
			writeError(w, http.StatusUnauthorized, "access key required")
			return
		}
		key, err := s.store.ValidateKey(r.Context(), laneID, store.KindAccess, tok)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid access key")
			return
		}
		self := strings.TrimSpace(r.Header.Get(mcp.HeaderMachine))
		if self == "" {
			self = strings.TrimSpace(r.URL.Query().Get("machine"))
		}
		ctx := context.WithValue(r.Context(), consumerCtxKey{}, consumerAuth{
			laneID: laneID, keyID: key.ID, self: self,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const maxConsumerBody = 2 << 20

func decodeConsumer(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		return true
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsumerBody)).Decode(v)
	if errors.Is(err, io.EOF) {
		return true
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return false
	}
	return true
}

func writeCallError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrOffline) {
		writeError(w, http.StatusServiceUnavailable, "machine is offline")
		return
	}
	var pe *protocol.Error
	if errors.As(err, &pe) {
		status := http.StatusBadRequest
		switch pe.Code {
		case protocol.CodeNotFound:
			status = http.StatusNotFound
		case protocol.CodeForbidden:
			status = http.StatusForbidden
		case protocol.CodeExists:
			status = http.StatusConflict
		case protocol.CodeTooLarge:
			status = http.StatusRequestEntityTooLarge
		case protocol.CodeUnsupported:
			status = http.StatusNotImplemented
		case protocol.CodeUnavailable:
			status = http.StatusServiceUnavailable
		case protocol.CodeTimeout:
			status = http.StatusGatewayTimeout
		case protocol.CodeInternal:
			status = http.StatusInternalServerError
		}
		if pe.Code == protocol.CodeUnsupported && strings.HasPrefix(pe.Message, "unknown method") {
			writeError(w, status, "this machine runs an older Lanesh that does not support this yet; it updates itself automatically, so try again in a minute")
			return
		}
		writeError(w, status, "%s", pe.Message)
		return
	}
	writeError(w, http.StatusBadGateway, "%v", err)
}

func (s *Server) consumerMachine(w http.ResponseWriter, r *http.Request, ref string, needOnline, needExec, needSkills bool) (MachineView, bool) {
	c := consumerFrom(r)
	m, err := s.resolveMachine(r.Context(), c.laneID, ref)
	if err != nil {
		writeError(w, http.StatusNotFound, "%v", err)
		return MachineView{}, false
	}
	if needOnline && !m.Online {
		writeError(w, http.StatusServiceUnavailable, "machine %q is offline", m.Name)
		return MachineView{}, false
	}
	if needExec && !m.Policy.AllowExec {
		writeError(w, http.StatusForbidden, "machine %q does not allow command execution or file access", m.Name)
		return MachineView{}, false
	}
	if needSkills && !m.Policy.ExposeSkills {
		writeError(w, http.StatusForbidden, "machine %q does not share its skills", m.Name)
		return MachineView{}, false
	}
	return m, true
}

func (s *Server) cAudit(ctx context.Context, c consumerAuth, action, machineID string, detail map[string]any, err error) {
	result := "ok"
	if err != nil {
		msg := err.Error()
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		result = "error: " + msg
	}
	s.audit(ctx, store.AuditEntry{
		LaneID: c.laneID, Actor: c.keyID, Action: action, MachineID: machineID, Detail: detail, Result: result,
	})
}

func (s *Server) cMachines(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	views, err := s.machineViews(r.Context(), c.laneID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) cDescribe(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, false, false)
	if !ok {
		return
	}
	var res protocol.DescribeResult
	if err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodDescribe, nil, &res); err != nil {
		writeCallError(w, err)
		return
	}
	if conn := s.hub.Get(c.laneID, m.ID); conn != nil {
		conn.setSkills(res.Skills)
	}
	_ = s.store.SaveSkills(context.WithoutCancel(r.Context()), c.laneID, m.ID, res.Skills)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cSkills(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	machineRef := strings.TrimSpace(r.URL.Query().Get("machine"))

	views, err := s.machineViews(r.Context(), c.laneID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if machineRef != "" {
		m, ok := s.consumerMachine(w, r, machineRef, false, false, true)
		if !ok {
			return
		}
		views = []MachineView{m}
	}
	type skillItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Machine     string `json:"machine"`
		Online      bool   `json:"online"`
		Source      string `json:"source"`
		Hash        string `json:"hash"`
	}
	var out []skillItem
	for _, m := range views {
		if !m.Policy.ExposeSkills {
			continue
		}
		idx := s.skillIndex(r.Context(), c.laneID, m.ID)
		for _, sk := range idx.Skills {
			if q != "" && !strings.Contains(strings.ToLower(sk.Name), q) && !strings.Contains(strings.ToLower(sk.Description), q) {
				continue
			}
			out = append(out, skillItem{
				Name: sk.Name, Description: sk.Description, Machine: m.Name, Online: m.Online, Source: sk.Source, Hash: sk.Hash,
			})
		}
	}
	if out == nil {
		out = []skillItem{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cSkillRead(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, false, true)
	if !ok {
		return
	}
	var p protocol.SkillReadParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	if p.Skill == "" {
		writeError(w, http.StatusBadRequest, "skill is required")
		return
	}
	var res protocol.SkillReadResult
	if err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodSkillRead, p, &res); err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cSkillGrep(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, false, true)
	if !ok {
		return
	}
	var p protocol.SkillGrepParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	if p.Skill == "" || p.Pattern == "" {
		writeError(w, http.StatusBadRequest, "skill and pattern are required")
		return
	}
	var res protocol.SkillGrepResult
	if err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodSkillGrep, p, &res); err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cExec(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.ExecParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	p.Owner = c.keyID
	var st protocol.JobStatus
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodExecStart, p, &st)
	cmdLog := p.Command
	if len(cmdLog) > 1000 {
		cmdLog = cmdLog[:1000] + "…"
	}
	detail := map[string]any{"command": cmdLog, "cwd": p.Cwd, "skill": p.Skill}
	if err == nil {
		detail["job_id"], detail["status"] = st.JobID, st.Status
		if st.ExitCode != nil {
			detail["exit_code"] = *st.ExitCode
		}
	}
	s.cAudit(r.Context(), c, "exec", m.ID, detail, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) cJobList(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	owner := c.keyID
	if r.URL.Query().Get("all") == "true" || r.URL.Query().Get("all") == "1" {
		owner = ""
	}
	var res protocol.JobListResult
	if err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodJobList, protocol.JobListParams{Owner: owner}, &res); err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cJobRead(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.JobReadParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	usesV2 := p.Stream == "both" || p.StderrOffset != 0 || p.WaitUntil != "" || p.IdleMS != 0 || p.Match != ""
	if usesV2 && !slices.Contains(m.Features, protocol.FeatureJobReadWait) {
		writeError(w, http.StatusNotImplemented, "this machine runs an older Lanesh that does not support this yet; it updates itself automatically, so try again in a minute")
		return
	}
	p.JobID = r.PathValue("job")
	var res protocol.JobReadResult
	if err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodJobRead, p, &res); err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cJobWrite(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.JobWriteParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	p.JobID = r.PathValue("job")
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodJobWrite, p, nil)
	s.cAudit(r.Context(), c, "job.write", m.ID, map[string]any{"job_id": p.JobID, "bytes": len(p.Data), "close_stdin": p.CloseStdin}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cJobKill(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.JobKillParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	p.JobID = r.PathValue("job")
	var st protocol.JobStatus
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodJobKill, p, &st)
	s.cAudit(r.Context(), c, "job.kill", m.ID, map[string]any{"job_id": p.JobID, "signal": p.Signal}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) cFileRead(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.FileReadParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	if (p.StartLine != 0 || p.EndLine != 0) && !slices.Contains(m.Features, protocol.FeatureFileLines) {
		writeError(w, http.StatusNotImplemented, "this machine runs an older Lanesh that does not support this yet; it updates itself automatically, so try again in a minute")
		return
	}
	var res protocol.FileReadResult
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodFileRead, p, &res)
	s.cAudit(r.Context(), c, "file.read", m.ID, map[string]any{"path": p.Path, "bytes": len(res.Content)}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cFileWrite(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.FileWriteParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	var res protocol.FileWriteResult
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodFileWrite, p, &res)
	s.cAudit(r.Context(), c, "file.write", m.ID, map[string]any{"path": p.Path, "bytes": res.Size, "overwrite": p.Overwrite}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cFileEdit(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.FileEditParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	var res protocol.FileEditResult
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodFileEdit, p, &res)
	s.cAudit(r.Context(), c, "file.edit", m.ID, map[string]any{"path": p.Path, "edits": len(p.Edits), "bytes": res.Size}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cFileList(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.FileListParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	var res protocol.FileListResult
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodFileList, p, &res)
	s.cAudit(r.Context(), c, "file.list", m.ID, map[string]any{"path": p.Path, "entries": len(res.Entries)}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cFileGrep(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	m, ok := s.consumerMachine(w, r, r.PathValue("machine"), true, true, false)
	if !ok {
		return
	}
	var p protocol.FileGrepParams
	if !decodeConsumer(w, r, &p) {
		return
	}
	var res protocol.FileGrepResult
	err := s.call(r.Context(), c.laneID, m.ID, protocol.MethodFileGrep, p, &res)
	s.cAudit(r.Context(), c, "file.grep", m.ID, map[string]any{"path": p.Path, "matches": len(res.Matches)}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cTransfer(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	var body struct {
		FromMachine string `json:"from_machine"`
		FromPath    string `json:"from_path"`
		ToMachine   string `json:"to_machine"`
		ToPath      string `json:"to_path"`
		Overwrite   bool   `json:"overwrite,omitempty"`
	}
	if !decodeConsumer(w, r, &body) {
		return
	}
	from, ok := s.consumerMachine(w, r, body.FromMachine, true, true, false)
	if !ok {
		return
	}
	to, ok := s.consumerMachine(w, r, body.ToMachine, true, true, false)
	if !ok {
		return
	}
	res, err := s.transfer(r.Context(), c.laneID, from.ID, body.FromPath, to.ID, body.ToPath, body.Overwrite)
	s.cAudit(r.Context(), c, "transfer", from.ID, map[string]any{
		"from_path": body.FromPath, "to_machine": to.Name, "to_path": body.ToPath, "bytes": res.Bytes, "files": res.Files,
	}, err)
	if err != nil {
		writeCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cListProjects(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	ps, err := s.store.ListProjects(r.Context(), c.laneID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views, _ := s.machineViews(r.Context(), c.laneID)
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
	removedNames, _ := s.store.MachineNames(r.Context(), c.laneID, unique(removed))
	out := make([]ProjectSummary, 0, len(ps))
	for _, p := range ps {
		sum := ProjectSummary{
			ID: p.ID, Name: p.Name, Summary: p.Summary(), Revision: p.Revision,
			UpdatedAt: p.UpdatedAt.UTC(), UpdatedBy: s.actorName(r.Context(), c.laneID, p.UpdatedBy),
			Locations: make([]mcp.LocationView, 0, len(p.Locations)),
		}
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

func (s *Server) cGetProject(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	p, err := s.store.GetProject(r.Context(), c.laneID, r.PathValue("project"))
	if !s.projectErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, s.projectView(r.Context(), p))
}

func (s *Server) cUpdateProject(w http.ResponseWriter, r *http.Request) {
	c := consumerFrom(r)
	var in mcp.UpdateProjectInput
	if !decodeConsumer(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Project) == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	if in.Context != "" && (in.OldText != "" || in.NewText != "") {
		writeError(w, http.StatusBadRequest, "use context, or old_text/new_text, not both")
		return
	}
	add := make([]store.Location, 0, len(in.AddLocations))
	for _, l := range in.AddLocations {
		m, err := s.resolveMachine(r.Context(), c.laneID, l.Machine)
		if err != nil {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
		p := strings.TrimSpace(l.Path)
		if p == "" {
			writeError(w, http.StatusBadRequest, "location path is required")
			return
		}
		add = append(add, store.Location{MachineID: m.ID, Path: p})
	}

	if in.Create {
		if in.OldText != "" {
			writeError(w, http.StatusBadRequest, "a new project has no text to replace; give its context")
			return
		}
		p := store.Project{Name: in.Project, Context: in.Context + in.NewText, Locations: add}
		if in.Rename != "" {
			p.Name = in.Rename
		}
		created, err := s.store.CreateProject(r.Context(), c.laneID, p, c.keyID)
		s.cAudit(r.Context(), c, "project.create", "", map[string]any{"name": cmp.Or(created.Name, strings.TrimSpace(p.Name)), "revision": created.Revision}, err)
		if !s.projectErr(w, err) {
			return
		}
		writeJSON(w, http.StatusCreated, s.projectView(r.Context(), created))
		return
	}

	var names map[string]string
	if len(in.RemoveLocations) > 0 {
		cur, err := s.store.GetProject(r.Context(), c.laneID, in.Project)
		if !s.projectErr(w, err) {
			return
		}
		ids := make([]string, len(cur.Locations))
		for i, l := range cur.Locations {
			ids[i] = l.MachineID
		}
		names, _ = s.store.MachineNames(r.Context(), c.laneID, ids)
	}
	var changed []string
	before := 0
	p, err := s.store.UpdateProject(r.Context(), c.laneID, in.Project, in.Revision, c.keyID, func(p *store.Project) error {
		before = p.Revision
		changed = changed[:0]
		if in.Rename != "" && strings.TrimSpace(in.Rename) != p.Name {
			p.Name = in.Rename
			changed = append(changed, "renamed")
		}
		switch {
		case in.Context != "":
			if in.Revision == 0 {
				return fmt.Errorf("replacing the whole context needs revision (now %d), or use old_text/new_text", p.Revision)
			}
			p.Context = in.Context
			changed = append(changed, "context replaced")
		case in.OldText != "":
			n := strings.Count(p.Context, in.OldText)
			if n != 1 {
				return fmt.Errorf("old_text occurs %d times in the context; it must occur exactly once", n)
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
		for _, rem := range in.RemoveLocations {
			path := strings.TrimSpace(rem.Path)
			idx := slices.IndexFunc(p.Locations, func(l store.Location) bool {
				return l.Path == path && (l.MachineID == rem.Machine || strings.EqualFold(names[l.MachineID], rem.Machine))
			})
			if idx < 0 {
				return fmt.Errorf("the project has no location %s %s", rem.Machine, path)
			}
			p.Locations = slices.Delete(p.Locations, idx, idx+1)
			changed = append(changed, "location removed")
		}
		for _, a := range add {
			if !slices.Contains(p.Locations, a) {
				p.Locations = append(p.Locations, a)
				changed = append(changed, "location added")
			}
		}
		return p.Validate()
	})
	if err != nil {
		s.cAudit(r.Context(), c, "project.update", "", map[string]any{"name": cmp.Or(p.Name, in.Project), "revision": p.Revision}, err)
		if !s.projectErr(w, err) {
			return
		}
		return
	}
	if p.Revision != before {
		s.cAudit(r.Context(), c, "project.update", "", map[string]any{"name": p.Name, "revision": p.Revision, "changes": strings.Join(changed, ", ")}, nil)
	}
	writeJSON(w, http.StatusOK, s.projectView(r.Context(), p))
}
