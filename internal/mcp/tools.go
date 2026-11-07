package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// --- describe_machine ---

// DescribeInput selects a machine.
type DescribeInput struct {
	Machine string `json:"machine" jsonschema:"machine name or id"`
}

// SkillInfo is a skill in describe_machine.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Hash        string `json:"hash"`
}

// DescribeOutput is the machine briefing.
type DescribeOutput struct {
	Briefing    string                   `json:"briefing" jsonschema:"pass this as briefing in every other call for this machine"`
	Machine     MachineInfo              `json:"machine"`
	Environment protocol.Environment     `json:"environment"`
	Skills      []SkillInfo              `json:"skills"`
	Shadowed    []protocol.ShadowedSkill `json:"shadowed,omitempty"`
}

func (t *Tools) describeMachine(ctx context.Context, req *mcp.CallToolRequest, in DescribeInput) (*mcp.CallToolResult, DescribeOutput, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, DescribeOutput{}, err
	}
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, DescribeOutput{}, err
	}
	d, err := t.describe(ctx, c, m)
	if err != nil {
		return nil, DescribeOutput{}, err
	}
	out := DescribeOutput{
		Briefing: t.briefings.Token(c.laneID, m.ID, m.Name, d.Skills.Hash),
		Machine: MachineInfo{
			ID: m.ID, Name: m.Name, OS: m.OS, Arch: m.Arch, Online: true,
			AllowExec: d.Policy.AllowExec, ExposeSkills: d.Policy.ExposeSkills, SkillCount: len(d.Skills.Skills),
			Self: c.self != "" && (c.self == m.Name || c.self == m.ID),
		},
		Environment: d.Environment,
		Skills:      make([]SkillInfo, 0, len(d.Skills.Skills)),
		Shadowed:    d.Skills.Shadowed,
	}
	for _, s := range d.Skills.Skills {
		h := s.Hash
		if len(h) > 12 {
			h = h[:12]
		}
		out.Skills = append(out.Skills, SkillInfo{Name: s.Name, Description: s.Description, Source: s.Source, Path: s.Path, Hash: h})
	}
	return nil, out, nil
}

// --- read_skill ---

// ReadSkillInput selects a skill file.
type ReadSkillInput struct {
	Machine  string `json:"machine" jsonschema:"machine name or id"`
	Briefing string `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Skill    string `json:"skill" jsonschema:"skill name"`
	Path     string `json:"path,omitempty" jsonschema:"file path relative to the skill directory; omit to read SKILL.md and the file tree"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"byte offset to start reading from"`
	Limit    int64  `json:"limit,omitempty" jsonschema:"maximum bytes to read (default 262144, max 1048576)"`
}

func (t *Tools) readSkill(ctx context.Context, req *mcp.CallToolRequest, in ReadSkillInput) (*mcp.CallToolResult, protocol.SkillReadResult, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.SkillReadResult{}, err
	}
	m, err := t.briefed(ctx, c, in.Machine, in.Briefing)
	if err != nil {
		return nil, protocol.SkillReadResult{}, err
	}
	if err := requirePolicy(m, false); err != nil {
		return nil, protocol.SkillReadResult{}, err
	}
	var res protocol.SkillReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodSkillRead,
		protocol.SkillReadParams{Skill: in.Skill, Path: in.Path, Offset: in.Offset, Limit: in.Limit}, &res)
	return nil, res, protoErr(err)
}

// --- exec ---

// ExecInput runs a command.
type ExecInput struct {
	Machine    string            `json:"machine" jsonschema:"machine name or id"`
	Briefing   string            `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Command    string            `json:"command" jsonschema:"command line, in the syntax of the machine's shell"`
	Cwd        string            `json:"cwd,omitempty" jsonschema:"working directory; relative to the skill directory when skill is set, otherwise to the home directory"`
	Skill      string            `json:"skill,omitempty" jsonschema:"run inside this skill's directory"`
	Shell      string            `json:"shell,omitempty" jsonschema:"shell to use instead of the machine's default, e.g. bash, zsh, pwsh, cmd"`
	Env        map[string]string `json:"env,omitempty" jsonschema:"extra environment variables"`
	Stdin      string            `json:"stdin,omitempty" jsonschema:"text written to stdin"`
	TimeoutMS  int               `json:"timeout_ms,omitempty" jsonschema:"how long to wait for completion before returning (default 30000, max 600000); the command keeps running afterwards"`
	Background bool              `json:"background,omitempty" jsonschema:"return immediately and keep stdin open for job_write"`
	PTY        bool              `json:"pty,omitempty" jsonschema:"reserved; pseudo-terminals are not supported yet"`
}

