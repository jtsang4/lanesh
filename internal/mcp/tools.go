package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// --- list_machines ---

func (t *Tools) listMachines(ctx context.Context, c caller, _ struct{}) (*text, error) {
	ms, err := t.backend.Machines(ctx, c.laneID)
	if err != nil {
		return nil, err
	}
	var out text
	if len(ms) == 0 {
		out.line("[lanesh] this lane has no machines yet.")
		return &out, nil
	}
	out.line("[lanesh] %s in this lane:", plural(len(ms), "machine", "machines"))
	for _, m := range ms {
		out.raw(machineLine(c, m))
	}
	return &out, nil
}

func machineLine(c caller, m Machine) string {
	name := m.Name
	if c.isSelf(m) {
		name += " (you)"
	}
	parts := []string{m.OS + "/" + m.Arch}
	if m.Shell != "" {
		parts = append(parts, m.Shell)
	}
	if m.Online {
		parts = append(parts, "online")
	} else {
		parts = append(parts, "offline since "+lastSeen(m))
	}
	if m.Policy.ExposeSkills {
		parts = append(parts, plural(m.SkillCount, "skill", "skills"))
	} else {
		parts = append(parts, "skills not shared")
	}
	if !m.Policy.AllowExec {
		parts = append(parts, "no commands or file access")
	}
	return "- " + name + ": " + strings.Join(parts, ", ")
}

// --- describe_machine ---

// DescribeInput selects a machine.
type DescribeInput struct {
	Machine string `json:"machine" jsonschema:"machine name"`
}

func (t *Tools) describeMachine(ctx context.Context, c caller, in DescribeInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	d, err := t.describe(ctx, c, m)
	if err != nil {
		return nil, err
	}
	var out text
	token := t.briefings.Token(c.laneID, m.ID, m.Name, d.Skills.Hash)
	out.line("[lanesh] briefing for %q", m.Name)
	out.line("Briefing token: %s (pass it as briefing in calls for this machine)", token)
	writeMachine(&out, m, d)
	return &out, nil
}

// --- find_skills ---

// FindInput searches skills.
type FindInput struct {
	Query   string `json:"query,omitempty" jsonschema:"keywords"`
	Machine string `json:"machine,omitempty" jsonschema:"only this machine"`
}

func (t *Tools) findSkills(ctx context.Context, c caller, in FindInput) (*text, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" && strings.TrimSpace(in.Machine) == "" {
		return nil, errors.New("give query, machine, or both")
	}
	ms, err := t.backend.Machines(ctx, c.laneID)
	if err != nil {
		return nil, err
	}
	if in.Machine != "" {
		m, err := t.backend.Resolve(ctx, c.laneID, in.Machine)
		if err != nil {
			return nil, err
		}
		ms = []Machine{m}
	}
	shared := ms[:0:0]
	for _, m := range ms {
		if m.Policy.ExposeSkills {
			shared = append(shared, m)
		}
	}
	if len(shared) == 0 {
		if in.Machine != "" {
			return nil, fmt.Errorf("machine %q does not share its skills", ms[0].Name)
		}
		return nil, errors.New("no machine in this lane shares skills")
	}
	matches, partial := findSkills(shared, t.backend.LaneSkills(ctx, c.laneID), query)

	var out text
	scope := "all machines"
	if in.Machine != "" {
		scope = fmt.Sprintf("%q", shared[0].Name)
		if !shared[0].Online {
			scope += " (offline; from its last known state)"
		}
	}
	switch {
	case query == "":
		out.line("[lanesh] %s on %s:", plural(len(matches), "skill", "skills"), scope)
	case len(matches) == 0:
		out.line("[lanesh] no skill on %s matches %q (keyword match on names and descriptions).", scope, query)
	case partial:
		out.line("[lanesh] no skill on %s matches most of %q; closest by keyword:", scope, query)
	default:
		out.line("[lanesh] keyword matches for %q on %s (%d):", query, scope, len(matches))
	}
	shown := 0
	for _, sm := range matches {
		line := "- " + sm.name
		if in.Machine == "" {
			line += " (" + machineList(sm) + ")"
		}
		line += ": " + shorten(sm.description, descMaxRunes)
		if out.tokens()+estimateTokens(line) > resultBudget {
			break
		}
		out.raw(line)
		shown++
	}
	if rest := len(matches) - shown; rest > 0 {
		if query == "" {
			out.line("[lanesh] %d more skills not shown (to fit the budget); add a query to narrow them.", rest)
		} else {
			out.line("[lanesh] %d more matches not shown (to fit the budget); refine the query.", rest)
		}
	}
	if query != "" {
		out.line("Keyword matching can miss skills. To choose reliably, use describe_machine, then read_skill.")
	}
	return &out, nil
}

