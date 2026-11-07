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
}

// Header names understood by the MCP endpoint.
const (
	HeaderMachine = "X-Lanesh-Machine"
	ExtraLane     = "lane"
	ExtraKey      = "key"
)

// Instructions are returned to clients on initialize.
const Instructions = `Lanesh gives you access to several machines in one lane. Each machine has its own globally installed agent skills, its own shell and its own files.

1. Call list_machines to see which machines exist and which are online.
2. Before using a machine, call describe_machine for it. It returns the machine's environment, what it allows, its installed skills, and a briefing token. Every other tool that touches that machine requires the briefing token, so describe a machine before working with it.
3. Skills live on the machine where they are installed. Read them with read_skill. When a skill tells you to run a script, run it on that same machine with exec, passing skill so that the working directory is the skill directory. Use the machine's shell syntax (see describe_machine).
4. Files are not shared between machines. Use transfer to copy files or directories from one machine to another; use read_file and write_file only for small files.
5. Long-running commands keep running in the background after exec returns; follow them with job_read and stop them with job_kill.`

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
	now       func() time.Time
}

// NewServer builds the MCP server with all tools registered.
func NewServer(b Backend, briefings *auth.Briefings) *mcp.Server {
	t := &Tools{backend: b, briefings: briefings, now: time.Now}
	s := mcp.NewServer(&mcp.Implementation{Name: "lanesh", Title: "Lanesh", Version: version.Version}, &mcp.ServerOptions{
		Instructions: Instructions,
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
			"Returns a briefing token that the other machine tools require. Call this before using a machine, and again when told its skills changed.",
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

// briefed resolves an online machine and verifies the caller's briefing token.
// The returned notice is non-empty when the machine's skills changed since.
func (t *Tools) briefed(ctx context.Context, c caller, ref, briefing string) (Machine, string, error) {
	m, err := t.online(ctx, c, ref)
	if err != nil {
		return Machine{}, "", err
	}
	b, err := t.briefings.Verify(c.laneID, m.ID, briefing)
	if err != nil {
		return Machine{}, "", fmt.Errorf("machine %q has not been described in this conversation: call describe_machine(%q) first and pass its briefing token", m.Name, m.Name)
	}
	notice := ""
	if b.SkillsTag != auth.SkillsTag(m.SkillsHash) {
		notice = fmt.Sprintf("The skills installed on %q changed since you described it. Call describe_machine(%q) again to refresh.", m.Name, m.Name)
	}
	return m, notice, nil
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