func (t *Tools) exec(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, protocol.JobStatus, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.JobStatus{}, err
	}
	m, err := t.briefed(ctx, c, in.Machine, in.Briefing)
	if err != nil {
		return nil, protocol.JobStatus{}, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, protocol.JobStatus{}, err
	}
	var st protocol.JobStatus
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodExecStart, protocol.ExecParams{
		Command: in.Command, Cwd: in.Cwd, Skill: in.Skill, Shell: in.Shell, Env: in.Env, Stdin: in.Stdin,
		TimeoutMS: in.TimeoutMS, Background: in.Background, PTY: in.PTY, Owner: c.keyID,
	}, &st)
	detail := map[string]any{"command": truncate(in.Command, 1000), "cwd": in.Cwd, "skill": in.Skill}
	if err == nil {
		detail["job_id"], detail["status"] = st.JobID, st.Status
		if st.ExitCode != nil {
			detail["exit_code"] = *st.ExitCode
		}
	}
	t.audit(ctx, c, "exec", m, detail, err)
	if err != nil {
		return nil, protocol.JobStatus{}, protoErr(err)
	}
	return nil, st, nil
}

// --- jobs ---

// JobReadInput reads job output.
type JobReadInput struct {
	Machine string `json:"machine" jsonschema:"machine name or id"`
	JobID   string `json:"job_id"`
	Stream  string `json:"stream,omitempty" jsonschema:"stdout (default) or stderr"`
	Offset  int64  `json:"offset,omitempty" jsonschema:"byte offset; use next_offset from the previous call"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum bytes (default 65536, max 1048576)"`
	WaitMS  int    `json:"wait_ms,omitempty" jsonschema:"if there is no output beyond offset yet, wait up to this long (max 60000) for new output or for the job to finish"`
}

func (t *Tools) jobRead(ctx context.Context, req *mcp.CallToolRequest, in JobReadInput) (*mcp.CallToolResult, protocol.JobReadResult, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.JobReadResult{}, err
	}
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, protocol.JobReadResult{}, err
	}
	var res protocol.JobReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobRead,
		protocol.JobReadParams{JobID: in.JobID, Stream: in.Stream, Offset: in.Offset, Limit: in.Limit, WaitMS: in.WaitMS}, &res)
	return nil, res, protoErr(err)
}

// JobWriteInput writes to a job's stdin.
type JobWriteInput struct {
	Machine    string `json:"machine" jsonschema:"machine name or id"`
	JobID      string `json:"job_id"`
	Data       string `json:"data,omitempty" jsonschema:"text to write, include a trailing newline for line-based programs"`
	CloseStdin bool   `json:"close_stdin,omitempty"`
}

// OKOutput acknowledges an operation.
type OKOutput struct {
	OK bool `json:"ok"`
}

func (t *Tools) jobWrite(ctx context.Context, req *mcp.CallToolRequest, in JobWriteInput) (*mcp.CallToolResult, OKOutput, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, OKOutput{}, err
	}
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, OKOutput{}, err
	}
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobWrite,
		protocol.JobWriteParams{JobID: in.JobID, Data: in.Data, CloseStdin: in.CloseStdin}, nil)
	t.audit(ctx, c, "job.write", m, map[string]any{"job_id": in.JobID, "bytes": len(in.Data), "close_stdin": in.CloseStdin}, err)
	if err != nil {
		return nil, OKOutput{}, protoErr(err)
	}
	return nil, OKOutput{OK: true}, nil
}

// JobKillInput stops a job.
type JobKillInput struct {
	Machine string `json:"machine" jsonschema:"machine name or id"`
	JobID   string `json:"job_id"`
	Signal  string `json:"signal,omitempty" jsonschema:"TERM, INT or KILL (default KILL)"`
}

func (t *Tools) jobKill(ctx context.Context, req *mcp.CallToolRequest, in JobKillInput) (*mcp.CallToolResult, protocol.JobStatus, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.JobStatus{}, err
	}
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, protocol.JobStatus{}, err
	}
	var st protocol.JobStatus
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobKill, protocol.JobKillParams{JobID: in.JobID, Signal: in.Signal}, &st)
	t.audit(ctx, c, "job.kill", m, map[string]any{"job_id": in.JobID, "signal": in.Signal}, err)
	return nil, st, protoErr(err)
}

// JobListInput lists jobs.
type JobListInput struct {
	Machine string `json:"machine" jsonschema:"machine name or id"`
	All     bool   `json:"all,omitempty" jsonschema:"include jobs started with other access keys"`
}

func (t *Tools) jobList(ctx context.Context, req *mcp.CallToolRequest, in JobListInput) (*mcp.CallToolResult, protocol.JobListResult, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.JobListResult{}, err
	}
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, protocol.JobListResult{}, err
	}
	owner := c.keyID
	if in.All {
		owner = ""
	}
	var res protocol.JobListResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobList, protocol.JobListParams{Owner: owner}, &res)
	return nil, res, protoErr(err)
}

// --- files ---