func machineList(sm *skillMatch) string {
	names := make([]string, len(sm.machines))
	for i, n := range sm.machines {
		names[i] = n
		if sm.offline[n] {
			names[i] += " (offline)"
		}
	}
	s := "on " + strings.Join(names, ", ")
	if len(sm.machines) > 1 {
		if len(sm.hashes) > 1 {
			s += "; versions differ"
		} else {
			s += "; identical"
		}
	}
	return s
}

// --- read_skill ---

// ReadSkillInput selects what to read from a skill.
type ReadSkillInput struct {
	Machine  string   `json:"machine" jsonschema:"machine name"`
	Briefing string   `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Skill    string   `json:"skill" jsonschema:"skill name"`
	Path     string   `json:"path,omitempty" jsonschema:"file, or directory ending in /, relative to the skill"`
	Paths    []string `json:"paths,omitempty" jsonschema:"several files to read at once"`
	Offset   int64    `json:"offset,omitempty" jsonschema:"byte offset in path"`
	Limit    int64    `json:"limit,omitempty" jsonschema:"bytes to read from path (default 20480)"`
}

// maxPaths caps read_skill paths per call.
const maxPaths = 20

func (t *Tools) readSkill(ctx context.Context, c caller, in ReadSkillInput) (*text, error) {
	m, err := t.briefed(ctx, c, "read_skill", "briefing", in.Machine, in.Briefing)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, false); err != nil {
		return nil, err
	}
	if in.Skill == "" {
		return nil, errors.New("skill is required")
	}
	read := func(p protocol.SkillReadParams) (protocol.SkillReadResult, error) {
		p.Skill = in.Skill
		var res protocol.SkillReadResult
		err := t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodSkillRead, p, &res)
		return res, protoErr(err)
	}
	var out text
	if len(in.Paths) > 0 {
		if in.Path != "" {
			return nil, errors.New("use path or paths, not both")
		}
		return t.readSkillFiles(&out, m, in, read)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = fileReadBudget
	}
	res, err := read(protocol.SkillReadParams{Path: in.Path, Offset: in.Offset, Limit: limit})
	if err != nil {
		return nil, err
	}
	switch {
	case res.IsDir:
		where := "/" + res.Path
		if res.Path != "" {
			where = res.Path + "/"
		}
		out.line("[lanesh] skill %q on %q, directory %s (skill directory %s)", res.Skill, m.Name, where, res.Dir)
		writeTree(&out, res.Path, res.Files, res.FilesTruncated)
	case in.Path == "":
		out.line("[lanesh] skill %q on %q: SKILL.md and its files (skill directory %s)", res.Skill, m.Name, res.Dir)
		writeFile(&out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, false)
		out.line("Files:")
		writeTree(&out, "", res.Files, res.FilesTruncated)
	default:
		out.line("[lanesh] skill %q on %q (skill directory %s)", res.Skill, m.Name, res.Dir)
		writeFile(&out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, false)
	}
	return &out, nil
}

// readSkillFiles reads several files within one fileReadBudget.
func (t *Tools) readSkillFiles(out *text, m Machine, in ReadSkillInput, read func(protocol.SkillReadParams) (protocol.SkillReadResult, error)) (*text, error) {
	paths := in.Paths
	var skipped []string
	if len(paths) > maxPaths {
		skipped = append(skipped, paths[maxPaths:]...)
		paths = paths[:maxPaths]
	}
	out.line("[lanesh] skill %q on %q: %s", in.Skill, m.Name, plural(len(paths), "file", "files"))
	budget := int64(fileReadBudget)
	for i, p := range paths {
		if budget <= 0 {
			skipped = append(skipped, paths[i:]...)
			break
		}
		res, err := read(protocol.SkillReadParams{Path: p, Limit: budget})
		switch {
		case err != nil:
			out.line("[lanesh] error: %s", err)
			continue
		case res.IsDir:
			out.line("[lanesh] %s is a directory; list it with path=%q", p, strings.TrimSuffix(p, "/")+"/")
			continue
		}
		writeFile(out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, false)
		budget -= int64(len(res.Content))
	}
	if len(skipped) > 0 {
		out.line("[lanesh] not read, to fit the budget: %s. Read them in another call.", strings.Join(skipped, ", "))
	}
	return out, nil
}

// writeFile writes a file section. Binary content arrives as base64; it is
// shown only with showBinary, otherwise just described.
func writeFile(out *text, name, content, encoding string, offset, total int64, showBinary bool) {
	n := int64(len(content))
	if encoding == protocol.EncodingBase64 {
		raw := n * 3 / 4
		if !showBinary {
			out.line("[lanesh] %s is binary (%s); not shown. Copy it with transfer if you need it.", name, size(total))
			return
		}
		n = raw
	}
	span := ""
	end := offset + n
	if offset > 0 || end < total {
		span = fmt.Sprintf(", bytes %d-%d", offset, end)
	}
	out.line("[lanesh] begin %s (%s%s)", name, size(total), span)
	if encoding == protocol.EncodingBase64 {
		out.line("(base64)")
	}
	out.raw(content)
	out.line("[lanesh] end %s", name)
	if end < total {
		out.line("[lanesh] %s continues: %d more bytes; read on with offset=%d", name, total-end, end)
	}
}

func writeTree(out *text, root string, files []protocol.FileEntry, truncated bool) {
	lines, collapsed := renderTree(root, files)
	if len(lines) == 0 {
		out.line("(empty)")
	}
	for _, l := range lines {
		out.raw(l)
	}
	if collapsed > 0 {
		out.line("[lanesh] %s collapsed; list one with the path shown.", plural(collapsed, "directory", "directories"))
	}
	if truncated {
		out.line("[lanesh] the listing stopped at %d entries; list a subdirectory to see the rest.", len(files))
	}
}

// --- grep_skill ---

// GrepInput searches a skill.
type GrepInput struct {
	Machine       string `json:"machine" jsonschema:"machine name"`
	Briefing      string `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Skill         string `json:"skill" jsonschema:"skill name"`
	Pattern       string `json:"pattern" jsonschema:"text to find"`
	Path          string `json:"path,omitempty" jsonschema:"only this file or directory"`
	Regex         bool   `json:"regex,omitempty" jsonschema:"pattern is a Go regular expression"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxMatches    int    `json:"max_matches,omitempty" jsonschema:"default 100"`
}

func (t *Tools) grepSkill(ctx context.Context, c caller, in GrepInput) (*text, error) {
	m, err := t.briefed(ctx, c, "grep_skill", "briefing", in.Machine, in.Briefing)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, false); err != nil {
		return nil, err
	}
	if in.Skill == "" || in.Pattern == "" {
		return nil, errors.New("skill and pattern are required")
	}
	var res protocol.SkillGrepResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodSkillGrep, protocol.SkillGrepParams{
		Skill: in.Skill, Pattern: in.Pattern, Path: in.Path, Regex: in.Regex, IgnoreCase: !in.CaseSensitive, MaxMatches: in.MaxMatches,
	}, &res)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	out.line("[lanesh] %q in skill %q on %q: %s in %d of %s", in.Pattern, res.Skill, m.Name,
		plural(len(res.Matches), "match", "matches"), res.FilesMatched, plural(res.FilesSearched, "file", "files"))
	shown := 0
	for _, g := range res.Matches {
		line := fmt.Sprintf("%s:%d: %s", g.Path, g.Line, g.Text)
		if out.tokens()+estimateTokens(line) > resultBudget {
			break
		}
		out.raw(line)
		shown++
	}
	if rest := len(res.Matches) - shown; rest > 0 {
		out.line("[lanesh] %d more matches not shown (to fit the budget); narrow pattern or path.", rest)
	}
	if res.Truncated {
		out.line("[lanesh] the search stopped at %d matches; there are more. Narrow pattern or path, or raise max_matches.", len(res.Matches))
	}
	if len(res.Skipped) > 0 {
		out.line("[lanesh] not searched: %s", strings.Join(res.Skipped, ", "))
	}
	return &out, nil
}

// --- exec ---

// ExecInput runs a command.
type ExecInput struct {
	Machine    string            `json:"machine" jsonschema:"machine name"`
	Briefing   string            `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Command    string            `json:"command" jsonschema:"in the syntax of the machine's shell"`
	Cwd        string            `json:"cwd,omitempty" jsonschema:"relative to the skill directory with skill, else to home"`
	Skill      string            `json:"skill,omitempty" jsonschema:"run in this skill's directory"`
	Shell      string            `json:"shell,omitempty" jsonschema:"instead of the default, e.g. bash, pwsh, cmd"`
	Env        map[string]string `json:"env,omitempty"`
	Stdin      string            `json:"stdin,omitempty"`
	TimeoutMS  int               `json:"timeout_ms,omitempty" jsonschema:"wait this long (default 30000, max 600000); the command keeps running after"`
	Background bool              `json:"background,omitempty" jsonschema:"return at once; stdin stays open for job_write"`
}

