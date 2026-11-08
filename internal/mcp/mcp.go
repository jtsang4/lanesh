// Package mcp exposes a lane to agents as MCP tools.
//
// Every tool returns framed plain text (see render.go): compact for the
// model, explicit about anything left out, and ending with EndMarker so that
// an agent can tell when its client cut a result.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
	"github.com/jtsang4/lanesh/internal/store"
	"github.com/jtsang4/lanesh/internal/version"
)

// Machine is a lane member as seen by the MCP layer.
type Machine struct {
	ID         string
	Name       string
	OS         string
	Arch       string
	Version    string
	Online     bool
	LastSeen   time.Time
	Shell      string
	Policy     protocol.Policy
	SkillCount int
	SkillsHash string
}

// Backend provides lane state and machine access to the tools.
type Backend interface {
	Machines(ctx context.Context, laneID string) ([]Machine, error)
	Resolve(ctx context.Context, laneID, ref string) (Machine, error)
	Call(ctx context.Context, laneID, machineID, method string, params, out any) error
	Transfer(ctx context.Context, laneID string, from, to Machine, fromPath, toPath string, overwrite bool) (protocol.TransferResult, error)
	Audit(ctx context.Context, e store.AuditEntry)
	// UpdateSkills records a skill index just read from a machine, so that
	// briefing tokens are issued and checked against the same state.
	UpdateSkills(ctx context.Context, laneID, machineID string, idx protocol.SkillIndex)
	// SkillHistory returns the machine's recent skill index versions, newest first.
	SkillHistory(ctx context.Context, laneID, machineID string) []protocol.SkillIndex
	// LaneSkills returns the latest known skill index of every machine in a
	// lane (live, or the last snapshot for offline machines), keyed by machine ID.
	LaneSkills(ctx context.Context, laneID string) map[string]protocol.SkillIndex
	Projects
}

// Header names understood by the MCP endpoint.
const (
	HeaderMachine = "X-Lanesh-Machine"
	ExtraLane     = "lane"
	ExtraKey      = "key"
)

type caller struct {
	laneID string
	keyID  string
	self   string
}

func callerFrom(req *mcp.CallToolRequest) (caller, error) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return caller{}, errors.New("unauthenticated")
	}
	lane, _ := req.Extra.TokenInfo.Extra[ExtraLane].(string)
	key, _ := req.Extra.TokenInfo.Extra[ExtraKey].(string)
	if lane == "" {
		return caller{}, errors.New("unauthenticated")
	}
	c := caller{laneID: lane, keyID: key}
	if req.Extra.Header != nil {
		c.self = strings.TrimSpace(req.Extra.Header.Get(HeaderMachine))
	}
	return c, nil
}

func (c caller) isSelf(m Machine) bool {
	return c.self != "" && (strings.EqualFold(c.self, m.Name) || c.self == m.ID)
}

// Tools implements the Lanesh MCP tools.
type Tools struct {
	backend   Backend
	briefings *auth.Briefings
}

// NewServer builds the MCP server with all tools registered.
// instructions should come from [Instructions].
func NewServer(b Backend, briefings *auth.Briefings, instructions string) *mcp.Server {
	t := &Tools{backend: b, briefings: briefings}
	s := mcp.NewServer(&mcp.Implementation{Name: "lanesh", Title: "Lanesh", Version: version.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	t.register(s)
	s.AddReceivingMiddleware(frameErrors)
	return s
}

// frameErrors adds the end marker to tool errors produced outside the tool
// handlers, such as argument validation by the SDK, so that every result
// ends with it.
func frameErrors(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		r, ok := res.(*mcp.CallToolResult)
		if method != "tools/call" || err != nil || !ok || !r.IsError {
			return res, err
		}
		var last *mcp.TextContent
		for _, c := range r.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				last = tc
			}
		}
		switch {
		case last == nil:
			r.Content = append(r.Content, &mcp.TextContent{Text: "[lanesh] error\n" + EndMarker})
		case !strings.HasSuffix(last.Text, EndMarker):
			if !strings.HasPrefix(last.Text, "[lanesh]") {
				last.Text = "[lanesh] error: " + last.Text
			}
			last.Text = strings.TrimRight(last.Text, "\n") + "\n" + EndMarker
		}
		return res, err
	}
}

