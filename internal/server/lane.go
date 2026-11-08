package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jtsang4/lanesh/internal/mcp"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
)

// MachineView combines a membership with its live connection state.
type MachineView struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	OS          string               `json:"os"`
	Arch        string               `json:"arch"`
	Hostname    string               `json:"hostname"`
	Version     string               `json:"version"`
	Online      bool                 `json:"online"`
	ConnectedAt *time.Time           `json:"connected_at,omitempty"`
	LastSeen    time.Time            `json:"last_seen"`
	JoinedAt    time.Time            `json:"joined_at"`
	Policy      protocol.Policy      `json:"policy"`
	SkillCount  int                  `json:"skill_count"`
	SkillsHash  string               `json:"skills_hash"`
	Environment protocol.Environment `json:"environment"`
}

func (s *Server) machineViews(ctx context.Context, laneID string) ([]MachineView, error) {
	ms, err := s.store.ListMemberships(ctx, laneID)
	if err != nil {
		return nil, err
	}
	live := s.hub.Lane(laneID)
	out := make([]MachineView, 0, len(ms))
	for _, m := range ms {
		v := MachineView{
			ID: m.Machine.ID, Name: m.DisplayName, OS: m.Machine.OS, Arch: m.Machine.Arch, Hostname: m.Machine.Hostname,
			Version: m.Machine.Version, LastSeen: m.LastSeen, JoinedAt: m.JoinedAt, Policy: m.Policy,
		}
		if c := live[m.Machine.ID]; c != nil {
			env, idx := c.State()
			v.Online, v.Policy, v.Version = true, c.Hello.Policy, c.Hello.Version
			t := c.Connected
			v.ConnectedAt = &t
			v.LastSeen = time.Now()
			v.Environment = env
			if idx.Hash != "" {
				v.SkillCount, v.SkillsHash = len(idx.Skills), idx.Hash
			} else {
				v.SkillCount, v.SkillsHash = c.Hello.SkillCount, c.Hello.SkillsHash
			}
		} else if idx, _, err := s.store.Skills(ctx, laneID, m.Machine.ID); err == nil {
			v.SkillCount, v.SkillsHash = len(idx.Skills), idx.Hash
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) resolveMachine(ctx context.Context, laneID, ref string) (MachineView, error) {
	views, err := s.machineViews(ctx, laneID)
	if err != nil {
		return MachineView{}, err
	}
	ref = strings.TrimSpace(ref)
	// IDs take precedence over names.
	for _, v := range views {
		if v.ID == ref {
			return v, nil
		}
	}
	for _, v := range views {
		if strings.EqualFold(v.Name, ref) {
			return v, nil
		}
	}
	names := make([]string, 0, len(views))
	for _, v := range views {
		names = append(names, v.Name)
	}
	return MachineView{}, fmt.Errorf("no machine %q in this lane (machines: %s)", ref, strings.Join(names, ", "))
}

func (s *Server) call(ctx context.Context, laneID, machineID, method string, params, out any) error {
	c := s.hub.Get(laneID, machineID)
	if c == nil {
		return ErrOffline
	}
	return c.Call(ctx, method, params, out)
}

// transfer streams from_path on one machine to to_path on another.
func (s *Server) transfer(ctx context.Context, laneID, fromID, fromPath, toID, toPath string, overwrite bool) (protocol.TransferResult, error) {
	var src, dst protocol.StatResult
	if err := s.call(ctx, laneID, fromID, protocol.MethodStat, protocol.StatParams{Path: fromPath}, &src); err != nil {
		return protocol.TransferResult{}, err
	}
	if !src.Exists {
		return protocol.TransferResult{}, fmt.Errorf("source %s does not exist", src.Path)
	}
	if err := s.call(ctx, laneID, toID, protocol.MethodStat, protocol.StatParams{Path: toPath}, &dst); err != nil {
		return protocol.TransferResult{}, err
	}
	if dst.Exists && !overwrite {
		return protocol.TransferResult{}, fmt.Errorf("destination %s already exists (set overwrite to replace it)", dst.Path)
	}

	id, ticket := s.transfers.create()
	defer s.transfers.remove(id)
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()

	type result struct {
		res protocol.TransferResult
		err error
	}
	sendCh, recvCh := make(chan result, 1), make(chan result, 1)
	go func() {
		var r protocol.TransferResult
		err := s.call(ctx, laneID, fromID, protocol.MethodTransferSend, protocol.TransferParams{
			TransferID: id, URL: "/transfers/" + id + "/upload", Ticket: ticket, Path: fromPath,
		}, &r)
		if err != nil {
			s.transfers.cancel(id, err)
		}
		sendCh <- result{r, err}
	}()
	go func() {
		var r protocol.TransferResult
		err := s.call(ctx, laneID, toID, protocol.MethodTransferReceive, protocol.TransferParams{
			TransferID: id, URL: "/transfers/" + id + "/download", Ticket: ticket, Path: toPath, Overwrite: overwrite,
		}, &r)
		if err != nil {
			s.transfers.cancel(id, err)
		}
		recvCh <- result{r, err}
	}()
	sent, recv := <-sendCh, <-recvCh
	if sent.err != nil {
		return protocol.TransferResult{}, fmt.Errorf("source: %w", sent.err)
	}
	if recv.err != nil {
		return protocol.TransferResult{}, fmt.Errorf("destination: %w", recv.err)
	}
	if sent.res.SHA256 != recv.res.SHA256 {
		return protocol.TransferResult{}, errors.New("checksum mismatch between source and destination")
	}
	return recv.res, nil
}

// backend adapts the server to mcp.Backend.
type backend struct{ s *Server }

func toMCP(v MachineView) mcp.Machine {
	return mcp.Machine{
		ID: v.ID, Name: v.Name, OS: v.OS, Arch: v.Arch, Version: v.Version, Online: v.Online, LastSeen: v.LastSeen,
		Shell: v.Environment.DefaultShell, Policy: v.Policy, SkillCount: v.SkillCount, SkillsHash: v.SkillsHash,
	}
}

func (b backend) Machines(ctx context.Context, laneID string) ([]mcp.Machine, error) {
	views, err := b.s.machineViews(ctx, laneID)
	if err != nil {
		return nil, err
	}
	out := make([]mcp.Machine, 0, len(views))
	for _, v := range views {
		out = append(out, toMCP(v))
	}
	return out, nil
}

func (b backend) Resolve(ctx context.Context, laneID, ref string) (mcp.Machine, error) {
	v, err := b.s.resolveMachine(ctx, laneID, ref)
	return toMCP(v), err
}

func (b backend) Call(ctx context.Context, laneID, machineID, method string, params, out any) error {
	return b.s.call(ctx, laneID, machineID, method, params, out)
}

func (b backend) Transfer(ctx context.Context, laneID string, from, to mcp.Machine, fromPath, toPath string, overwrite bool) (protocol.TransferResult, error) {
	return b.s.transfer(ctx, laneID, from.ID, fromPath, to.ID, toPath, overwrite)
}

func (b backend) Audit(ctx context.Context, e store.AuditEntry) { b.s.audit(ctx, e) }

func (b backend) UpdateSkills(ctx context.Context, laneID, machineID string, idx protocol.SkillIndex) {
	if c := b.s.hub.Get(laneID, machineID); c != nil {
		c.setSkills(idx)
	}
	_ = b.s.store.SaveSkills(context.WithoutCancel(ctx), laneID, machineID, idx)
}

func (b backend) LaneSkills(ctx context.Context, laneID string) map[string]protocol.SkillIndex {
	views, err := b.s.machineViews(ctx, laneID)
	if err != nil {
		return nil
	}
	out := make(map[string]protocol.SkillIndex, len(views))
	for _, v := range views {
		out[v.ID] = b.s.skillIndex(ctx, laneID, v.ID)
	}
	return out
}

// skillIndex returns a machine's live skill index, or its last snapshot when
// it is offline or has not reported one on this connection yet.
func (s *Server) skillIndex(ctx context.Context, laneID, machineID string) protocol.SkillIndex {
	var idx protocol.SkillIndex
	if c := s.hub.Get(laneID, machineID); c != nil {
		_, idx = c.State()
	}
	if idx.Hash == "" {
		idx, _, _ = s.store.Skills(ctx, laneID, machineID)
	}
	return idx
}

func (b backend) SkillHistory(ctx context.Context, laneID, machineID string) []protocol.SkillIndex {
	hist, err := b.s.store.SkillHistory(ctx, laneID, machineID)
	if err != nil {
		return nil
	}
	return hist
}

// laneServers caches one MCP server per lane, rebuilt when the lane's
// instructions (which include its machine roster) change.
type laneServers struct {
	mu      sync.Mutex
	servers map[string]laneServer
}

type laneServer struct {
	instructions string
	server       *sdkmcp.Server
}

// mcpServer returns the MCP server for a lane, with instructions describing
// the lane as it is now.
func (s *Server) mcpServer(r *http.Request) *sdkmcp.Server {
	laneID := r.PathValue("lane")
	var machines []mcp.Machine
	if views, err := s.machineViews(r.Context(), laneID); err == nil {
		for _, v := range views {
			machines = append(machines, toMCP(v))
		}
	}
	var projects []string
	if ps, err := s.store.ListProjects(r.Context(), laneID); err == nil {
		for _, p := range ps {
			projects = append(projects, p.Name)
		}
	}
	instructions := mcp.Instructions(machines, projects)

	s.lanes.mu.Lock()
	defer s.lanes.mu.Unlock()
	if ls, ok := s.lanes.servers[laneID]; ok && ls.instructions == instructions {
		return ls.server
	}
	srv := mcp.NewServer(backend{s}, s.briefings, instructions)
	s.lanes.servers[laneID] = laneServer{instructions: instructions, server: srv}
	return srv
}

// mcpHandler serves the MCP endpoint, authenticating access keys per lane.
func (s *Server) mcpHandler() http.Handler {
	handler := sdkmcp.NewStreamableHTTPHandler(s.mcpServer, &sdkmcp.StreamableHTTPOptions{
		SessionTimeout: 24 * time.Hour,
		// Requests are authenticated with access keys, and deployments commonly
		// sit behind a reverse proxy on the same host.
		DisableLocalhostProtection: true,
	})
	verify := func(ctx context.Context, token string, r *http.Request) (*sdkauth.TokenInfo, error) {
		laneID := r.PathValue("lane")
		key, err := s.store.ValidateKey(ctx, laneID, store.KindAccess, token)
		if err != nil {
			return nil, sdkauth.ErrInvalidToken
		}
		exp := time.Now().Add(365 * 24 * time.Hour)
		if key.ExpiresAt != nil {
			exp = *key.ExpiresAt
		}
		return &sdkauth.TokenInfo{
			UserID: key.ID, Expiration: exp,
			Extra: map[string]any{mcp.ExtraLane: laneID, mcp.ExtraKey: key.ID},
		}, nil
	}
	protected := sdkauth.RequireBearerToken(verify, nil)(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Header.Get("Authorization") == "" && q.Get("key") != "" {
			r.Header.Set("Authorization", "Bearer "+q.Get("key"))
		}
		if m := q.Get("machine"); m != "" && r.Header.Get(mcp.HeaderMachine) == "" {
			r.Header.Set(mcp.HeaderMachine, m)
		}
		protected.ServeHTTP(w, r)
	})
}