func (t *Tools) exec(ctx context.Context, c caller, in ExecInput) (*text, error) {
	m, err := t.briefed(ctx, c, "exec", "briefing", in.Machine, in.Briefing)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	var st protocol.JobStatus
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodExecStart, protocol.ExecParams{
		Command: in.Command, Cwd: in.Cwd, Skill: in.Skill, Shell: in.Shell, Env: in.Env, Stdin: in.Stdin,
		TimeoutMS: in.TimeoutMS, Background: in.Background, Owner: c.keyID,
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
		return nil, protoErr(err)
	}
	var out text
	head := fmt.Sprintf("[lanesh] on %q: %s", m.Name, jobState(st.Status, st.ExitCode, st.DurationMS))
	if st.Status == protocol.JobRunning || st.OutputTruncated {
		head += " (job " + st.JobID + ")"
	}
	out.raw(head)
	out.line("cwd %s, shell %s", st.Cwd, st.Shell)
	writeStream(&out, "stdout", st.Stdout, st.StdoutBytes)
	writeStream(&out, "stderr", st.Stderr, st.StderrBytes)
	if st.Status == protocol.JobRunning {
		out.line("[lanesh] follow with job_read job_id=%q offset=%d wait_ms=30000 (stream=\"stderr\" offset=%d for stderr); stop with job_kill.",
			st.JobID, st.StdoutBytes, st.StderrBytes)
	}
	return &out, nil
}

