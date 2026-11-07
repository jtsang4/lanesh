package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
	"github.com/jtsang4/lanesh/internal/version"
)

// JoinRequest is sent by `lanesh join`.
type JoinRequest struct {
	MachineID string          `json:"machine_id"`
	Name      string          `json:"name"`
	OS        string          `json:"os"`
	Arch      string          `json:"arch"`
	Hostname  string          `json:"hostname"`
	Version   string          `json:"version"`
	Policy    protocol.Policy `json:"policy"`
}

// JoinResponse returns the machine credential.
type JoinResponse struct {
	LaneID        string `json:"lane_id"`
	LaneName      string `json:"lane_name"`
	MachineName   string `json:"machine_name"`
	Credential    string `json:"credential"`
	ServerVersion string `json:"server_version"`
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	laneID := r.PathValue("lane")
	if !s.limiter.allow("join:" + clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	key, err := s.store.ValidateKey(r.Context(), laneID, store.KindJoin, bearer(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid join key for this lane")
		return
	}
	var req JoinRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: %v", err)
		return
	}
	if !strings.HasPrefix(req.MachineID, "mch_") || len(req.MachineID) > 64 {
		writeError(w, http.StatusBadRequest, "invalid machine id")
		return
	}
	req.Name = sanitizeName(req.Name, req.Hostname)
	lane, err := s.store.GetLane(r.Context(), laneID)
	if err != nil {
		writeError(w, http.StatusNotFound, "lane not found")
		return
	}
	if err := s.store.UpsertMachine(r.Context(), store.Machine{
		ID: req.MachineID, Name: req.Name, OS: req.OS, Arch: req.Arch, Hostname: req.Hostname, Version: req.Version,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	credential := auth.NewToken(auth.PrefixMachine)
	m, err := s.store.Join(r.Context(), laneID, req.MachineID, req.Name, auth.Hash(credential), req.Policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	// A re-join rotates the credential; drop a connection using the old one.
	s.hub.Disconnect(laneID, req.MachineID, "machine re-joined")
	s.audit(r.Context(), store.AuditEntry{
		LaneID: laneID, Actor: key.ID, Action: "machine.join", MachineID: req.MachineID,
		Detail: map[string]any{"name": m.DisplayName, "os": req.OS, "arch": req.Arch, "allow_exec": req.Policy.AllowExec}, Result: "ok",
	})
	writeJSON(w, http.StatusOK, JoinResponse{
		LaneID: laneID, LaneName: lane.Name, MachineName: m.DisplayName, Credential: credential, ServerVersion: version.Version,
	})
}

func sanitizeName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(fallback)
	}
	if i := strings.IndexByte(name, '.'); i > 0 && name == fallback {
		name = name[:i] // strip domain from host names
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = out[:48]
	}
	if out == "" {
		out = "machine"
	}
	return out
}

func (s *Server) membership(r *http.Request) (store.Membership, error) {
	return s.store.MembershipByCredential(r.Context(), r.PathValue("lane"), auth.Hash(bearer(r)))
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	m, err := s.membership(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid machine credential")
		return
	}
	if err := s.store.RevokeMembership(r.Context(), m.LaneID, m.Machine.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	s.hub.Disconnect(m.LaneID, m.Machine.ID, "machine left the lane")
	s.audit(r.Context(), store.AuditEntry{LaneID: m.LaneID, Actor: m.Machine.ID, Action: "machine.leave", MachineID: m.Machine.ID, Result: "ok"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	m, err := s.membership(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid machine credential")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	ws.SetReadLimit(readLimit)
	ctx := r.Context()

	hello, err := readHello(ctx, ws)
	if err != nil {
		_ = ws.Close(websocket.StatusProtocolError, "expected hello")
		return
	}
	if hello.MachineID != m.Machine.ID {
		_ = ws.Close(websocket.StatusPolicyViolation, "machine id does not match credential")
		return
	}
	if hello.Protocol < protocol.MinVersion {
		msg, _ := protocol.NewMessage("", protocol.TypeError, nil)
		msg.Error = protocol.Errorf(protocol.CodeIncompatible, "protocol %d is too old; this server needs %d-%d, please upgrade lanesh",
			hello.Protocol, protocol.MinVersion, protocol.Version)
		raw, _ := json.Marshal(msg)
		_ = ws.Write(ctx, websocket.MessageText, raw)
		_ = ws.Close(websocket.StatusPolicyViolation, "incompatible protocol")
		return
	}

	_ = s.store.UpsertMachine(ctx, store.Machine{
		ID: hello.MachineID, Name: hello.Name, OS: hello.OS, Arch: hello.Arch, Hostname: hello.Hostname, Version: hello.Version,
	})
	_ = s.store.TouchMembership(ctx, m.LaneID, m.Machine.ID, hello.Policy)
	lane, _ := s.store.GetLane(ctx, m.LaneID)

	c := newConn(ws, m.LaneID, hello)
	welcome, _ := protocol.NewMessage("", protocol.TypeWelcome, protocol.Welcome{
		Protocol: min(hello.Protocol, protocol.Version), MinProtocol: protocol.MinVersion, MaxProtocol: protocol.Version,
		ServerVersion: version.Version, LaneID: m.LaneID, LaneName: lane.Name, MachineName: m.DisplayName,
	})
	if err := c.send(ctx, welcome); err != nil {
		return
	}
	s.hub.add(c)
	slog.Info("machine connected", "lane", m.LaneID, "machine", m.DisplayName, "version", hello.Version)
	defer func() {
		s.hub.remove(c)
		c.Close(websocket.StatusNormalClosure, "")
		_ = s.store.TouchMembership(context.WithoutCancel(ctx), m.LaneID, m.Machine.ID, hello.Policy)
		slog.Info("machine disconnected", "lane", m.LaneID, "machine", m.DisplayName)
	}()

	go c.pingLoop(ctx)
	go s.refreshDescribe(ctx, c)

	if err := c.readLoop(ctx, s.onEvent); !isNormalClose(err) {
		slog.Debug("daemon connection ended", "machine", m.Machine.ID, "err", err)
	}
}

func readHello(ctx context.Context, ws *websocket.Conn) (protocol.Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, raw, err := ws.Read(ctx)
	if err != nil {
		return protocol.Hello{}, err
	}
	var m protocol.Message
	if err := json.Unmarshal(raw, &m); err != nil || m.Type != protocol.TypeHello {
		return protocol.Hello{}, errors.New("expected hello")
	}
	var h protocol.Hello
	err = json.Unmarshal(m.Payload, &h)
	return h, err
}

func isNormalClose(err error) bool {
	s := websocket.CloseStatus(err)
	return s == websocket.StatusNormalClosure || s == websocket.StatusGoingAway || errors.Is(err, context.Canceled)
}

// refreshDescribe fetches the machine's environment and skills after connecting.
func (s *Server) refreshDescribe(ctx context.Context, c *Conn) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var d protocol.DescribeResult
	if err := c.Call(ctx, protocol.MethodDescribe, nil, &d); err != nil {
		slog.Debug("describe after connect failed", "machine", c.MachineID, "err", err)
		return
	}
	c.setDescribe(d)
	if err := s.store.SaveSkills(ctx, c.MachineID, d.Skills); err != nil {
		slog.Warn("save skills failed", "err", err)
	}
}

func (s *Server) onEvent(c *Conn, m protocol.Message) {
	ctx := context.Background()
	switch m.Type {
	case protocol.EventSkillsChanged:
		var idx protocol.SkillIndex
		if json.Unmarshal(m.Payload, &idx) == nil {
			c.setSkills(idx)
			_ = s.store.SaveSkills(ctx, c.MachineID, idx)
		}
	case protocol.EventJobExited:
		var e protocol.JobExitedEvent
		if json.Unmarshal(m.Payload, &e) == nil {
			code := -1
			if e.ExitCode != nil {
				code = *e.ExitCode
			}
			s.audit(ctx, store.AuditEntry{
				LaneID: c.LaneID, Actor: e.Owner, Action: "job.exited", MachineID: c.MachineID,
				Detail: map[string]any{"job_id": e.JobID, "command": truncate(e.Command, 500), "exit_code": code, "duration_ms": e.DurationMS},
				Result: e.Status,
			})
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
