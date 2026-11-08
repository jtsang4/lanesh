package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
	"github.com/jtsang4/lanesh/internal/version"
)

const (
	sessionCookie = "lanesh_session"
	sessionTTL    = 30 * 24 * time.Hour
)

func (s *Server) adminAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", s.apiLogin)
	mux.HandleFunc("POST /logout", s.apiLogout)
	mux.HandleFunc("GET /session", s.apiSession)

	authed := http.NewServeMux()
	authed.HandleFunc("GET /info", s.apiInfo)
	authed.HandleFunc("GET /lanes", s.apiListLanes)
	authed.HandleFunc("POST /lanes", s.apiCreateLane)
	authed.HandleFunc("GET /lanes/{lane}", s.apiGetLane)
	authed.HandleFunc("PATCH /lanes/{lane}", s.apiRenameLane)
	authed.HandleFunc("DELETE /lanes/{lane}", s.apiDeleteLane)
	authed.HandleFunc("GET /lanes/{lane}/machines", s.apiMachines)
	authed.HandleFunc("PATCH /lanes/{lane}/machines/{machine}", s.apiRenameMachine)
	authed.HandleFunc("DELETE /lanes/{lane}/machines/{machine}", s.apiRemoveMachine)
	authed.HandleFunc("GET /lanes/{lane}/machines/{machine}/skills", s.apiMachineSkills)
	authed.HandleFunc("GET /lanes/{lane}/machines/{machine}/skills/{skill}", s.apiReadSkill)
	authed.HandleFunc("GET /lanes/{lane}/skills", s.apiLaneSkills)
	authed.HandleFunc("GET /lanes/{lane}/keys", s.apiListKeys)
	authed.HandleFunc("POST /lanes/{lane}/keys", s.apiCreateKey)
	authed.HandleFunc("DELETE /lanes/{lane}/keys/{key}", s.apiRevokeKey)
	authed.HandleFunc("GET /lanes/{lane}/audit", s.apiAudit)
	mux.Handle("/", s.requireAdmin(authed))

	return http.NewCrossOriginProtection().Handler(mux)
}

func (s *Server) isAdmin(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && s.store.ValidSession(r.Context(), c.Value) {
		return true
	}
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return auth.Equal(auth.Hash(strings.TrimSpace(tok)), s.adminHash)
	}
	return false
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return false
	}
	return true
}

func (s *Server) secureCookies(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" || strings.HasPrefix(s.cfg.PublicURL, "https://")
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("login:" + clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again in a minute")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !auth.Equal(auth.Hash(strings.TrimSpace(body.Token)), s.adminHash) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	tok, err := s.store.CreateSession(r.Context(), sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the UI is served over HTTPS.
		Name: sessionCookie, Value: tok, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: s.secureCookies(r), SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	//nolint:gosec // Clears the cookie; Secure follows the request scheme as in apiLogin.
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookies(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": s.isAdmin(r), "version": version.Version})
}

func (s *Server) publicURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

func (s *Server) apiInfo(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version, "commit": version.Commit, "protocol": protocol.Version,
		"public_url": s.publicURL(r), "public_url_configured": s.cfg.PublicURL != "",
		"data_dir": s.cfg.DataDir, "started_at": s.started.UTC(), "lanes": st.Lanes, "machines": st.Machines,
		"online": s.hub.Count(),
	})
}

// LaneSummary is a lane with counts.
type LaneSummary struct {
	store.Lane
	MachineCount int `json:"machine_count"`
	OnlineCount  int `json:"online_count"`
}

func (s *Server) laneSummary(l store.Lane, counts map[string]int) LaneSummary {
	return LaneSummary{Lane: l, MachineCount: counts[l.ID], OnlineCount: len(s.hub.Lane(l.ID))}
}

func (s *Server) apiListLanes(w http.ResponseWriter, r *http.Request) {
	lanes, err := s.store.ListLanes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	counts, _ := s.store.CountMemberships(r.Context())
	out := make([]LaneSummary, 0, len(lanes))
	for _, l := range lanes {
		out = append(out, s.laneSummary(l, counts))
	}
	writeJSON(w, http.StatusOK, out)
}

func validName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, name != "" && len(name) <= 64
}

func (s *Server) apiCreateLane(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, ok := validName(body.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters")
		return
	}
	l, err := s.store.CreateLane(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "lane.create", Detail: map[string]any{"name": name}, Result: "ok"})
	writeJSON(w, http.StatusCreated, LaneSummary{Lane: l})
}

func (s *Server) lane(w http.ResponseWriter, r *http.Request) (store.Lane, bool) {
	l, err := s.store.GetLane(r.Context(), r.PathValue("lane"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "lane not found")
		return l, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return l, false
	}
	return l, true
}

func (s *Server) apiGetLane(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	counts, _ := s.store.CountMemberships(r.Context())
	writeJSON(w, http.StatusOK, s.laneSummary(l, counts))
}

func (s *Server) apiRenameLane(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, valid := validName(body.Name)
	if !valid {
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters")
		return
	}
	if err := s.store.RenameLane(r.Context(), l.ID, name); err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	l.Name = name
	counts, _ := s.store.CountMemberships(r.Context())
	writeJSON(w, http.StatusOK, s.laneSummary(l, counts))
}