// jobState describes a job's state, such as "exited 0 in 1.2s".
func jobState(status string, code *int, durMS int64) string {
	dur := time.Duration(durMS) * time.Millisecond
	if dur < time.Second {
		dur = dur.Round(time.Millisecond)
	} else {
		dur = dur.Round(100 * time.Millisecond)
	}
	switch {
	case status == protocol.JobRunning:
		return "still running after " + dur.String()
	case status == protocol.JobExited && code != nil:
		return fmt.Sprintf("exited %d in %s", *code, dur)
	case status == protocol.JobLost:
		return "lost (the daemon restarted) after " + dur.String()
	}
	return status + " after " + dur.String()
}

func writeStream(out *text, name, data string, total int64) {
	if total == 0 {
		out.line("%s: empty", name)
		return
	}
	out.line("[lanesh] begin %s (%s)", name, size(total))
	out.raw(data)
	out.line("[lanesh] end %s", name)
}

// --- jobs ---

// JobReadInput reads job output.
type JobReadInput struct {
	Machine string `json:"machine" jsonschema:"machine name"`
	JobID   string `json:"job_id"`
	Stream  string `json:"stream,omitempty" jsonschema:"stdout (default) or stderr"`
	Offset  int64  `json:"offset,omitempty" jsonschema:"byte offset"`
	Limit   int    `json:"limit,omitempty" jsonschema:"bytes (default 20480)"`
	WaitMS  int    `json:"wait_ms,omitempty" jsonschema:"wait up to this long (max 60000) for new output"`
}