// ReadFileInput reads a file.
type ReadFileInput struct {
	Machine  string `json:"machine" jsonschema:"machine name or id"`
	Briefing string `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Path     string `json:"path" jsonschema:"absolute path, or relative to the home directory"`
	Offset   int64  `json:"offset,omitempty"`
	Limit    int64  `json:"limit,omitempty" jsonschema:"maximum bytes (default 262144, max 1048576)"`
	Encoding string `json:"encoding,omitempty" jsonschema:"set to base64 to force base64 output"`
}

func (t *Tools) readFile(ctx context.Context, req *mcp.CallToolRequest, in ReadFileInput) (*mcp.CallToolResult, protocol.FileReadResult, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.FileReadResult{}, err
	}
	m, err := t.briefed(ctx, c, in.Machine, in.Briefing)
	if err != nil {
		return nil, protocol.FileReadResult{}, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, protocol.FileReadResult{}, err
	}
	var res protocol.FileReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileRead,
		protocol.FileReadParams{Path: in.Path, Offset: in.Offset, Limit: in.Limit, Encoding: in.Encoding}, &res)
	t.audit(ctx, c, "file.read", m, map[string]any{"path": in.Path, "bytes": len(res.Content)}, err)
	return nil, res, protoErr(err)
}

// WriteFileInput writes a file.
type WriteFileInput struct {
	Machine    string `json:"machine" jsonschema:"machine name or id"`
	Briefing   string `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Path       string `json:"path" jsonschema:"absolute path, or relative to the home directory"`
	Content    string `json:"content"`
	Encoding   string `json:"encoding,omitempty" jsonschema:"utf-8 (default) or base64"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	CreateDirs bool   `json:"create_dirs,omitempty" jsonschema:"create missing parent directories"`
	Mode       uint32 `json:"mode,omitempty" jsonschema:"Unix permission bits as a decimal number, e.g. 493 for 0755"`
}

func (t *Tools) writeFile(ctx context.Context, req *mcp.CallToolRequest, in WriteFileInput) (*mcp.CallToolResult, protocol.FileWriteResult, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.FileWriteResult{}, err
	}
	m, err := t.briefed(ctx, c, in.Machine, in.Briefing)
	if err != nil {
		return nil, protocol.FileWriteResult{}, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, protocol.FileWriteResult{}, err
	}
	var res protocol.FileWriteResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileWrite, protocol.FileWriteParams{
		Path: in.Path, Content: in.Content, Encoding: in.Encoding, Overwrite: in.Overwrite, CreateDirs: in.CreateDirs, Mode: in.Mode,
	}, &res)
	t.audit(ctx, c, "file.write", m, map[string]any{"path": in.Path, "bytes": res.Size, "overwrite": in.Overwrite}, err)
	return nil, res, protoErr(err)
}

// TransferInput copies between machines.
type TransferInput struct {
	FromMachine  string `json:"from_machine" jsonschema:"source machine name or id"`
	FromBriefing string `json:"from_briefing" jsonschema:"briefing token of the source machine"`
	FromPath     string `json:"from_path" jsonschema:"file or directory to copy"`
	ToMachine    string `json:"to_machine" jsonschema:"destination machine name or id"`
	ToBriefing   string `json:"to_briefing" jsonschema:"briefing token of the destination machine"`
	ToPath       string `json:"to_path" jsonschema:"full destination path (not the parent directory)"`
	Overwrite    bool   `json:"overwrite,omitempty" jsonschema:"replace to_path if it exists"`
}

func (t *Tools) transfer(ctx context.Context, req *mcp.CallToolRequest, in TransferInput) (*mcp.CallToolResult, protocol.TransferResult, error) {
	c, err := callerFrom(req)
	if err != nil {
		return nil, protocol.TransferResult{}, err
	}
	// Check both machines so that one reply carries every briefing needed.
	from, fromErr := t.briefed(ctx, c, in.FromMachine, in.FromBriefing)
	to, toErr := t.briefed(ctx, c, in.ToMachine, in.ToBriefing)
	if err := joinBriefings(fromErr, toErr); err != nil {
		return nil, protocol.TransferResult{}, err
	}
	for _, m := range []Machine{from, to} {
		if err := requirePolicy(m, true); err != nil {
			return nil, protocol.TransferResult{}, err
		}
	}
	res, err := t.backend.Transfer(ctx, c.laneID, from, to, in.FromPath, in.ToPath, in.Overwrite)
	t.audit(ctx, c, "transfer", from, map[string]any{
		"from_path": in.FromPath, "to_machine": to.Name, "to_path": in.ToPath, "bytes": res.Bytes, "files": res.Files,
	}, err)
	if err != nil {
		return nil, protocol.TransferResult{}, fmt.Errorf("transfer failed: %w", protoErr(err))
	}
	return nil, res, nil
}