func (s *Server) apiDeleteLane(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteLane(r.Context(), l.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	s.hub.Disconnect(l.ID, "", "lane deleted")
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "lane.delete", Detail: map[string]any{"name": l.Name}, Result: "ok"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiMachines(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	views, err := s.machineViews(r.Context(), l.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) apiRenameMachine(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := sanitizeName(body.Name, "")
	if name == "machine" && strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	views, _ := s.machineViews(r.Context(), l.ID)
	for _, v := range views {
		if v.Name == name && v.ID != r.PathValue("machine") {
			writeError(w, http.StatusConflict, "another machine in this lane is called %q", name)
			return
		}
	}
	if err := s.store.RenameMembership(r.Context(), l.ID, r.PathValue("machine"), name); err != nil {
		writeError(w, http.StatusNotFound, "machine not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (s *Server) apiRemoveMachine(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	id := r.PathValue("machine")
	if err := s.store.RevokeMembership(r.Context(), l.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "machine not found")
		return
	}
	s.hub.Disconnect(l.ID, id, "removed from lane")
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "machine.remove", MachineID: id, Result: "ok"})
	w.WriteHeader(http.StatusNoContent)
}

// SkillsResponse is a machine's skill index.
type SkillsResponse struct {
	protocol.SkillIndex
	Live      bool      `json:"live"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) apiMachineSkills(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	id := r.PathValue("machine")
	if _, err := s.store.GetMembership(r.Context(), l.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "machine not found")
		return
	}
	if c := s.hub.Get(l.ID, id); c != nil {
		if _, idx := c.State(); idx.Hash != "" {
			writeJSON(w, http.StatusOK, SkillsResponse{SkillIndex: idx, Live: true, UpdatedAt: time.Now().UTC()})
			return
		}
	}
	idx, updated, err := s.store.Skills(r.Context(), l.ID, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, SkillsResponse{SkillIndex: idx, UpdatedAt: updated.UTC()})
}

func (s *Server) apiReadSkill(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	var res protocol.SkillReadResult
	err := s.call(r.Context(), l.ID, r.PathValue("machine"), protocol.MethodSkillRead,
		protocol.SkillReadParams{Skill: r.PathValue("skill"), Path: r.URL.Query().Get("path")}, &res)
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
		}
		writeError(w, status, "%s", pe.Message)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// LaneSkill aggregates a skill across the machines of a lane.
type LaneSkill struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Consistent  bool               `json:"consistent"`
	Machines    []LaneSkillMachine `json:"machines"`
}

// LaneSkillMachine is one machine that has a skill.
type LaneSkillMachine struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
	Online    bool   `json:"online"`
	Source    string `json:"source"`
	Hash      string `json:"hash"`
}

func (s *Server) apiLaneSkills(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	views, err := s.machineViews(r.Context(), l.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	byName := map[string]*LaneSkill{}
	for _, v := range views {
		for _, sk := range s.skillIndex(r.Context(), l.ID, v.ID).Skills {
			ls := byName[sk.Name]
			if ls == nil {
				ls = &LaneSkill{Name: sk.Name, Description: sk.Description, Consistent: true}
				byName[sk.Name] = ls
			}
			if len(ls.Machines) > 0 && ls.Machines[0].Hash != sk.Hash {
				ls.Consistent = false
			}
			ls.Machines = append(ls.Machines, LaneSkillMachine{MachineID: v.ID, Name: v.Name, Online: v.Online, Source: sk.Source, Hash: sk.Hash})
		}
	}
	out := make([]LaneSkill, 0, len(byName))
	for _, ls := range byName {
		out = append(out, *ls)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiListKeys(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	keys, err := s.store.ListKeys(r.Context(), l.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) apiCreateKey(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind          string `json:"kind"`
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Kind != store.KindJoin && body.Kind != store.KindAccess {
		writeError(w, http.StatusBadRequest, "kind must be join or access")
		return
	}
	name, valid := validName(body.Name)
	if !valid {
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters")
		return
	}
	var exp *time.Time
	if body.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
		exp = &t
	}
	k, token, err := s.store.CreateKey(r.Context(), l.ID, body.Kind, name, exp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "key.create", Detail: map[string]any{"key_id": k.ID, "kind": k.Kind, "name": k.Name}, Result: "ok"})
	writeJSON(w, http.StatusCreated, map[string]any{"key": k, "token": token})
}

func (s *Server) apiRevokeKey(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	id := r.PathValue("key")
	if err := s.store.RevokeKey(r.Context(), l.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "key not found or already revoked")
		return
	}
	s.audit(r.Context(), store.AuditEntry{LaneID: l.ID, Actor: "admin", Action: "key.revoke", Detail: map[string]any{"key_id": id}, Result: "ok"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lane(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, err := s.store.ListAudit(r.Context(), store.AuditFilter{
		LaneID: l.ID, MachineID: q.Get("machine"), Action: q.Get("action"), Actor: q.Get("actor"), Before: before, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