func (t *Tools) jobRead(ctx context.Context, c caller, in JobReadInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	var res protocol.JobReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobRead,
		protocol.JobReadParams{JobID: in.JobID, Stream: in.Stream, Offset: in.Offset, Limit: in.Limit, WaitMS: in.WaitMS}, &res)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	state := res.Status
	if res.Status == protocol.JobExited && res.ExitCode != nil {
		state = fmt.Sprintf("exited %d", *res.ExitCode)
	}
	out.line("[lanesh] job %s on %q (%s): %s bytes %d-%d of %d", res.JobID, m.Name, state, res.Stream, res.Offset, res.NextOffset, res.Total)
	if res.Offset > in.Offset {
		out.line("[lanesh] bytes %d-%d were dropped from the job's buffer and cannot be read.", in.Offset, res.Offset)
	}
	if res.Data != "" {
		out.line("[lanesh] begin %s", res.Stream)
		out.raw(res.Data)
		out.line("[lanesh] end %s", res.Stream)
	}
	switch {
	case res.NextOffset < res.Total:
		out.line("[lanesh] %d more bytes; continue with offset=%d", res.Total-res.NextOffset, res.NextOffset)
	case res.EOF:
		out.line("[lanesh] end of %s; the job has finished.", res.Stream)
	default:
		out.line("[lanesh] no more %s yet; the job is running. Continue with offset=%d (wait_ms waits for more).", res.Stream, res.NextOffset)
	}
	return &out, nil
}

// JobWriteInput writes to a job's stdin.
type JobWriteInput struct {
	Machine    string `json:"machine" jsonschema:"machine name"`
	JobID      string `json:"job_id"`
	Data       string `json:"data,omitempty" jsonschema:"include a trailing newline for line-based programs"`
	CloseStdin bool   `json:"close_stdin,omitempty"`
}

func (t *Tools) jobWrite(ctx context.Context, c caller, in JobWriteInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobWrite,
		protocol.JobWriteParams{JobID: in.JobID, Data: in.Data, CloseStdin: in.CloseStdin}, nil)
	t.audit(ctx, c, "job.write", m, map[string]any{"job_id": in.JobID, "bytes": len(in.Data), "close_stdin": in.CloseStdin}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	msg := fmt.Sprintf("[lanesh] wrote %s to job %s", size(int64(len(in.Data))), in.JobID)
	if in.CloseStdin {
		msg += " and closed its stdin"
	}
	out.line("%s.", msg)
	return &out, nil
}

// JobKillInput stops a job.
type JobKillInput struct {
	Machine string `json:"machine" jsonschema:"machine name"`
	JobID   string `json:"job_id"`
	Signal  string `json:"signal,omitempty" jsonschema:"TERM, INT or KILL (default)"`
}

func (t *Tools) jobKill(ctx context.Context, c caller, in JobKillInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	var st protocol.JobStatus
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobKill, protocol.JobKillParams{JobID: in.JobID, Signal: in.Signal}, &st)
	t.audit(ctx, c, "job.kill", m, map[string]any{"job_id": in.JobID, "signal": in.Signal}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	out.line("[lanesh] job %s: %s", st.JobID, jobState(st.Status, st.ExitCode, st.DurationMS))
	return &out, nil
}

// JobListInput lists jobs.
type JobListInput struct {
	Machine string `json:"machine" jsonschema:"machine name"`
	All     bool   `json:"all,omitempty" jsonschema:"include jobs of other access keys"`
}

func (t *Tools) jobList(ctx context.Context, c caller, in JobListInput) (*text, error) {
	m, err := t.online(ctx, c, in.Machine)
	if err != nil {
		return nil, err
	}
	owner := c.keyID
	if in.All {
		owner = ""
	}
	var res protocol.JobListResult
	if err := t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodJobList, protocol.JobListParams{Owner: owner}, &res); err != nil {
		return nil, protoErr(err)
	}
	var out text
	out.line("[lanesh] %s on %q:", plural(len(res.Jobs), "job", "jobs"), m.Name)
	for _, j := range res.Jobs {
		out.line("- %s: %s; %s (cwd %s)", j.JobID, jobState(j.Status, j.ExitCode, j.DurationMS), shorten(j.Command, 200), j.Cwd)
	}
	return &out, nil
}

// --- files ---