// handler is a tool implementation that returns framed text.
type handler[In any] func(context.Context, caller, In) (*text, error)

// add registers a tool whose result is plain text. Errors are framed too, so
// that every result ends with EndMarker.
func add[In any](s *mcp.Server, t *mcp.Tool, h handler[In]) {
	mcp.AddTool(s, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		c, err := callerFrom(req)
		if err != nil {
			return nil, nil, err
		}
		out, err := h(ctx, c, in)
		if err != nil {
			var br *BriefingRequired
			if errors.As(err, &br) {
				return nil, nil, errors.New(br.Text)
			}
			return nil, nil, errors.New("[lanesh] error: " + err.Error() + "\n" + EndMarker)
		}
		return out.result(), nil, nil
	})
}

func (t *Tools) register(s *mcp.Server) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	rw := &mcp.ToolAnnotations{DestructiveHint: ptr(true)}
	tool := func(name, desc string, ann *mcp.ToolAnnotations) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: desc, Annotations: ann}
	}
	add(s, tool("list_machines", "Machines in this lane: platform, online, permissions, skill count.", ro), t.listMachines)
	add(s, tool("describe_machine", "A machine's environment, permissions and skills (name and short description), "+
		"plus the briefing token every other call for it needs. The main way to choose a skill: review this list, then read_skill.", ro), t.describeMachine)
	add(s, tool("find_skills", "Optional quick keyword search of skill names and descriptions on all machines. Not semantic; "+
		"if a query or two finds nothing clear, use describe_machine instead of more queries. machine without query lists its skills.", ro), t.findSkills)
	add(s, tool("read_skill", "Read a skill: no path gives SKILL.md and its file tree; path is a file (offset/limit) or a directory ending in \"/\"; "+
		"paths reads several files.", ro), t.readSkill)
	add(s, tool("grep_skill", "Search a skill's text files for a string (or regex). Returns file:line: text.", ro), t.grepSkill)
	add(s, tool("get_project", "A project's context (what it is, its rules, where it stands) and its locations: "+
		"the machines and paths where it lives, a guide rather than a limit. Without project, lists projects.", ro), t.getProject)
	add(s, tool("update_project", "Create or change a project. Prefer old_text/new_text for edits; keep the context "+
		"short and current, since the source itself is the full record.", &mcp.ToolAnnotations{DestructiveHint: ptr(false)}), t.updateProject)
	add(s, tool("exec", "Run a shell command on a machine. Returns when done or after timeout_ms (default 30000); "+
		"then it keeps running as a job (job_read, job_kill). With skill, cwd is relative to the skill directory. "+
		"stdin is closed unless background.", &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}), t.exec)
	add(s, tool("job_read", "Read a job's output from offset; wait_ms waits for new output.", ro), t.jobRead)
	add(s, tool("job_write", "Write to a background job's stdin.", nil), t.jobWrite)
	add(s, tool("job_kill", "Stop a job and its children.", rw), t.jobKill)
	add(s, tool("job_list", "Recent jobs on a machine started with your key (all: every key).", ro), t.jobList)
	add(s, tool("read_file", "Read a file on a machine (offset/limit; paths relative to home). Binary as base64.", ro), t.readFile)
	add(s, tool("write_file", "Write a file up to 1 MiB atomically; overwrite to replace.", rw), t.writeFile)
	add(s, tool("transfer", "Copy a file or directory between machines. to_path is the full destination path, not its parent; "+
		"overwrite to replace.", rw), t.transfer)
}

func ptr[T any](v T) *T { return &v }

// online resolves a machine and checks that it is connected.
func (t *Tools) online(ctx context.Context, c caller, ref string) (Machine, error) {
	if strings.TrimSpace(ref) == "" {
		return Machine{}, errors.New("machine is required")
	}
	m, err := t.backend.Resolve(ctx, c.laneID, ref)
	if err != nil {
		return Machine{}, err
	}
	if !m.Online {
		return Machine{}, fmt.Errorf("machine %q is offline (last seen %s)", m.Name, lastSeen(m))
	}
	return m, nil
}

