// Package mcp exposes a lane to agents as MCP tools.
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
	// UpdateSkills records a skill index just read from a machine, so that
	// briefing tokens are issued and checked against the same state.
	UpdateSkills(ctx context.Context, laneID, machineID string, idx protocol.SkillIndex)
	// SkillHistory returns the machine's recent skill index versions, newest first.
	SkillHistory(ctx context.Context, laneID, machineID string) []protocol.SkillIndex
	Transfer(ctx context.Context, laneID string, from, to Machine, fromPath, toPath string, overwrite bool) (protocol.TransferResult, error)
	Audit(ctx context.Context, e store.AuditEntry)
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
	return s
}

func (t *Tools) register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_machines",
		Description: "List the machines in this lane with their status, platform, permissions and number of installed skills. Start here.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.listMachines)
	mcp.AddTool(s, &mcp.Tool{
		Name: "describe_machine",
		Description: "Describe a machine: its OS, shell, home directory, what it allows, and its installed global skills (name and description). " +
			"Returns a short briefing token such as \"build-box:k3f9q2ab\" that every other tool touching this machine requires. " +
			"The token stays the same until the machine's skills change.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.describeMachine)
	mcp.AddTool(s, &mcp.Tool{
		Name: "read_skill",
		Description: "Read a skill installed on a machine. Without path it returns SKILL.md, the skill's absolute directory and its file tree; " +
			"with path (relative to the skill directory) it returns that file. Large files can be read in parts with offset and limit.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.readSkill)
	mcp.AddTool(s, &mcp.Tool{
		Name: "exec",
		Description: "Run a shell command on a machine using its default shell (or shell). Waits up to timeout_ms (default 30000) for the command to finish; " +
			"if it is still running, it continues in the background and the result has status \"running\" and a job_id for job_read/job_kill. " +
			"stdin is closed after the optional stdin text unless background is true. Set skill to run inside that skill's directory (cwd is then relative to it). " +
			"Output beyond the inline limit is elided in the middle; read all of it with job_read.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
	}, t.exec)
	mcp.AddTool(s, &mcp.Tool{
		Name: "job_read",
		Description: "Read a job's stdout or stderr starting at offset. Use next_offset for the following call; eof is true once the job finished and everything was read. " +
			"Set wait_ms to wait for new output instead of polling.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.jobRead)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_write",
		Description: "Write text to the stdin of a background job (started with background: true), optionally closing stdin afterwards.",
	}, t.jobWrite)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_kill",
		Description: "Stop a job and its child processes. signal may be TERM, INT or KILL (default KILL).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true)},
	}, t.jobKill)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_list",
		Description: "List recent jobs on a machine that were started with your access key (or all jobs with all: true).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.jobList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_file",
		Description: "Read a small file (up to 1 MiB per call) on a machine. Relative paths and ~ resolve against the machine's home directory. Binary content is returned as base64.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.readFile)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "write_file",
		Description: "Write a small file (up to 1 MiB) on a machine atomically. Fails if the file exists unless overwrite is true. Use encoding base64 for binary content.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true)},
	}, t.writeFile)
	mcp.AddTool(s, &mcp.Tool{
		Name: "transfer",
		Description: "Copy a file or directory from one machine to another (or within one machine), streamed through the server. " +
			"to_path is the full destination path, not its parent: copying the directory ~/out into ~/inbox means to_path ~/inbox/out. " +
			"It must not exist unless overwrite is true. The result reports the number of files, their total bytes and the stream's sha256.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true)},
	}, t.transfer)
}

func ptr[T any](v T) *T { return &v }

// --- list_machines ---

// MachineInfo is a machine in list_machines.
type MachineInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Online       bool   `json:"online"`
	LastSeen     string `json:"last_seen,omitempty"`
	AllowExec    bool   `json:"allow_exec"`
	ExposeSkills bool   `json:"expose_skills"`
	SkillCount   int    `json:"skill_count"`
	Self         bool   `json:"self,omitempty"`
}

// ListMachinesOutput is the result of list_machines.
type ListMachinesOutput struct {
	Machines []MachineInfo `json:"machines"`
}

func (t *Tools) listMachines(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListMachinesOutput, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, ListMachinesOutput{}, err
	}
	ms, err := t.backend.Machines(ctx, c.laneID)
	if err != nil {
		return nil, ListMachinesOutput{}, err
	}
	out := ListMachinesOutput{Machines: make([]MachineInfo, 0, len(ms))}
	for _, m := range ms {
		info := MachineInfo{
			ID: m.ID, Name: m.Name, OS: m.OS, Arch: m.Arch, Online: m.Online,
			AllowExec: m.Policy.AllowExec, ExposeSkills: m.Policy.ExposeSkills, SkillCount: m.SkillCount,
			Self: c.self != "" && (strings.EqualFold(c.self, m.Name) || c.self == m.ID),
		}
		if !m.Online && !m.LastSeen.IsZero() {
			info.LastSeen = m.LastSeen.UTC().Format(time.RFC3339)
		}
		out.Machines = append(out.Machines, info)
	}
	return nil, out, nil
}

// --- helpers ---

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
		last := "never"
		if !m.LastSeen.IsZero() {
			last = m.LastSeen.UTC().Format(time.RFC3339)
		}
		return Machine{}, fmt.Errorf("machine %q is offline (last seen %s)", m.Name, last)
	}
	return m, nil
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

func protoErr(err error) error {
	var pe *protocol.Error
	if errors.As(err, &pe) {
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