// ReadFileInput reads a file.
type ReadFileInput struct {
	Machine  string `json:"machine" jsonschema:"machine name"`
	Briefing string `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Path     string `json:"path" jsonschema:"absolute, or relative to home"`
	Offset   int64  `json:"offset,omitempty"`
	Limit    int64  `json:"limit,omitempty" jsonschema:"bytes (default 20480)"`
	Encoding string `json:"encoding,omitempty" jsonschema:"base64 to force base64"`
}

func (t *Tools) readFile(ctx context.Context, c caller, in ReadFileInput) (*text, error) {
	m, err := t.briefed(ctx, c, "read_file", "briefing", in.Machine, in.Briefing)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = fileReadBudget
	}
	var res protocol.FileReadResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileRead,
		protocol.FileReadParams{Path: in.Path, Offset: in.Offset, Limit: limit, Encoding: in.Encoding}, &res)
	t.audit(ctx, c, "file.read", m, map[string]any{"path": in.Path, "bytes": len(res.Content)}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	out.line("[lanesh] file on %q", m.Name)
	writeFile(&out, res.Path, res.Content, res.Encoding, res.Offset, res.Size, true)
	return &out, nil
}

// WriteFileInput writes a file.
type WriteFileInput struct {
	Machine    string `json:"machine" jsonschema:"machine name"`
	Briefing   string `json:"briefing" jsonschema:"briefing token from describe_machine"`
	Path       string `json:"path" jsonschema:"absolute, or relative to home"`
	Content    string `json:"content"`
	Encoding   string `json:"encoding,omitempty" jsonschema:"utf-8 (default) or base64"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	CreateDirs bool   `json:"create_dirs,omitempty"`
	Mode       uint32 `json:"mode,omitempty" jsonschema:"Unix permissions as decimal, e.g. 493 for 0755"`
}

func (t *Tools) writeFile(ctx context.Context, c caller, in WriteFileInput) (*text, error) {
	m, err := t.briefed(ctx, c, "write_file", "briefing", in.Machine, in.Briefing)
	if err != nil {
		return nil, err
	}
	if err := requirePolicy(m, true); err != nil {
		return nil, err
	}
	var res protocol.FileWriteResult
	err = t.backend.Call(ctx, c.laneID, m.ID, protocol.MethodFileWrite, protocol.FileWriteParams{
		Path: in.Path, Content: in.Content, Encoding: in.Encoding, Overwrite: in.Overwrite, CreateDirs: in.CreateDirs, Mode: in.Mode,
	}, &res)
	t.audit(ctx, c, "file.write", m, map[string]any{"path": in.Path, "bytes": res.Size, "overwrite": in.Overwrite}, err)
	if err != nil {
		return nil, protoErr(err)
	}
	var out text
	out.line("[lanesh] wrote %s to %s on %q.", size(res.Size), res.Path, m.Name)
	return &out, nil
}

// TransferInput copies between machines.
type TransferInput struct {
	FromMachine  string `json:"from_machine"`
	FromBriefing string `json:"from_briefing" jsonschema:"briefing token of from_machine"`
	FromPath     string `json:"from_path" jsonschema:"file or directory"`
	ToMachine    string `json:"to_machine"`
	ToBriefing   string `json:"to_briefing" jsonschema:"briefing token of to_machine"`
	ToPath       string `json:"to_path" jsonschema:"full destination path"`
	Overwrite    bool   `json:"overwrite,omitempty"`
}

func (t *Tools) transfer(ctx context.Context, c caller, in TransferInput) (*text, error) {
	// Check both machines so that one reply carries every briefing needed.
	from, fromErr := t.briefed(ctx, c, "transfer", "from_briefing", in.FromMachine, in.FromBriefing)
	to, toErr := t.briefed(ctx, c, "transfer", "to_briefing", in.ToMachine, in.ToBriefing)
	if err := joinBriefings(fromErr, toErr); err != nil {
		return nil, err
	}
	for _, m := range []Machine{from, to} {
		if err := requirePolicy(m, true); err != nil {
			return nil, err
		}
	}
	res, err := t.backend.Transfer(ctx, c.laneID, from, to, in.FromPath, in.ToPath, in.Overwrite)
	t.audit(ctx, c, "transfer", from, map[string]any{
		"from_path": in.FromPath, "to_machine": to.Name, "to_path": in.ToPath, "bytes": res.Bytes, "files": res.Files,
	}, err)
	if err != nil {
		return nil, fmt.Errorf("transfer failed: %w", protoErr(err))
	}
	var out text
	dest := res.Path
	if dest == "" {
		dest = in.ToPath
	}
	out.line("[lanesh] copied %s (%s) from %s on %q to %s on %q.", plural(res.Files, "file", "files"), size(res.Bytes),
		in.FromPath, from.Name, dest, to.Name)
	return &out, nil
}