func lastSeen(m Machine) string {
	if m.LastSeen.IsZero() {
		return "never"
	}
	return m.LastSeen.UTC().Format("2006-01-02 15:04 UTC")
}

// briefed resolves an online machine and checks the caller's briefing token
// against the machine's current state. When the token is missing, unknown or
// outdated, nothing runs: the error carries a fresh briefing and token so that
// the agent can read it and repeat the call.
//
// tool and param name the call and its token parameter, so that the reply can
// say exactly how to retry.
func (t *Tools) briefed(ctx context.Context, c caller, tool, param, ref, token string) (Machine, error) {
	m, err := t.online(ctx, c, ref)
	if err != nil {
		return Machine{}, err
	}
	if t.briefings.Valid(c.laneID, m.ID, m.SkillsHash, token) {
		return m, nil
	}
	// The server's view of the machine may lag behind; ask the machine itself.
	d, err := t.describe(ctx, c, m)
	if err != nil {
		return Machine{}, err
	}
	m.SkillsHash, m.Policy = d.Skills.Hash, d.Policy
	if t.briefings.Valid(c.laneID, m.ID, m.SkillsHash, token) {
		return m, nil
	}
	reason, changes := "no briefing token was given", ""
	if strings.TrimSpace(token) != "" {
		reason = "its briefing token is unknown (or older than the versions Lanesh keeps)"
		// Find the version the token was issued for, so that the agent learns
		// what changed relative to what it saw; agents without a token (for
		// example new sessions) get a plain briefing.
		for _, old := range t.backend.SkillHistory(ctx, c.laneID, m.ID) {
			if old.Hash != d.Skills.Hash && t.briefings.Valid(c.laneID, m.ID, old.Hash, token) {
				reason = "the machine's skills changed since your last briefing"
				changes = renderChanges(diffSkills(old, d.Skills))
				if changes == "" {
					changes = "Changes since your last briefing: only which of two same-named skills takes priority changed."
				}
				break
			}
		}
	}
	return Machine{}, &BriefingRequired{
		Machine: m.Name,
		Text:    renderBriefing(m, d, t.briefings.Token(c.laneID, m.ID, m.Name, d.Skills.Hash), reason, tool, param, changes),
	}
}

// describe fetches a machine's briefing and records its skill index.
func (t *Tools) describe(ctx context.Context, c caller, m Machine) (protocol.DescribeResult, error) {
	var d protocol.DescribeResult
	if err := t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodDescribe, nil, &d); err != nil {
		return d, protoErr(err)
	}
	t.backend.UpdateSkills(ctx, c.laneID, m.ID, d.Skills)
	return d, nil
}

func requirePolicy(m Machine, exec bool) error {
	if exec && !m.Policy.AllowExec {
		return fmt.Errorf("machine %q does not allow command execution or file access", m.Name)
	}
	if !exec && !m.Policy.ExposeSkills {
		return fmt.Errorf("machine %q does not share its skills", m.Name)
	}
	return nil
}

// protoErr unwraps protocol errors and explains methods an older daemon lacks.
func protoErr(err error) error {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		if pe.Code == protocol.CodeUnsupported && strings.HasPrefix(pe.Message, "unknown method") {
			return errors.New("this machine runs an older Lanesh that does not support this yet; it updates itself to the server's version automatically, so try again in a minute")
		}
		return errors.New(pe.Message)
	}
	return err
}

func (t *Tools) audit(ctx context.Context, c caller, action string, m Machine, detail map[string]any, err error) {
	result := "ok"
	if err != nil {
		result = "error: " + truncate(err.Error(), 300)
	}
	t.backend.Audit(ctx, store.AuditEntry{LaneID: c.laneID, Actor: c.keyID, Action: action, MachineID: m.ID, Detail: detail, Result: result})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
